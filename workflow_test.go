package preemption

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/benbjohnson/clock"
	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/cloudevents/sdk-go/v2/client/test"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vapi/rest"
	_ "github.com/vmware/govmomi/vapi/simulator"
	"github.com/vmware/govmomi/vapi/tags"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/mo"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

const (
	getTagPath = "rest/com/vmware/cis/tagging/tag" // VAPI ListTags to resolve tag name to URN
	getTag     = "GetTagOp"
)

var any = mock.Anything

type UnitTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
}

func (s *UnitTestSuite) SetupTest() {
}

func (s *UnitTestSuite) AfterTest(suiteName, testName string) {
}

func TestUnitTestSuite(t *testing.T) {
	suite.Run(t, new(UnitTestSuite))
}

func (s *UnitTestSuite) Test_Workflow() {
	s.T().Run("cancel workflow stops workflow execution without error", func(t *testing.T) {
		env := s.NewTestWorkflowEnvironment()
		start := env.Now()
		env.RegisterDelayedCallback(func() {
			env.CancelWorkflow()
		}, time.Minute*10)

		env.ExecuteWorkflow(PreemptVMsWorkflow)

		elapsed := env.Now().Sub(start)
		s.Equal(time.Minute*10, elapsed)
		s.True(env.IsWorkflowCompleted())
		s.NoError(env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	s.T().Run("sets forced shutdown due to HIGH criticality w/out sending event", func(t *testing.T) {
		env := s.NewTestWorkflowEnvironment()
		env.RegisterDelayedCallback(func() {
			e := ce.NewEvent()
			e.SetID("1")
			e.SetType("AlarmStatusChangedEvent")
			e.SetSource("https://vcenter.test/sdk")

			req := WorkflowRequest{
				Tag:         "test-preemption",
				Criticality: CriticalityHigh,
				Event:       e,
				ReplyTo:     "",
			}
			env.SignalWorkflow(SignalChannel, req)
		}, time.Minute)

		env.RegisterDelayedCallback(func() {
			env.CancelWorkflow()
		}, time.Minute*10)

		var c *Client
		env.RegisterActivity(c)

		// mock no VMs found (note: will still call power off)
		env.OnActivity("GetPreemptibleVMs", any, any).Return(nil, nil).Once()

		// assert forced is true
		env.OnActivity("PowerOffVMs", any, any, true).Return(nil, nil).Once()

		// assert no event is sent
		env.OnActivity("SendPreemptedEvent", any, any, any, any).Never()

		env.ExecuteWorkflow(PreemptVMsWorkflow)

		s.True(env.IsWorkflowCompleted())
		s.NoError(env.GetWorkflowError())

		env.AssertExpectations(t)
	})

	s.T().Run("fails to power off VMs with retries and immediately returns from this workflow run", func(t *testing.T) {
		env := s.NewTestWorkflowEnvironment()
		env.RegisterDelayedCallback(func() {
			e := ce.NewEvent()
			e.SetID("1")
			e.SetType("AlarmStatusChangedEvent")
			e.SetSource("https://vcenter.test/sdk")

			req := WorkflowRequest{
				Tag:         "test-preemption",
				Criticality: CriticalityHigh,
				Event:       e,
				ReplyTo:     "",
			}
			env.SignalWorkflow(SignalChannel, req)
		}, time.Minute)

		env.RegisterDelayedCallback(func() {
			env.CancelWorkflow()
		}, time.Minute*10)

		var c *Client
		env.RegisterActivity(c)

		// mock no VMs found (note: will still call power off)
		env.OnActivity("GetPreemptibleVMs", any, any).Return(nil, nil).Once()
		env.OnActivity("PowerOffVMs", any, any, any).Return(nil, errors.New("failed to power off")).Times(3)

		env.OnActivity("AnnotateVms", any, any, any).Never()
		env.OnActivity("SendPreemptedEvent", any, any, any, any).Never()

		env.ExecuteWorkflow(PreemptVMsWorkflow)

		s.True(env.IsWorkflowCompleted())
		s.NoError(env.GetWorkflowError())

		env.AssertExpectations(t)
	})

	s.T().Run("fails to annotate VMs due to missing custom field, no retry and continues this workflow run", func(t *testing.T) {
		env := s.NewTestWorkflowEnvironment()
		env.RegisterDelayedCallback(func() {
			e := ce.NewEvent()
			e.SetID("1")
			e.SetType("AlarmStatusChangedEvent")
			e.SetSource("https://vcenter.test/sdk")

			req := WorkflowRequest{
				Tag:         "test-preemption",
				Criticality: CriticalityHigh,
				Event:       e,
				ReplyTo:     "https://test-broker.local",
			}
			env.SignalWorkflow(SignalChannel, req)
		}, time.Minute)

		env.RegisterDelayedCallback(func() {
			env.CancelWorkflow()
		}, time.Minute*10)

		var c *Client
		env.RegisterActivity(c)

		env.OnActivity("GetPreemptibleVMs", any, any).Return(nil, nil).Once()
		env.OnActivity("PowerOffVMs", any, any, any).Return(nil, nil).Once()

		// should not retry
		env.OnActivity("AnnotateVms", any, any, any).Return(temporal.NewNonRetryableApplicationError("annotation failed", errVSphere, errors.New("custom field not found"))).Once()

		// assert event is sent
		env.OnActivity("SendPreemptedEvent", any, any, any, any).Return(nil).Once()

		env.ExecuteWorkflow(PreemptVMsWorkflow)

		s.True(env.IsWorkflowCompleted())
		s.NoError(env.GetWorkflowError())

		env.AssertExpectations(t)
	})

	s.T().Run("sends event after preemption", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		logger := zaptest.NewLogger(s.T())

		const broker = "https://test-broker.local"

		env := s.NewTestWorkflowEnvironment()
		env.RegisterDelayedCallback(func() {
			e := ce.NewEvent()
			e.SetID("1")
			e.SetType("AlarmStatusChangedEvent")
			e.SetSource("https://vcenter.test/sdk")

			req := WorkflowRequest{
				Tag:         "test-preemption",
				Criticality: CriticalityHigh,
				Event:       e,
				ReplyTo:     broker,
			}
			env.SignalWorkflow(SignalChannel, req)
		}, time.Minute)

		env.RegisterDelayedCallback(func() {
			env.CancelWorkflow()
		}, time.Minute*10)

		ceMock, recvCh := test.NewMockSenderClient(t, 1)
		c := Client{
			ceclient: ceMock,
			clock:    clock.NewMock(),
		}
		env.RegisterActivity(&c)

		// mock everything
		env.OnActivity("GetPreemptibleVMs", any, any).Return(nil, nil).Once()
		env.OnActivity("PowerOffVMs", any, any, any).Return(nil, nil).Once()
		env.OnActivity("AnnotateVms", any, any, any).Return(nil).Once()

		var (
			expected int32 = 1
			received int32
			wg       sync.WaitGroup
		)

		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				s.FailNow("context cancelled before receiving event")
			case e := <-recvCh:
				logger.Sugar().Debugw("received event", zap.String("event", e.String()))
				atomic.AddInt32(&received, 1)
				return
			}
		}()

		err := setEnvVars()
		s.NoError(err, "set environment variables")

		env.ExecuteWorkflow(PreemptVMsWorkflow)

		s.True(env.IsWorkflowCompleted())
		s.NoError(env.GetWorkflowError())

		wg.Wait()
		s.Equal(expected, atomic.LoadInt32(&received), "receiving cloud event")

		env.AssertExpectations(t)
	})

	s.T().Run("e2e: retry GetPreemptibleVMs activity due to non-existing tag", func(t *testing.T) {
		simulator.Run(func(ctx context.Context, client *vim25.Client) error {
			rc := rest.NewClient(client)
			rcRT := rc.Client.Client.Transport
			logger := zaptest.NewLogger(t)

			fakeRT := fakeRoundTripper{
				rt:       rcRT,
				Logger:   logger,
				callsMap: make(map[string]int),
			}
			rc.Client.Client.Transport = &fakeRT

			c := Client{
				vcclient:   client,
				clock:      clock.NewMock(),
				tagManager: tags.NewManager(rc),
			}

			env := s.NewTestWorkflowEnvironment()
			env.RegisterDelayedCallback(func() {
				e := ce.NewEvent()
				e.SetID("1")
				e.SetType("AlarmStatusChangedEvent")
				e.SetSource("https://vcenter.test/sdk")

				req := WorkflowRequest{
					Tag:         "does-not-exist",
					Criticality: CriticalityHigh,
					Event:       e,
					ReplyTo:     "",
				}
				env.SignalWorkflow(SignalChannel, req)
			}, time.Minute)

			env.RegisterDelayedCallback(func() {
				env.CancelWorkflow()
			}, time.Minute*10)

			env.RegisterActivity(&c)

			// assert never called
			env.OnActivity("PowerOffVMs", any, any, any).Return(nil, nil).Never()

			// assert never called
			env.OnActivity("AnnotateVms", any, any, any).Return(nil).Never()

			// assert never called
			env.OnActivity("SendPreemptedEvent", any, any, any, any).Return(nil).Never()

			env.ExecuteWorkflow(PreemptVMsWorkflow)

			s.True(env.IsWorkflowCompleted())
			s.NoError(env.GetWorkflowError())

			// expect three retried VAPI ListTag calls from GetPreemptibleVMs activity
			s.Equal(3, fakeRT.callsMap[getTag])

			env.AssertExpectations(t)

			return nil
		})
	})

	s.T().Run("e2e: power off two preemptible VM", func(t *testing.T) {
		simulator.Run(func(ctx context.Context, client *vim25.Client) error {
			rc := rest.NewClient(client)
			err := rc.Login(ctx, simulator.DefaultLogin)
			s.NoError(err)

			c := Client{
				vcclient:   client,
				clock:      clock.NewMock(),
				tagManager: tags.NewManager(rc),
			}

			cID, err := c.tagManager.CreateCategory(ctx, &tags.Category{
				Name:            "test-category",
				Description:     "test category",
				AssociableTypes: []string{"VirtualMachine"},
				Cardinality:     "SINGLE",
			})
			s.NoError(err)

			const tagName = "preemptible"
			_, err = c.tagManager.CreateTag(ctx, &tags.Tag{
				Name:        tagName,
				Description: "test tag",
				CategoryID:  cID,
			})
			s.NoError(err)

			vms, err := getAllVms(ctx, client)
			s.NoError(err)

			const wantVms = 4
			if len(vms) != wantVms {
				s.FailNowf("received unexpected number of virtual machines", "want=%d got=%d", wantVms, len(vms))
			}

			for _, vm := range vms {
				state, err := vm.PowerState(ctx)
				s.NoError(err)
				if state != vimtypes.VirtualMachinePowerStatePoweredOn {
					s.FailNowf("expected vm to be powered on", "vm: %q", vm.Reference().String())
				}
			}

			tagVms := []mo.Reference{vms[0], vms[1]}
			err = c.tagManager.AttachTagToMultipleObjects(ctx, tagName, tagVms)
			s.NoError(err)

			env := s.NewTestWorkflowEnvironment()
			env.RegisterDelayedCallback(func() {
				e := ce.NewEvent()
				e.SetID("1")
				e.SetType("AlarmStatusChangedEvent")
				e.SetSource("https://vcenter.test/sdk")

				req := WorkflowRequest{
					Tag:         tagName,
					Criticality: CriticalityHigh,
					Event:       e,
					ReplyTo:     "",
				}
				env.SignalWorkflow(SignalChannel, req)
			}, time.Minute)

			env.RegisterDelayedCallback(func() {
				env.CancelWorkflow()
			}, time.Minute*10)

			env.RegisterActivity(&c)
			env.ExecuteWorkflow(PreemptVMsWorkflow)

			s.True(env.IsWorkflowCompleted())
			s.NoError(env.GetWorkflowError())

			// retrieve updated vm states
			vms, err = getAllVms(ctx, client)
			s.NoError(err)

			// first two vms should be preempted and annotated
			for _, vm := range vms[:2] {
				state, err := vm.PowerState(ctx)
				s.NoError(err)
				s.Equal(vimtypes.VirtualMachinePowerStatePoweredOff, state)
			}

			// other vms should not be preempted
			for _, vm := range vms[2:] {
				state, err := vm.PowerState(ctx)
				s.NoError(err)
				s.Equal(vimtypes.VirtualMachinePowerStatePoweredOn, state)
			}

			// first two vms should have annotation
			var objs []mo.ManagedEntity
			refs := []vimtypes.ManagedObjectReference{vms[0].Reference(), vms[1].Reference()}
			err = property.DefaultCollector(client).Retrieve(ctx, refs, []string{"customValue"}, &objs)
			s.NoError(err)

			fm, err := object.GetCustomFieldsManager(client)
			s.NoError(err)

			keyID, err := fm.FindKey(ctx, customField)
			s.NoError(err)

			var found bool
			for _, obj := range objs {
				for _, cv := range obj.CustomValue {
					val := cv.(*vimtypes.CustomFieldStringValue)
					if val.Key != keyID {
						continue
					}

					found = true
					s.NotEqual("", val.Value)

				}
			}
			s.Equal(true, found, "custom field %q value not found", customField)

			env.AssertExpectations(t)

			return nil
		})
	})
}

