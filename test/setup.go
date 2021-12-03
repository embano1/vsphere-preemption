package test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/vmware/govmomi/simulator"
	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"knative.dev/pkg/logging"
)

const (
	VSphereSecretName   = "preemption-worker-secret"
	VCSimDeploymentName = "vcsim"
	VCClientJobName     = "tagvms"
	PollInterval        = time.Second * 3
	PollTimeout         = time.Second * 30
	DeployedVMs         = 8
	TaggedVMs           = 2 // vms with preemption tag
)

var (
	DefaultLabels = map[string]string{
		"app":  "vsphere-preemption",
		"test": "e2e",
	}
)

type Config struct {
	KubeConfig  string `envconfig:"KUBECONFIG"`
	Namespace   string `default:"vsphere-preemption-e2e" required:"true"`
	WorkerImage string `envconfig:"WORKER_IMAGE" required:"true"` // preemption worker
	CLIImage    string `envconfig:"CLI_IMAGE" required:"true"`    // preemptctl job
	TagVmsImage string `envconfig:"TAG_VM_IMAGE" required:"true"` // job configures vcsim
	GetVMsImage string `envconfig:"GET_VM_IMAGE" required:"true"` // job reads from vcsim

	// Temporal settings (helm minimal install defaults)
	Address    string `envconfig:"TEMPORAL_URL" default:"temporaltest-frontend.default.svc.cluster.local:7233" required:"true"`
	TNamespace string `envconfig:"TEMPORAL_NAMESPACE" default:"vsphere-preemption" required:"true"`
	Queue      string `envconfig:"TEMPORAL_TASKQUEUE" default:"vsphere-preemption" required:"true"`

	// vSphere settings (vcsim defaults)
	Insecure  string `envconfig:"VCENTER_INSECURE" default:"true"`
	VCAddress string `envconfig:"VCENTER_URL" default:"https://vcsim.vsphere-preemption-e2e.svc.cluster.local" required:"true"`
	Tag       string `envconfig:"VCENTER_TAG" default:"preemptible" required:"true"`
}

type CleanupFunc func(ctx context.Context) error

func SetupEnvironment(t *testing.T, ctx context.Context, client *kubernetes.Clientset, logger *zap.Logger) (CleanupFunc, error) {
	t.Helper()

	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		t.Fatal(err)
	}

	cleanup := func(ctx context.Context) error {
		logger.Warn("deleting namespace")
		delPolicy := metav1.DeletePropagationForeground
		return client.CoreV1().Namespaces().Delete(ctx, cfg.Namespace, metav1.DeleteOptions{
			PropagationPolicy: &delPolicy,
		})
	}

	logger.Debug("creating namespace")
	ctx = logging.WithLogger(ctx, logger.Sugar())
	_, err := createNamespace(ctx, client, cfg.Namespace, DefaultLabels)
	if err != nil {
		if errors.IsAlreadyExists(err) {
			logger.Warn("namespace already exists") // continue on exists
		} else {
			t.Fatalf("create Kubernetes namespace: %v", err)
		}
	}
	logger.Debug("creating vcenter simulator deployment and service")
	if err = createVCSimulator(ctx, client, VCSimDeploymentName, cfg.Namespace, DefaultLabels); err != nil {
		t.Fatalf("create vcenter simulator deployment and service: %v", err)
	}

	if err = WaitDeploymentReady(ctx, client, cfg.Namespace, VCSimDeploymentName, PollInterval, PollTimeout); err != nil {
		t.Fatalf("vcenter simulator deployment condition %q did not reach state %q: %v", appsv1.DeploymentAvailable, corev1.ConditionTrue, err)
	}

	logger.Debug("creating vcenter simulator client job to configure environment")
	if _, err = createVSphereTagJob(ctx, client, VCClientJobName, cfg, DefaultLabels); err != nil {
		t.Fatalf("create and execute vcenter client job: %v", err)
	}

	if err = WaitJobComplete(ctx, client, cfg.Namespace, VCClientJobName, PollInterval, PollTimeout); err != nil {
		t.Fatalf("vcenter client job condition %q did not reach state %q: %v", batchv1.JobComplete, corev1.ConditionTrue, err)
	}

	username := simulator.DefaultLogin.Username()
	password, _ := simulator.DefaultLogin.Password()

	logger.Debug("creating vsphere credentials secret", zap.String("name", VSphereSecretName))
	_, err = createVSphereSecret(ctx, client, VSphereSecretName, cfg.Namespace, username, password, DefaultLabels)
	if err != nil {
		return cleanup, fmt.Errorf("create secret: %v", err)
	}

	return cleanup, nil
}
