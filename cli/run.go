package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	sdk "go.temporal.io/sdk/client"
	"go.uber.org/zap"

	preemption "github.com/embano1/vsphere-preemption"
)

const (
	wfID               = "preempctl-run"
	eventType          = "PreempctlRunEvent"
	wfExecutionTimeout = time.Minute * 5
)

type runConfig struct {
	*wfConfig
	tag         string
	criticality string
	replyTo     string
	event       string
}

func NewRunCommand(wfConfig *wfConfig) *cobra.Command {
	cfg := &runConfig{
		wfConfig: wfConfig,
	}

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run preemption workflow",
		Long: `Send a signal to a preemption worker to trigger a preemption workflow. 
If the workflow is not running, it will be started.`,
		Example: `# trigger preemption on a custom Temporal server with run default workflow values
preemptctl workflow run --server temporal01.prod.corp.local:7233

# trigger preemption with a custom cloudevent provided to the workflow and request a reply to a specified broker
preemptctl workflow run --server temporal01.prod.corp.local:7233 --event \
'{"data":{"threshold":70,"current":87},"datacontenttype":"application/json","id":"757098cc-b275-41b6-ab52-f2966f9d714c","source":"preemptctl","specversion":"1.0","time":"2021-11-24T20:26:00.98041Z","type":"ThresholdExceededEvent"}' \
--reply-to https://broker.corp.local
`,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return validateRunFlags(cfg)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPreemption(cmd, cfg)
		},
	}

	flags := cmd.PersistentFlags()
	flags.StringVarP(&cfg.tag, "tag", "t", "preemptible", "vSphere tag to use to identify preemptible virtual machines")
	flags.StringVarP(&cfg.criticality, "criticality", "c", string(preemption.CriticalityLow), "criticality of the workflow request (LOW, MEDIUM, HIGH)")
	flags.StringVarP(&cfg.event, "event", "e", "", "custom CloudEvent JSON string provided in workflow request (optional)")
	flags.StringVar(&cfg.replyTo, "reply-to", "", "send preemption event to this address after workflow completion (optional)")

	return cmd
}

func validateRunFlags(cfg *runConfig) error {
	if err := checkNotEmpty("tag", cfg.tag); err != nil {
		return err
	}

	criticality := cfg.criticality
	if err := checkNotEmpty("criticality", criticality); err != nil {
		return err
	}

	critUpper := strings.ToUpper(criticality)
	validCriticality := map[preemption.Criticality]struct{}{
		preemption.CriticalityLow:    {},
		preemption.CriticalityMedium: {},
		preemption.CriticalityHigh:   {},
	}

	if _, ok := validCriticality[preemption.Criticality(critUpper)]; !ok {
		return fmt.Errorf("criticality %q invalid (valid: LOW, MEDIUM, HIGH)", criticality)
	}
	cfg.criticality = critUpper

	if cfg.event != "" {
		_, err := parseJsonEvent(cfg.event)
		if err != nil {
			return err
		}
	}

	return nil
}

func runPreemption(cmd *cobra.Command, cfg *runConfig) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), rpcTimeout)
	defer cancel()

	logger, err := getLogger(cmd)
	if err != nil {
		return fmt.Errorf("create logger: %w", err)
	}

	logger = logger.With(
		zap.String("address", cfg.address),
		zap.String("namespace", cfg.namespace),
		zap.String("queue", cfg.queue),
	)

	logger.Debug("creating temporal client")
	tc, err := newTemporalClient(ctx, cfg.address, cfg.namespace, logger)
	if err != nil {
		return fmt.Errorf("create temporal client: %w", err)
	}

	logger = logger.With(
		zap.String("workflow", preemption.WorkflowName),
		zap.String("workflowID", wfID),
	)

	var e ce.Event
	if cfg.event != "" {
		e, err = parseJsonEvent(cfg.event)
		if err != nil {
			return err
		}
	} else {
		logger.Debug("no input event specified, creating default cloudevent")
		e = ce.NewEvent()
		e.SetSource(wfID)
		e.SetID(uuid.New().String())
		e.SetType(eventType)
		e.SetTime(cfg.clock.Now().UTC())
	}

	req := preemption.WorkflowRequest{
		Tag:         cfg.tag,
		Event:       e,
		Criticality: preemption.Criticality(cfg.criticality),
		ReplyTo:     cfg.replyTo,
	}

	options := sdk.StartWorkflowOptions{
		ID:                       wfID,
		TaskQueue:                cfg.queue,
		WorkflowExecutionTimeout: wfExecutionTimeout,
		// WorkflowIDReusePolicy:
		// enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE, // multiple
		// executions handled in workflow
		WorkflowExecutionErrorWhenAlreadyStarted: false,
	}

	// fire and forget
	logger.Info(
		"executing workflow",
		zap.String("tag", cfg.tag),
		zap.String("criticality", cfg.criticality),
		zap.String("replyto", cfg.replyTo),
	)

	// wfID is used as the workflow name in the signal and a new workflow is started
	// unless it is already running
	wf, err := tc.SignalWithStartWorkflow(ctx, wfID, preemption.SignalChannel, req, options, preemption.WorkflowName)
	if err != nil {
		return fmt.Errorf("execute workflow: %w", err)
	}

	logger.Info("successfully triggered workflow", zap.String("workflowID", wf.GetID()), zap.String("workflowRunID", wf.GetRunID()))
	return nil
}
