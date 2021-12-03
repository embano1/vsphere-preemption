package preemption

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/benbjohnson/clock"
	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/protocol"
	"github.com/kelseyhightower/envconfig"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vapi/tags"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

const (
	eventType              = "com.vmware.workflows.vsphere.VmPreemptedEvent.v0" // returned event if requested
	customField            = "com.vmware.workflows.vsphere.preemption"          // custom field info in vm
	heartBeatInterval      = time.Second * 2
	maxPreemptVms          = 10 // never preempt more vms
	concurrentVCenterCalls = 5

	// custom temporal error types
	errVSphere  = "vsphere"
	errInternal = "internal"
)

type EnvConfig struct {
	// Temporal settings
	Address   string `envconfig:"TEMPORAL_URL" required:"true"`
	Namespace string `envconfig:"TEMPORAL_NAMESPACE" required:"true"`
	Queue     string `envconfig:"TEMPORAL_TASKQUEUE" required:"true"`

	// vSphere settings
	Insecure   bool   `envconfig:"VCENTER_INSECURE" default:"false"`
	VCAddress  string `envconfig:"VCENTER_URL" required:"true"`
	SecretPath string `envconfig:"VCENTER_SECRET_PATH" default:""`

	Debug bool `envconfig:"DEBUG" default:"false"`
}

type eventResponseData struct {
	annotationData
	VirtualMachines []types.ManagedObjectReference `json:"virtualMachines"`
}

type annotationData struct {
	Preempted       bool        `json:"preempted"`
	Tag             string      `json:"tag"`
	ForcedShutdown  bool        `json:"forcedShutdown" `
	Criticality     Criticality `json:"criticality"`
	WorkflowID      string      `json:"workflowID"`
	WorkflowStarted time.Time   `json:"workflowStarted"`
	Event           ce.Event    `json:"event"` // event that triggered preemption
}

type Client struct {
	vcclient   *vim25.Client
	tagManager *tags.Manager
	ceclient   ce.Client
	clock      clock.Clock
}

func NewClient(ctx context.Context) (*Client, error) {
	vclient, err := newSOAPClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create vsphere SOAP client: %w", err)
	}

	rc, err := newRESTClient(ctx, vclient.Client)
	if err != nil {
		return nil, fmt.Errorf("create vsphere REST client: %w", err)
	}
	tm := tags.NewManager(rc)

	p, err := ce.NewHTTP()
	if err != nil {
		return nil, fmt.Errorf("create cloudevents http protocol: %w", err)
	}

	ceclient, err := ce.NewClient(p)
	if err != nil {
		return nil, fmt.Errorf("create cloudevents client: %w", err)
	}

	client := Client{
		vcclient:   vclient.Client,
		tagManager: tm,
		ceclient:   ceclient,
		clock:      clock.New(),
	}

	return &client, nil
}

func (c *Client) GetPreemptibleVMs(ctx context.Context, tag string) ([]types.ManagedObjectReference, error) {
	logger := activity.GetLogger(ctx)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// send heartbeats
	go heartbeat(ctx)

	logger.Debug("searching for preemptible vms", "maxPreemptVMs", maxPreemptVms)
	tagRefs, err := c.tagManager.ListAttachedObjects(ctx, tag)
	if err != nil {
		return nil, fmt.Errorf("get tag %q: %w", tag, err)
	}

	refs := make([]types.ManagedObjectReference, 0, maxPreemptVms)
	logger.Debug("tag to vm mapping", "tag", tag, "vms", tagRefs)
	for i, ref := range tagRefs {
		if i == maxPreemptVms {
			logger.Debug("maximum search count for preemptible vms reached", "maxPreemptVMs", maxPreemptVms)
			break
		}
		refs = append(refs, ref.Reference())
	}

	return refs, nil
}

func (c *Client) PowerOffVMs(ctx context.Context, refs []types.ManagedObjectReference, force bool) ([]types.ManagedObjectReference, error) {
	logger := activity.GetLogger(ctx)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if len(refs) == 0 {
		logger.Debug("empty list of preemptible virtual machines")
		return nil, nil
	}

	// send heartbeats
	go heartbeat(ctx)

	var (
		wg         sync.WaitGroup
		poweredOff []types.ManagedObjectReference
	)

	refCh := make(chan types.ManagedObjectReference, len(refs))
	lim := newLimiter(concurrentVCenterCalls) // limit concurrent vc calls

	logger.Debug("powering off vms", "refs", refs)
	for _, ref := range refs {
		lim.acquire()
		wg.Add(1)
		go c.powerOffVm(ctx, ref, refCh, lim, &wg, force)
	}

	go func() {
		logger.Debug("waiting for operations to finish")
		wg.Wait()
		close(refCh)
	}()

	for ref := range refCh {
		poweredOff = append(poweredOff, ref)
	}

	return poweredOff, nil
}

