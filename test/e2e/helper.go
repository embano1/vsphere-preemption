//go:build e2e

package e2e

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/pointer"

	"github.com/embano1/vsphere-preemption/test"
)

const (
	cliJobName           = "cli"
	workerDeploymentName = "worker"
	getVMsJobName        = "getvms"
)

func createWorkerDeployment(ctx context.Context, cs *kubernetes.Clientset, name, namespace, image, secret string, env []corev1.EnvVar, labels map[string]string) (*appsv1.Deployment, error) {
	l := test.AddLabel(labels, "deployment", name)

	depl := appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    l,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: pointer.Int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: l,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: l,
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "vsphere-credentials",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: secret,
								},
							},
						},
					},
					Containers: []corev1.Container{{
						Name:  name,
						Image: image,
						Env:   env,
						VolumeMounts: []corev1.VolumeMount{{
							Name:      "vsphere-credentials",
							ReadOnly:  true,
							MountPath: "/var/bindings/vsphere",
						}},
						ImagePullPolicy: corev1.PullIfNotPresent,
					}},
					TerminationGracePeriodSeconds: pointer.Int64Ptr(5),
				},
			},
		},
	}
	return cs.AppsV1().Deployments(namespace).Create(ctx, &depl, metav1.CreateOptions{})
}

func createCLIJob(ctx context.Context, cs *kubernetes.Clientset, name string, cfg test.Config, labels map[string]string) (*batchv1.Job, error) {
	l := test.AddLabel(labels, "job", name)

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
						Image: cfg.CLIImage,
						Args: []string{
							"workflow",
							"run",
							"-c",
							"HIGH", // forced shutdown
							"-t",
							cfg.Tag,
							"-n",
							cfg.TNamespace,
							"-s",
							cfg.Address,
							"-q",
							cfg.Queue,
						},
					}},
					RestartPolicy:                 corev1.RestartPolicyOnFailure,
					TerminationGracePeriodSeconds: pointer.Int64Ptr(5),
				},
			},
		},
	}
	return cs.BatchV1().Jobs(cfg.Namespace).Create(ctx, &job, metav1.CreateOptions{})
}

func createGetVMsJob(ctx context.Context, cs *kubernetes.Clientset, name string, cfg test.Config, env []corev1.EnvVar, labels map[string]string) (*batchv1.Job, error) {
	l := test.AddLabel(labels, "job", name)

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
						Image: cfg.GetVMsImage,
						Env:   env,
					}},
					RestartPolicy:                 corev1.RestartPolicyOnFailure,
					TerminationGracePeriodSeconds: pointer.Int64Ptr(5),
				},
			},
		},
	}
	return cs.BatchV1().Jobs(cfg.Namespace).Create(ctx, &job, metav1.CreateOptions{})
}