type fakeRoundTripper struct {
	rt http.RoundTripper
	*zap.Logger

	sync.RWMutex
	callsMap map[string]int
}

func (f *fakeRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	f.Sugar().Debugf("request: %v", r)

	if strings.Contains(r.URL.Path, getTagPath) && r.Method == http.MethodGet {
		f.Lock()
		defer f.Unlock()

		f.callsMap[getTag]++
	}

	return f.rt.RoundTrip(r)
}

func getAllVms(ctx context.Context, client *vim25.Client) ([]*object.VirtualMachine, error) {
	f := find.NewFinder(client)
	vms, err := f.VirtualMachineList(ctx, "/...")
	if err != nil {
		return nil, fmt.Errorf("retrieve vms: %w", err)
	}
	return vms, nil
}

func setEnvVars() error {
	if err := os.Setenv("TEMPORAL_URL", "https://temporal.test.local:1234"); err != nil {
		return fmt.Errorf("set TEMPORAL_URL environment variable: %w", err)
	}

	if err := os.Setenv("TEMPORAL_NAMESPACE", "temporal-test"); err != nil {
		return fmt.Errorf("set TEMPORAL_NAMESPACE environment variable: %w", err)
	}

	if err := os.Setenv("TEMPORAL_TASKQUEUE", "temporal-test"); err != nil {
		return fmt.Errorf("set TEMPORAL_TASKQUEUE environment variable: %w", err)
	}

	if err := os.Setenv("VCENTER_URL", "https://vcenter.test.local"); err != nil {
		return fmt.Errorf("set VCENTER_URL environment variable: %w", err)
	}

	return nil
}
