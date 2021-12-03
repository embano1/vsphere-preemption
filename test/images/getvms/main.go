package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/kelseyhightower/envconfig"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	"go.uber.org/zap"
	"knative.dev/pkg/logging"

	"github.com/embano1/vsphere-preemption/test"
)

type config struct {
	Insecure  string `envconfig:"VCENTER_INSECURE" default:"true"`
	VCAddress string `envconfig:"VCENTER_URL" default:"https://vcsim.vsphere-preemption-e2e.svc.cluster.local" required:"true"`
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger, err := zap.NewDevelopment()
	if err != nil {
		panic("create logger: " + err.Error())
	}

	ctx = logging.WithLogger(ctx, logger.Sugar())
	if err = getVMs(ctx); err != nil {
		logger.Fatal("setup environment", zap.Error(err))
	}
}

func getVMs(ctx context.Context) error {
	var cfg config
	err := envconfig.Process("", &cfg)
	if err != nil {
		return fmt.Errorf("process environment variables: %w", err)
	}

	username := simulator.DefaultLogin.Username()
	password, _ := simulator.DefaultLogin.Password()
	insecure, err := strconv.ParseBool(cfg.Insecure)
	if err != nil {
		return fmt.Errorf("parse flag %q: %v", cfg.Insecure, err)
	}

	logger := logging.FromContext(ctx)
	logger.Debugw("connecting to vcenter simulator", zap.String("address", cfg.VCAddress))
	vcclient, err := test.GetVSphereClient(ctx, cfg.VCAddress, username, password, insecure)
	if err != nil {
		return fmt.Errorf("connecting to vcenter simulator: %w", err)
	}

	vms, err := test.GetVMs(ctx, vcclient.Soap, "/DC0/vm/*")
	if err != nil {
		return fmt.Errorf("get virtual machines: %w", err)
	}

	var (
		poweredOnVms  []*object.VirtualMachine
		poweredOffVms []*object.VirtualMachine
	)

	for _, vm := range vms {
		state, err := vm.PowerState(ctx)
		if err != nil {
			return fmt.Errorf("get virtual machine power state: %w", err)
		}

		switch state {
		case vimtypes.VirtualMachinePowerStatePoweredOn:
			poweredOnVms = append(poweredOnVms, vm)
		case vimtypes.VirtualMachinePowerStatePoweredOff:
			poweredOffVms = append(poweredOffVms, vm)
		}

	}
	logger.Debugw("number of powered on vms", zap.Int("count", len(poweredOnVms)))
	logger.Debugw("number of powered off vms", zap.Int("count", len(poweredOffVms)))

	const (
		expectOff = test.TaggedVMs
		expectOn  = test.DeployedVMs - test.TaggedVMs
	)

	if len(poweredOffVms) != expectOff {
		return fmt.Errorf("expected powered off vms %d, got %d", expectOff, len(poweredOffVms))
	}

	if len(poweredOnVms) != expectOn {
		return fmt.Errorf("expected powered on vms %d, got %d", expectOn, len(poweredOnVms))
	}

	logger.Debugw(
		"expected number of powered on/off vms matches desired state",
		zap.Int("poweredOn", len(poweredOnVms)),
		zap.Int("expectedOn", expectOn),
		zap.Int("poweredOff", len(poweredOffVms)),
		zap.Int("expectedOff", expectOff),
	)

	return nil
}
