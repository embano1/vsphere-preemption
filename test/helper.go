package test

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vapi/rest"
	"github.com/vmware/govmomi/vapi/tags"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/soap"
	"github.com/vmware/govmomi/vim25/types"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/pointer"
	"knative.dev/pkg/logging"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

func GetKubeClient(t *testing.T) (*kubernetes.Clientset, error) {
	t.Helper()

	// create the clientset
	clientset, err := kubernetes.NewForConfig(config.GetConfigOrDie())
	if err != nil {
		t.Fatal(err)
	}

	return clientset, nil
}

func createNamespace(ctx context.Context, cs *kubernetes.Clientset, name string, labels map[string]string) (*corev1.Namespace, error) {
	ns := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}
	return cs.CoreV1().Namespaces().Create(ctx, &ns, metav1.CreateOptions{})
}

func createVSphereSecret(ctx context.Context, cs *kubernetes.Clientset, name, namespace, username, password string, labels map[string]string) (*corev1.Secret, error) {
	l := AddLabel(labels, "secret", name)

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    l,
		},
		Data: map[string][]byte{
			corev1.BasicAuthUsernameKey: []byte(username),
			corev1.BasicAuthPasswordKey: []byte(password),
		},
		Type: corev1.SecretTypeBasicAuth,
	}
	return cs.CoreV1().Secrets(namespace).Create(ctx, &secret, metav1.CreateOptions{})
}

func createVCSimulator(ctx context.Context, cs *kubernetes.Clientset, name, namespace string, labels map[string]string) error {
	l := AddLabel(labels, "deployment", name)

	vcsim := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    l,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: pointer.Int32(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: l,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: l,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  name,
						Image: "vmware/vcsim:latest",
						Args: []string{
							"/vcsim",
							"-l",
							":8989",
							"-vm",
							strconv.Itoa(DeployedVMs / 2), // total = number resource pools * vm param
						},
						Ports: []corev1.ContainerPort{
							{
								Name:          "https",
								ContainerPort: 8989,
							},
						},
					}},
				},
			},
		},
	}

	svc := corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    l,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name: "https",
					Port: 443,
					TargetPort: intstr.IntOrString{
						IntVal: 8989,
					},
				},
			},
			Selector: l,
		},
	}

	_, err := cs.AppsV1().Deployments(namespace).Create(ctx, &vcsim, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create vcsim deployment: %v", err)
	}

	_, err = cs.CoreV1().Services(namespace).Create(ctx, &svc, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create vcsim service: %v", err)
	}

	return nil
}

func createVSphereTagJob(ctx context.Context, cs *kubernetes.Clientset, name string, cfg Config, labels map[string]string) (*batchv1.Job, error) {
	l := AddLabel(labels, "job", name)
	k8senv := []corev1.EnvVar{
		{Name: "VCENTER_URL", Value: cfg.VCAddress},
		{Name: "VCENTER_INSECURE", Value: cfg.Insecure},
		{Name: "VCENTER_TAG", Value: cfg.Tag},
	}

	job := batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: l,
		},
		Spec: batchv1.JobSpec{
			Parallelism: pointer.Int32(1),
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: l,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  name,
						Image: cfg.TagVmsImage,
						Env:   k8senv,
					}},
					RestartPolicy:                 corev1.RestartPolicyOnFailure,
					TerminationGracePeriodSeconds: pointer.Int64Ptr(5),
				},
			},
		},
	}
	return cs.BatchV1().Jobs(cfg.Namespace).Create(ctx, &job, metav1.CreateOptions{})
}

// AddLabel creates and returns a copy of the given labels and adds the provided
// label and value. If labels is nil a new map will be created.
func AddLabel(labels map[string]string, label, value string) map[string]string {
	if labels == nil {
		labels = make(map[string]string)

		labels[label] = value
		return labels
	}

	copyLabels := make(map[string]string)
	for k, v := range labels {
		copyLabels[k] = v
	}
	copyLabels[label] = value
	return copyLabels
}

