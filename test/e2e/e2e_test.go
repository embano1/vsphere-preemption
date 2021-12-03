//go:build e2e

package e2e

import (
	"context"
	"testing"

	"github.com/kelseyhightower/envconfig"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
	"gotest.tools/v3/assert"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/embano1/vsphere-preemption/test"
)

func Test_E2E(t *testing.T) {
	var cfg test.Config
	err := envconfig.Process("", &cfg)
	if err != nil {
		t.Fatalf("process environment variables: %v", err)
	}

	fields := []zap.Field{
		zap.String("namespace", cfg.Namespace),
		zap.String("temporal_address", cfg.Address),
		zap.String("temporal_namespace", cfg.TNamespace),
		zap.String("temporal_queue", cfg.Queue),
		zap.String("vcenter_address", cfg.VCAddress),
		zap.String("vcenter_insecure", cfg.Insecure),
	}
	logger := zaptest.NewLogger(t).With(fields...)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := test.GetKubeClient(t)
	assert.NilError(t, err, "create Kubernetes client")

	logger.Debug("creating test environment")
	_, err = test.SetupEnvironment(t, ctx, client, logger)
	assert.NilError(t, err, "setup test environment")

	k8senv := []corev1.EnvVar{
		{Name: "TEMPORAL_URL", Value: cfg.Address},
		{Name: "TEMPORAL_NAMESPACE", Value: cfg.TNamespace},
		{Name: "TEMPORAL_TASKQUEUE", Value: cfg.Queue},
		{Name: "VCENTER_URL", Value: cfg.VCAddress},
		{Name: "VCENTER_INSECURE", Value: cfg.Insecure},
		{Name: "DEBUG", Value: "true"},
	}

	t.Run("worker deployment succeeds", func(t *testing.T) {
		logger.Debug("creating preemption worker", zap.String("name", workerDeploymentName), zap.String("image", cfg.WorkerImage))
		_, err = createWorkerDeployment(ctx, client, workerDeploymentName, cfg.Namespace, cfg.WorkerImage, test.VSphereSecretName, k8senv, test.DefaultLabels)
		assert.NilError(t, err, "create preemption worker")

		logger.Debug("waiting for preemption worker deployment to become available")
		err = test.WaitDeploymentReady(ctx, client, cfg.Namespace, workerDeploymentName, test.PollInterval, test.PollTimeout)
		assert.NilError(t, err, "preemption worker deployment condition %q did not reach state %q", appsv1.DeploymentAvailable, corev1.ConditionTrue)
	})

	t.Run("preemptctl CLI successfully executes", func(t *testing.T) {
		logger.Debug("creating CLI job", zap.String("name", cliJobName))
		_, err = createCLIJob(ctx, client, cliJobName, cfg, test.DefaultLabels)
		assert.NilError(t, err, "create preemptctl CLI job")

		waitErr := test.WaitJobComplete(ctx, client, cfg.Namespace, cliJobName, test.PollInterval, test.PollTimeout)
		assert.NilError(t, waitErr, "preemptctl CLI job condition %q did not reach state %q", batchv1.JobComplete, corev1.ConditionTrue)
	})

	t.Run("preemption succeeds with powering off expected number of vm(s)", func(t *testing.T) {
		logger.Debug("creating GetVMs job", zap.String("name", getVMsJobName))
		_, err = createGetVMsJob(ctx, client, getVMsJobName, cfg, k8senv, test.DefaultLabels)
		assert.NilError(t, err, "create GetVMs job")

		waitErr := test.WaitJobComplete(ctx, client, cfg.Namespace, getVMsJobName, test.PollInterval, test.PollTimeout)
		assert.NilError(t, waitErr, "GetVMs job condition %q did not reach state %q", batchv1.JobComplete, corev1.ConditionTrue)
	})
}