func (c *Client) powerOffVm(ctx context.Context, ref types.ManagedObjectReference, refCh chan types.ManagedObjectReference, lim *limiter, wg *sync.WaitGroup, force bool) {
	defer func() {
		lim.release()
		wg.Done()
	}()

	logger := activity.GetLogger(ctx)
	o := object.NewVirtualMachine(c.vcclient, ref)

	state, err := o.PowerState(ctx)
	if err != nil {
		// log only and continue to attempt to power off vm
		logger.Warn("failed to get vm power state", "error", err, "ref", ref.String())
	}

	if !(state == types.VirtualMachinePowerStatePoweredOn) {
		logger.Debug("vm is not powered on", "ref", ref.String())
		return
	}

	if !force {
		logger.Debug("attempting graceful vm shutdown", "ref", ref.String())
		// shutdown does not return task and immediately returns
		if err = o.ShutdownGuest(ctx); err != nil {
			logger.Warn("failed to shut down vm", "error", err, "ref", ref.String())
			return
		}
		refCh <- ref
		return
	}

	// hard shutdown
	_, err = o.PowerOff(ctx)
	if err != nil {
		logger.Warn("failed to power off vm", "error", err, "ref", ref.String())
		return
	}
	refCh <- ref
}

func (c *Client) AnnotateVms(ctx context.Context, refs []types.ManagedObjectReference, data annotationData) error {
	logger := activity.GetLogger(ctx)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if len(refs) == 0 {
		return nil
	}

	// send heartbeats
	go heartbeat(ctx)

	om, err := object.GetCustomFieldsManager(c.vcclient)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("retrieve custom fields manager", errVSphere, err)
	}

	b, err := json.Marshal(data)
	if err != nil {
		return temporal.NewNonRetryableApplicationError("marshal annotation data", errInternal, err)
	}

	key, err := om.FindKey(ctx, customField)
	if err != nil {
		if !strings.Contains(err.Error(), "key name not found") {
			return temporal.NewNonRetryableApplicationError("find custom field", errVSphere, err, "key", customField)
		}

		logger.Debug("custom field not found, creating field", "key", customField)
		def, fieldErr := om.Add(ctx, customField, "VirtualMachine", nil, nil)
		if fieldErr != nil {
			return temporal.NewNonRetryableApplicationError("create custom field", errVSphere, fieldErr, "key", customField)
		}
		key = def.Key
	}

	logger.Debug("annotating preempted vms", "refs", refs)
	lim := newLimiter(concurrentVCenterCalls) // limit concurrent vc calls
	wg := sync.WaitGroup{}
	for i := range refs {
		ref := refs[i]
		lim.acquire()
		wg.Add(1)
		go func() {
			defer func() {
				lim.release()
				wg.Done()
			}()

			err := om.Set(ctx, ref, key, string(b))
			if err != nil {
				logger.Warn("set custom field", "ref", ref, "error", err)
			}
		}()
	}

	logger.Debug("waiting for operations to finish")
	wg.Wait()

	return nil
}

func (c *Client) SendPreemptedEvent(ctx context.Context, wfID, target string, data eventResponseData) error {
	var env EnvConfig
	if err := envconfig.Process("", &env); err != nil {
		return err
	}

	logger := activity.GetLogger(ctx)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// send heartbeats
	go heartbeat(ctx)

	ctx = ce.ContextWithTarget(ctx, target)

	source := fmt.Sprintf("%s/%s", env.Address, env.Namespace) // temporal URL + namespace
	event := ce.NewEvent()
	event.SetSource(source)
	event.SetID(fmt.Sprintf("%s-%s", wfID, data.Event.ID())) // format: wfID-vcEventID
	event.SetTime(c.clock.Now().UTC())
	event.SetType(eventType)
	err := event.SetData(ce.ApplicationJSON, data)
	if err != nil {
		return fmt.Errorf("set event data: %w", err)
	}

	logger.Debug("sending cloudevent response", "id", event.ID(), "target", target)
	result := c.ceclient.Send(ctx, event) // retries handled by activity options
	if !protocol.IsACK(result) {
		logger.Error("send cloudevent response", "id", event.ID(), "target", target, "error", result)
		return result
	}
	logger.Debug("successfully sent cloudevent response", "id", event.ID(), "target", target)
	return nil
}

func heartbeat(ctx context.Context) {
	hbTicker := time.NewTicker(heartBeatInterval)
	defer hbTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			activity.GetLogger(ctx).Debug("stopping heartbeat")
			return
		case <-hbTicker.C:
			activity.GetLogger(ctx).Debug("sending heartbeat", "interval", heartBeatInterval.String())
			activity.RecordHeartbeat(ctx)
		}
	}
}