func WaitDeploymentReady(ctx context.Context, client *kubernetes.Clientset, namespace, deployment string, pollInterval, pollTimeout time.Duration) error {
	return wait.PollImmediateWithContext(ctx, pollInterval, pollTimeout, func(ctx context.Context) (bool, error) {
		depl, err := client.AppsV1().Deployments(namespace).Get(ctx, deployment, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		status := depl.Status
		for i := range status.Conditions {
			c := status.Conditions[i]
			if c.Type == appsv1.DeploymentAvailable && c.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
}

func WaitJobComplete(ctx context.Context, client *kubernetes.Clientset, namespace, job string, pollInterval, pollTimeout time.Duration) error {
	return wait.PollImmediateWithContext(ctx, pollInterval, pollTimeout, func(ctx context.Context) (bool, error) {
		j, err := client.BatchV1().Jobs(namespace).Get(ctx, job, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		status := j.Status
		for i := range status.Conditions {
			c := status.Conditions[i]
			if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
}

type VSphereClient struct {
	Soap *govmomi.Client
	Rest *rest.Client
}

func GetVSphereClient(ctx context.Context, vcaddress, user, pass string, insecure bool) (*VSphereClient, error) {
	u, err := soap.ParseURL(vcaddress)
	if err != nil {
		return nil, fmt.Errorf("parse vcenter address: %w", err)
	}

	u.User = url.UserPassword(user, pass)
	c, err := govmomi.NewClient(ctx, u, insecure)
	if err != nil {
		return nil, fmt.Errorf("create vsphere client: %w", err)
	}

	rc := rest.NewClient(c.Client)
	if err = rc.Login(ctx, u.User); err != nil {
		return nil, fmt.Errorf("perform rest login: %w", err)
	}

	return &VSphereClient{
		c,
		rc,
	}, nil

}

func GetVMs(ctx context.Context, client *govmomi.Client, path string) ([]*object.VirtualMachine, error) {
	finder := find.NewFinder(client.Client)
	return finder.VirtualMachineList(ctx, path)
}

func CreateTag(ctx context.Context, client *rest.Client, tag string) (string, error) {
	logger := logging.FromContext(ctx)
	logger.Debug("creating category")

	tm := tags.NewManager(client)

	const (
		catName = "test-category"
	)
	catID, err := tm.CreateCategory(ctx, &tags.Category{
		Name:            catName,
		Description:     "test category",
		AssociableTypes: []string{"VirtualMachine"},
		Cardinality:     "SINGLE",
	})
	if err != nil {
		if f, ok := err.(types.HasFault); ok {
			switch f.Fault().(type) {
			case *types.AlreadyExists: // continue
				logger.Debugw("category already exists", "category", catName)
			default:
				return "", fmt.Errorf("create category: %w", err)
			}
		}
	}

	// if already exists, we need to retrieve ID
	if catID == "" {
		cat, err := tm.GetCategory(ctx, catName)
		if err != nil {
			return "", fmt.Errorf("get category: %w", err)
		}
		catID = cat.ID
	}

	logging.FromContext(ctx).Debug("creating tag")
	id, err := tm.CreateTag(ctx, &tags.Tag{
		Name:        tag,
		Description: "preemption test tag",
		CategoryID:  catID,
	})
	if err != nil {
		if f, ok := err.(types.HasFault); ok {
			switch f.Fault().(type) {
			case *types.AlreadyExists: // continue
				logger.Debugw("tag already exists", "category", tag)
			default:
				return "", fmt.Errorf("create tag: %w", err)
			}
		}
	}

	// if already exists, we need to retrieve ID
	if id == "" {
		t, err := tm.GetTag(ctx, tag)
		if err != nil {
			return "", fmt.Errorf("get tag: %w", err)
		}
		id = t.ID
	}

	return id, nil
}

func AttachTag(ctx context.Context, client *rest.Client, tag string, vms []mo.Reference) error {
	tm := tags.NewManager(client)
	return tm.AttachTagToMultipleObjects(ctx, tag, vms)
}
