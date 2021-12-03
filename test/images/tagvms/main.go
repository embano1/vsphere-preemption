package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/kelseyhightower/envconfig"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25/mo"
	"go.uber.org/zap"
	"knative.dev/pkg/logging"

	"github.com/embano1/vsphere-preemption/test"
)

type config struct {
	Insecure  string `envconfig:"VCENTER_INSECURE" default:"true"`
	VCAddress string `envconfig:"VCENTER_URL" default:"https://vcsim.vsphere-preemption-e2e.svc.cluster.local" required:"true"`
	Tag       string `envconfig:"VCENTER_TAG" default:"preemptible" required:"true"`
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger, err := zap.NewDevelopment()
	if err != nil {
		panic("create logger: " + err.Error())
	}

	ctx = logging.WithLogger(ctx, logger.Sugar())
	if err = setupEnvironment(ctx); err != nil {
		logger.Fatal("setup environment", zap.Error(err))
	}
}

func setupEnvironment(ctx context.Context) error {
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

	logger.Debugw("creating tag", zap.String("tag", cfg.Tag))
	tagID, err := test.CreateTag(ctx, vcclient.Rest, cfg.Tag)
	if err != nil {
		return fmt.Errorf("create tag: %w", err)
	}

	logger.Debugw("retrieving virtual machines")
	vms, err := test.GetVMs(ctx, vcclient.Soap, "/DC0/vm/*")
	if err != nil {
		return fmt.Errorf("retrieving virtual machines: %w", err)
	}

	if len(vms) == 0 {
		return fmt.Errorf("empty list of virtual machines returned")
	}
	logger.Debugw("retrieved virtual machines", zap.Int("count", len(vms)))

	var refs = make([]mo.Reference, test.TaggedVMs)
	for i := 0; i < test.TaggedVMs; i++ {
		refs[i] = vms[i]
	}

	logger.Debugw("applying tag to virtual machine(s)", zap.Int("taggedvms", test.TaggedVMs), zap.String("tag", cfg.Tag), zap.Any("refs", refs))
	err = test.AttachTag(ctx, vcclient.Rest, tagID, refs)
	if err != nil {
		return fmt.Errorf("attach tag %q to virtual machines %v: %w", cfg.Tag, refs, err)
	}

	return nil
}
