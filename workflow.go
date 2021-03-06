package preemption

import (
	"encoding/json"
	"fmt"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/vmware/govmomi/vim25/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type Criticality string

const (
	CriticalityLow    Criticality = "LOW" // attempts graceful VM shutdown
	CriticalityMedium Criticality = "MEDIUM"
	CriticalityHigh   Criticality = "HIGH"

	WorkflowName      = "PreemptVMsWorkflow"
	SignalChannel     = "PreemptVMsChan"
	WorkFlowQueryType = "current_state"

	minTimeBetweenRuns = time.Minute // prevent multiple workflow executions within this window
)

// default activity retry policy
var defaultRetryPolicy = temporal.RetryPolicy{
	InitialInterval:    time.Second * 2,
	BackoffCoefficient: 2,
	MaximumInterval:    time.Second * 10,
	MaximumAttempts:    3,
}

type WorkflowRequest struct {
	Tag         string      `json:"tag"` // tag identifying preemptible VMs
	Criticality Criticality `json:"criticality"`
	Event       ce.Event    `json:"event"`   // e.g. AlarmStatusChangedEvent
	ReplyTo     string      `json:"replyTo"` // empty if no cloudevent response wanted
}

type WorkflowResponse struct {
	WorkflowID      string                         `json:"workflowID"`
	RunID           string                         `json:"workflowRunID"`
	WorkflowName    string                         `json:"workflowName"`
	LastPreemption  time.Time                      `json:"lastPreemptionTime"`
	VirtualMachines []types.ManagedObjectReference `json:"virtualMachines"`
	Tag             string                         `json:"tag"`
	Criticality     Criticality                    `json:"criticality"`
	Event           ce.Event                       `json:"event"`
	ReplyTo         string                         `json:"replyTo"`
}

func (res *WorkflowResponse) getCurrentState() (string, error) {
	b, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("marshal JSON workflow response: %w", err)
	}
	return string(b), nil
}

// PreemptVMsWorkflow preempts VMs
func PreemptVMsWorkflow(ctx workflow.Context) (*WorkflowResponse, error) {
	var (
		lastRun time.Time
	)

	info := workflow.GetInfo(ctx)
	res := &WorkflowResponse{
		WorkflowID:   info.WorkflowExecution.ID,
		RunID:        info.WorkflowExecution.RunID,
		WorkflowName: info.WorkflowType.Name,
		Event:        ce.NewEvent(),
	}

	logger := workflow.GetLogger(ctx)

	err := workflow.SetQueryHandler(ctx, WorkFlowQueryType, func() (string, error) {
		logger.Debug("received query", "queryType", WorkFlowQueryType)
		return res.getCurrentState()
	})
	if err != nil {
		return nil, err
	}

	sigCh := workflow.GetSignalChannel(ctx, SignalChannel)

	for ctx.Err() == nil {
		logger.Info("waiting for incoming signal", "channel", SignalChannel)
		sel := workflow.NewSelector(ctx)

		// context handling
		sel.AddReceive(ctx.Done(), func(_ workflow.ReceiveChannel, _ bool) {
			logger.Info("received cancellation signal")
			logger.Info("stopping workflow")
		})

		// workflow handling
		sel.AddReceive(sigCh, func(c workflow.ReceiveChannel, _ bool) {
			var (
				req         WorkflowRequest
				vc          *Client // vcenter client will be injected
				preemptible []types.ManagedObjectReference
				preempted   []types.ManagedObjectReference
			)

			c.Receive(ctx, &req)
			logger.Debug("received signal", "signal", req)

			// update workflow response stats
			defer func() {
				lastRun = workflow.Now(ctx)

				// 	persist last run information in case workflow is stopped/canceled
				res.LastPreemption = lastRun
				res.VirtualMachines = preempted
				res.Criticality = req.Criticality
				res.Tag = req.Tag
				res.Event = req.Event
				res.ReplyTo = req.ReplyTo
			}()

			now := workflow.Now(ctx)
			// don't run if still within window
			if now.Sub(lastRun) < minTimeBetweenRuns {
				logger.Info(
					"skipping workflow run because last run is not older than configured re-run threshold",
					"threshold",
					minTimeBetweenRuns,
					"currentRun",
					now.UTC().String(),
					"lastRun",
					lastRun.UTC().String(),
				)
				return
			}

			// execute activities
			options := workflow.ActivityOptions{
				StartToCloseTimeout: time.Minute * 5,
				HeartbeatTimeout:    time.Second * 5,
				WaitForCancellation: false,
				RetryPolicy:         &defaultRetryPolicy,
			}
			ctx = workflow.WithActivityOptions(ctx, options)

			logger.Debug("searching for preemptible virtual machines")
			if err := workflow.ExecuteActivity(ctx, vc.GetPreemptibleVMs, req.Tag).Get(ctx, &preemptible); err != nil {
				logger.Error("get preemptible vms", "error", err)
				return
			}
			logger.Debug("preemptible virtual machines result", "count", len(preemptible), "refs", preemptible)

			logger.Debug("preempting virtual machines")
			force := req.Criticality != CriticalityLow
			if err := workflow.ExecuteActivity(ctx, vc.PowerOffVMs, preemptible, force).Get(ctx, &preempted); err != nil {
				logger.Error("power off preemptible vms", "error", err)
				return
			}
			logger.Debug("preempted virtual machines result", "count", len(preempted), "refs", preempted)

			info := workflow.GetInfo(ctx)
			annotation := annotationData{
				Preempted:       true,
				Tag:             req.Tag,
				ForcedShutdown:  force,
				Criticality:     req.Criticality,
				WorkflowID:      info.WorkflowExecution.ID,
				WorkflowStarted: info.WorkflowStartTime.UTC(),
				Event:           req.Event,
			}

			logger.Debug("annotating preempted virtual machines")
			if err := workflow.ExecuteActivity(ctx, vc.AnnotateVms, preempted, annotation).Get(ctx, nil); err != nil {
				// log only, continue workflow
				logger.Warn("annotate virtual machines", "error", err)
			}

			if req.ReplyTo == "" {
				logger.Debug("not creating cloud event response: replyTo address not set")
				return
			}

			eventData := eventResponseData{
				annotationData:  annotation,
				VirtualMachines: preempted,
			}
			logger.Debug("sending cloudevents response")

			if err := workflow.ExecuteActivity(ctx, vc.SendPreemptedEvent, info.WorkflowExecution.ID, req.ReplyTo, eventData).Get(ctx, nil); err != nil {
				logger.Error("send cloudevent", "error", err)
				return
			}
		})

		// blocks on workflow ctx and signal chan
		sel.Select(ctx)
	}

	return res, nil
}
