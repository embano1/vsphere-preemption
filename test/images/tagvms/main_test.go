package main

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/kelseyhightower/envconfig"
	"github.com/vmware/govmomi/simulator"
	_ "github.com/vmware/govmomi/vapi/simulator"
	"github.com/vmware/govmomi/vapi/tags"
	"github.com/vmware/govmomi/vim25"
	"go.uber.org/zap/zaptest"
	"gotest.tools/v3/assert"

	"github.com/embano1/vsphere-preemption/test"
)

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func Test_setupEnvironment(t *testing.T) {
	t.Run("succeeds on first run", func(t *testing.T) {
		logger := zaptest.NewLogger(t)

		model := simulator.VPX()
		defer model.Remove()
		err := model.Create()
		assert.NilError(t, err, "create simulator model")

		model.Service.Listen = &url.URL{
			User: url.UserPassword("user", "pass"),
		}

		simulator.Run(func(ctx context.Context, client *vim25.Client) error {
			logger.Debug("setting environment variables")
			t.Setenv("VCENTER_INSECURE", "true")
			t.Setenv("VCENTER_URL", client.URL().String())
			t.Setenv("VCENTER_TAG", "preemptible")

			var cfg config
			err = envconfig.Process("", &cfg)
			assert.NilError(t, err, "process environment variables")

			// run and assert
			err = setupEnvironment(ctx)
			assert.NilError(t, err)

			c, err := test.GetVSphereClient(ctx, cfg.VCAddress, "user", "pass", true)
			assert.NilError(t, err)

			tm := tags.NewManager(c.Rest)
			vms, err := tm.ListAttachedObjects(ctx, cfg.Tag)
			assert.NilError(t, err)
			assert.Assert(t, len(vms) > 0, "no objects tagged")

			return nil
		}, model)
	})

	t.Run("succeeds when retried and tag already exists", func(t *testing.T) {
		logger := zaptest.NewLogger(t)

		model := simulator.VPX()
		defer model.Remove()
		err := model.Create()
		assert.NilError(t, err, "create simulator model")

		model.Service.Listen = &url.URL{
			User: url.UserPassword("user", "pass"),
		}

		simulator.Run(func(ctx context.Context, client *vim25.Client) error {
			logger.Debug("setting environment variables")
			t.Setenv("VCENTER_INSECURE", "true")
			t.Setenv("VCENTER_URL", client.URL().String())
			t.Setenv("VCENTER_TAG", "preemptible")

			var cfg config
			err = envconfig.Process("", &cfg)
			assert.NilError(t, err, "process environment variables")

			// simulate restart by having the tag already being created
			c, err := test.GetVSphereClient(ctx, cfg.VCAddress, "user", "pass", true)
			assert.NilError(t, err)

			_, err = test.CreateTag(ctx, c.Rest, cfg.Tag)
			assert.NilError(t, err)

			// run and assert
			err = setupEnvironment(ctx)
			assert.NilError(t, err)

			tm := tags.NewManager(c.Rest)
			vms, err := tm.ListAttachedObjects(ctx, cfg.Tag)
			assert.NilError(t, err)
			assert.Assert(t, len(vms) > 0, "no objects tagged")

			return nil
		}, model)
	})
}
