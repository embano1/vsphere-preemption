# vSphere Preemption

[![Tests](https://github.com/embano1/vsphere-preemption/actions/workflows/unit-tests.yaml/badge.svg)](https://github.com/embano1/vsphere-preemption/actions/workflows/unit-tests.yaml)
[![E2E Tests](https://github.com/embano1/vsphere-preemption/actions/workflows/e2e-main.yaml/badge.svg)](https://github.com/embano1/vsphere-preemption/actions/workflows/e2e-main.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/embano1/vsphere-preemption)](https://goreportcard.com/report/github.com/embano1/vsphere-preemption)
[![Latest Release](https://img.shields.io/github/release/embano1/vsphere-preemption.svg?logo=github&style=flat-square)](https://github.com/embano1/vsphere-preemption/releases/latest)
[![go.mod Go version](https://img.shields.io/github/go-mod/go-version/embano1/vsphere-preemption)](https://github.com/embano1/vsphere-preemption)

Prototype to demonstrate **preemption of specific virtual machines** (VMs) in
vSphere based on a custom preemption workflow developed and executed by the
Temporal [Workflow Engine](https://temporal.io/). 

## Motivation

Virtual Machine (VM) preemption is a common pattern in cloud environments where
certain VMs can be powered off based on their criticality (quality of service)
or lifetime (also known as "spot instances"). This pattern is often used to
offer "cheap" compute or power off/terminate instances when resources are short.

As of today, VMware **vSphere does not support preemption natively**. 

This prototype, in conjunction with an external trigger, e.g. a
[function](https://github.com/vmware-samples/vcenter-event-broker-appliance/tree/development/examples/knative/go/kn-go-preemption)
deployed in [VMware Event Broker Appliance](https://vmweventbroker.io/) (VEBA),
shows how such a feature can be built on top of vSphere primitives in an
event-driven manner. 

When the aforementioned example function is used, preemption is triggered by
using vSphere `AlarmStatusChangedEvents` to start a multi-step asynchronous
(non-blocking) workflow to identify and power off preemptible VMs.

### Why not VM Reservations and Admission Control?

In case of failed (vSphere HA) or workload rebalancing (vSphere DRS), vCenter
performs admission control based on the individually configured VM resources
(reservations, shares, etc. if any) and total number of workloads running in a
cluster.

vSphere attempts to guarantee that enough (spare) cluster capacity is available
in case of failure for all **running** virtual machines with defined
reservations. If not enough capacity is available during a power on operation
for a VM with reservations, admission check would fail and the VM would not be
able to power on.

Under certain scenarios this might not lead to the desired outcome, e.g. when
losing a significant amount of cluster capacity. Or when the cluster reaches a
certain level of utilization under normal operation and VMs start contending for
resources which cannot/should not be expressed with reservations.

In such scenarios, having support for workload preemption, i.e. powering off
specific VMs to make room for critical workloads and release resources
(capacity) is desirable.

Also note that the equivalent of "spot" instances cannot be modeled with the
current vSphere primitives, i.e. shares, reservations and limits.

## The Preemption Workflow

The [workflow](workflow.go) is divided into several activities, executed by a
custom Temporal [`worker`](cmd/worker/main.go):

1) Identify `preemptible` VMs (based on vSphere tags)
1) Power off the identified VMs; soft or hard, depending on `CRITICALITY` (1)
1) Annotate the powered off VMs with detailed information using a custom attribute (2)
1) Optionally: send a custom CloudEvent (3) with detailed information, e.g. to a
  Knative `broker`

Any of these steps can fail, so it's imperative to build a robust workflow which
can handle various failure scenarios, e.g. by retrying a failed step, persisting
state across these steps to ensure deterministic behavior, enforcing timeouts to
avoid blocking/running execution runs, etc. 

See [Why a Workflow Engine?](#why-a-workflow-engine) for more details.

**Note:** The workflow implementation uses Temporal
[`signals`](https://docs.temporal.io/docs/concepts/signals/) to send workflow
requests to the `worker`. That means, once a workflow is started it will block
on more signals and **will not terminate** unless explicitly cancelled by an
external actor. For example, the further below mentioned mentioned VEBA example
`kn-go-preemption` does this when an alert severity level is dropping.

The benefit of `signals` instead of creating distinct workflows on each request
is that the overall workflow keeps running and state can be shared between
invocations easily, e.g. to avoid multiple invocations within a short time.

(1) `CRITICALITY` is determined by the alarm status in the
`AlarmStatusChangedEvent`. `"Red"` will force an immediate shutdown, whereas a
lower alarm state ("yellow") will attempt a soft shutdown.

(2) `com.vmware.workflows.vsphere.preemption`

(3) `com.vmware.workflows.vsphere.VmPreemptedEvent.v0`

## Why a Workflow Engine?

One could assume that the individual steps, as outlined above, could be combined
into one or more microservices/functions. However, this has several challenges:

- State management and resiliency (retries, timeouts, replays, atomic operations,
  etc.)
- Visibility and inspection in an asynchronous/event-driven system with multiple
  actors
- Event duplication and concurrency handling to avoid multiple/conflicting
  preemption operations
- Scalability (work queue pattern)
- Unit and end-to-end (chaos) testing

Eventually, one ends up writing a custom workflow engine which should not be
underestimated. See [Designing A Workflow Engine from First
Principles](https://docs.temporal.io/blog/workflow-engine-principles/) for more
details around the challanges and complexities involved in building a robust and
scalable workflow engine.

## Requirements

- Kubernetes environment
- Temporal Workflow Engine, e.g. via
  [Helm](https://github.com/temporalio/helm-charts)
- VMware vCenter and security account with sufficient privileges to query
  tags, power off virtual machines and create/write custom attributes
- An external actor, e.g. a function, to (periodically) trigger the workflow (1)

(1) Deploy this example from the VEBA project or see its code for details how to
trigger the workflow:
[kn-go-preemption](https://github.com/vmware-samples/vcenter-event-broker-appliance/tree/development/examples/knative/go/kn-go-preemption).

## Deployment

💡 A detailed video walkthrough is available
[here](https://www.youtube.com/watch?v=K22Jdiu1HiY) (Youtube).

### vCenter Settings

When a workflow is triggered from an external actor, e.g. by using a function
(see [requirements](#requirements)), a vSphere tag and alarm name must be
provided. 

The vSphere tag, e.g. `preemptible`, must be assigned to virtual machines which
should be powered off. The alarm is used to trigger the execution of the
workflow, e.g. using a function, and contains details on the severity
(`CRITICALITY`).

⚠️ Details on creating alarms and tags are out of scope of this project. See
this example from the VEBA project for details:
[kn-go-preemption](https://github.com/vmware-samples/vcenter-event-broker-appliance/tree/development/examples/knative/go/kn-go-preemption).

When a VM is powered off in a workflow run, a [custom
attribute](https://www.google.com/search?q=vsphere+custom+attributes&oq=vsphere+custom+&aqs=chrome.1.69i57j0i512j69i59j0i22i30j69i65j69i60l3.2778j0j7&sourceid=chrome&ie=UTF-8)
value in JSON format based on a (hardcoded) key
`com.vmware.workflows.vsphere.preemption` is updated to provide details on which
workflow/event caused the power operation.

⚠️ This custom attribute must be created upfront, otherwise the annotation step
will fail. This can be done through the vCenter UI or programmatically, e.g.
using `govc`:

```console
govc fields.add -type VirtualMachine com.vmware.workflows.vsphere.preemption
```

⚠️ The worker needs permissions to:

- search the vCenter inventory for objects (VMs) attached to the specified tag
- power off these VMs
- annotate these VMs with a custom attribute

It is recommended to create a dedicated vCenter service account/role for the
worker. This account will be used in later steps.

### Temporal Settings

**Note:** The following steps assumes a successfully deployed and running
Temporal workflow engine. The examples are based on the official Temporal [Helm
Chart](https://github.com/temporalio/helm-charts) using the deployment name
`temporaltest`.

Create a dedicated Temporal
[Namespace](https://docs.temporal.io/docs/server/namespaces/), e.g. by opening a
`$SHELL` into the Temporal Kubernetes deployment.

```console
kubectl exec -it services/temporaltest-admintools /bin/bash
```

From within the container create the namespace `vsphere-preemption`.

```console
# inside container
tctl --ns vsphere-preemption namespace register
```

### Worker

Before deploying the `worker` instance, a Kubernetes `namespace` and `secret`
must be created.

```console
kubectl create ns vmware-preemption
```

Now create a Kubernetes secret for the worker holding the vCenter account
information to perform required vCenter operations as described
[above](#vcenter-settings).

```console
kubectl -n vmware-preemption create secret generic vsphere-credentials --from-literal=username=preemption-worker@vsphere.local --from-literal=password='ReplaceMe'
```

Download the `latest` deployment manifest (`release.yaml`) file from the Github
[release](https://github.com/embano1/vsphere-preemption/releases/latest) page
and update the environment variables in `release.yaml` to match your setup. Then
save the file under the same name (to follow along with the commands).

#### Example Download with `curl`:

```console
curl -L -O https://github.com/embano1/vsphere-preemption/releases/latest/download/release.yaml
```

#### Environment Variables

| Variable              | Description                                                                                                                                | Example                                                              | Required |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------- | -------- |
| `TEMPORAL_URL`        | `IP/FQDN:<port>` of the Temporal Frontend Service*                                                                                         | `temporaltest-frontend.default.svc.cluster.local:7233`               | **yes**  |
| `TEMPORAL_NAMESPACE`  | Temporal (not Kubernetes!) [namespace](https://docs.temporal.io/docs/server/namespaces/) to use*                                           | `vsphere-preemption`                                                 | **yes**  |
| `TEMPORAL_TASKQUEUE`  | User-defined Temporal [task queue](https://docs.temporal.io/docs/concepts/task-queues) to send workflows to the worker (created on-demand) | `vsphere-preemption`                                                 | **yes**  |
| `VCENTER_URL`         | VMware vCenter Server URL                                                                                                                  | `https://my-vcenter.corp.local`                                      | **yes**  |
| `VCENTER_INSECURE`    | Ignore VMware vCenter certificate (TLS) warnings, e.g. when using self-signed certificates                                                 | `"true"` or `"false"` (note the `""` around the value are mandatory) | no       |
| `DEBUG`               | Enable debug logs                                                                                                                          | `"true"` or `"false"` (note the `""` around the value are mandatory) | no       |
| `VCENTER_SECRET_PATH` | Overwrite default mount path of secret (useful during testing)                                                                             | `/var/bindings/vsphere`                                              | no       |

\* As created in earlier steps

**Note:** In addition to the above custom settings, the `worker` is configured
to allow for up to **5** concurrent vCenter calls (rate limit) and preempt a
maximum **10** VMs in a single workflow execution. Failed activities (steps) are
retried up to **3** times with backoff logic. If another workflow run is
executed within **1 minute** after the last run, it will be skipped to avoid too
many preemption within a short window.

#### Deploy to Kubernetes

Now, deploy the `worker`:

```console
kubectl -n vmware-preemption create -f release.yaml
```

Verify that the `worker` is correctly starting:

```console
kubectl -n vmware-preemption logs deploy/vsphere-preemption-worker -f
INFO    internal/internal_worker.go:922 Started Worker  {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-55b6854755-q24vx@"}
```

When an external actor, e.g. function, triggers the `preemption` workflow by
sending a [`signal`](https://docs.temporal.io/docs/concepts/signals/), workflow
and activity details can be observed in the log, too:

```console
INFO    log/replay_logger.go:61 waiting for incoming signal     {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "Attempt": 1, "channel": "PreemptVMsChan"}
DEBUG   log/replay_logger.go:54 received signal {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "Attempt": 1, "signal": {"tag":"preemptible","criticality":"HIGH","event":{"specversion":"1.0","id":"d65a8ad1-7d3c-4f72-ace4-e3f2883c4475","source":"https://10.161.164.224/sdk","type":"com.vmware.event.router/event","subject":"AlarmStatusChangedEvent","datacontenttype":"application/json","time":"2021-11-05T13:13:49.993999Z","data":{"Key":15227,"ChainId":15227,"CreatedTime":"2021-11-05T13:13:49.993999Z","UserName":"","Datacenter":{"Name":"vcqaDC","Datacenter":{"Type":"Datacenter","Value":"datacenter-2"}},"ComputeResource":{"Name":"cls","ComputeResource":{"Type":"ClusterComputeResource","Value":"domain-c7"}},"Host":{"Name":"10.161.166.252","Host":{"Type":"HostSystem","Value":"host-21"}},"Vm":null,"Ds":null,"Net":null,"Dvs":null,"FullFormattedMessage":"Alarm 'cluster-cpu-above-80' on 10.161.166.252 changed from Green to Red","ChangeTag":"","Alarm":{"Name":"cluster-cpu-above-80","Alarm":{"Type":"Alarm","Value":"alarm-282"}},"Source":{"Name":"cls","Entity":{"Type":"ClusterComputeResource","Value":"domain-c7"}},"Entity":{"Name":"10.161.166.252","Entity":{"Type":"HostSystem","Value":"host-21"}},"From":"green","To":"red"},"vsphereapiversion":"6.7.3"},"replyTo":"http://default-broker-ingress.vmware-functions.svc.cluster.local"}}
DEBUG   log/replay_logger.go:54 searching for preemptible virtual machines      {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "Attempt": 1}
DEBUG   log/replay_logger.go:54 ExecuteActivity {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "Attempt": 1, "ActivityID": "6", "ActivityType": "GetPreemptibleVMs"}
DEBUG   vsphere-preemption/activities.go:111    searching for preemptible vms   {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "ActivityID": "6", "ActivityType": "GetPreemptibleVMs", "Attempt": 1, "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "maxPreemptVMs": 10}
DEBUG   vsphere-preemption/activities.go:118    tag to vm mapping       {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "ActivityID": "6", "ActivityType": "GetPreemptibleVMs", "Attempt": 1, "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "tag": "preemptible", "vms": [{"type":"VirtualMachine","id":"vm-58"},{"type":"VirtualMachine","id":"vm-57"}]}
DEBUG   vsphere-preemption/activities.go:314    stopping heartbeat      {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "ActivityID": "6", "ActivityType": "GetPreemptibleVMs", "Attempt": 1, "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8"}
DEBUG   log/replay_logger.go:54 preemptible virtual machines result     {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "Attempt": 1, "count": 2, "refs": [{"Type":"VirtualMachine","Value":"vm-58"},{"Type":"VirtualMachine","Value":"vm-57"}]}
DEBUG   log/replay_logger.go:54 preempting virtual machines     {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "Attempt": 1}
DEBUG   log/replay_logger.go:54 ExecuteActivity {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "Attempt": 1, "ActivityID": "20", "ActivityType": "PowerOffVMs"}
DEBUG   vsphere-preemption/activities.go:151    powering off vms        {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "ActivityID": "20", "ActivityType": "PowerOffVMs", "Attempt": 1, "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "refs": [{"Type":"VirtualMachine","Value":"vm-58"},{"Type":"VirtualMachine","Value":"vm-57"}]}
DEBUG   vsphere-preemption/activities.go:159    waiting for operations to finish        {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "ActivityID": "20", "ActivityType": "PowerOffVMs", "Attempt": 1, "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8"}
DEBUG   vsphere-preemption/activities.go:187    vm is not powered on    {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "ActivityID": "20", "ActivityType": "PowerOffVMs", "Attempt": 1, "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "ref": "VirtualMachine:vm-57"}
DEBUG   vsphere-preemption/activities.go:314    stopping heartbeat      {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "ActivityID": "20", "ActivityType": "PowerOffVMs", "Attempt": 1, "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8"}
DEBUG   log/replay_logger.go:54 preempted virtual machines result       {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "Attempt": 1, "count": 1, "refs": [{"Type":"VirtualMachine","Value":"vm-58"}]}
DEBUG   log/replay_logger.go:54 annotating preempted virtual machines   {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "Attempt": 1}
DEBUG   log/replay_logger.go:54 ExecuteActivity {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "Attempt": 1, "ActivityID": "26", "ActivityType": "AnnotateVms"}
DEBUG   vsphere-preemption/activities.go:246    annotating preempted vms        {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "ActivityID": "26", "ActivityType": "AnnotateVms", "Attempt": 1, "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "refs": [{"Type":"VirtualMachine","Value":"vm-58"}]}
DEBUG   vsphere-preemption/activities.go:265    waiting for operations to finish        {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "ActivityID": "26", "ActivityType": "AnnotateVms", "Attempt": 1, "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8"}
DEBUG   vsphere-preemption/activities.go:314    stopping heartbeat      {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "ActivityID": "26", "ActivityType": "AnnotateVms", "Attempt": 1, "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8"}
DEBUG   log/replay_logger.go:54 sending cloudevents response    {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "Attempt": 1}
DEBUG   log/replay_logger.go:54 ExecuteActivity {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "Attempt": 1, "ActivityID": "32", "ActivityType": "SendPreemptedEvent"}
DEBUG   vsphere-preemption/activities.go:297    sending cloudevent response     {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "ActivityID": "32", "ActivityType": "SendPreemptedEvent", "Attempt": 1, "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "id": "cluster-cpu-above-80-d65a8ad1-7d3c-4f72-ace4-e3f2883c4475", "target": "http://default-broker-ingress.vmware-functions.svc.cluster.local"}
DEBUG   vsphere-preemption/activities.go:303    successfully sent cloudevent response   {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "ActivityID": "32", "ActivityType": "SendPreemptedEvent", "Attempt": 1, "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "id": "cluster-cpu-above-80-d65a8ad1-7d3c-4f72-ace4-e3f2883c4475", "target": "http://default-broker-ingress.vmware-functions.svc.cluster.local"}
DEBUG   vsphere-preemption/activities.go:314    stopping heartbeat      {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "ActivityID": "32", "ActivityType": "SendPreemptedEvent", "Attempt": 1, "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8"}
INFO    log/replay_logger.go:61 waiting for incoming signal     {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "Attempt": 1, "channel": "PreemptVMsChan"}
DEBUG   log/replay_logger.go:54 received signal {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "Attempt": 1, "signal": {"tag":"preemptible","criticality":"HIGH","event":{"specversion":"1.0","id":"9e1ff305-d662-4d90-8e07-7d1f001a85ad","source":"https://10.161.164.224/sdk","type":"com.vmware.event.router/event","subject":"AlarmStatusChangedEvent","datacontenttype":"application/json","time":"2021-11-05T13:13:53.215Z","data":{"Key":15228,"ChainId":15228,"CreatedTime":"2021-11-05T13:13:53.215Z","UserName":"","Datacenter":{"Name":"vcqaDC","Datacenter":{"Type":"Datacenter","Value":"datacenter-2"}},"ComputeResource":{"Name":"cls","ComputeResource":{"Type":"ClusterComputeResource","Value":"domain-c7"}},"Host":{"Name":"10.161.167.160","Host":{"Type":"HostSystem","Value":"host-27"}},"Vm":null,"Ds":null,"Net":null,"Dvs":null,"FullFormattedMessage":"Alarm 'cluster-cpu-above-80' on 10.161.167.160 changed from Green to Red","ChangeTag":"","Alarm":{"Name":"cluster-cpu-above-80","Alarm":{"Type":"Alarm","Value":"alarm-282"}},"Source":{"Name":"cls","Entity":{"Type":"ClusterComputeResource","Value":"domain-c7"}},"Entity":{"Name":"10.161.167.160","Entity":{"Type":"HostSystem","Value":"host-27"}},"From":"green","To":"red"},"vsphereapiversion":"6.7.3"},"replyTo":"http://default-broker-ingress.vmware-functions.svc.cluster.local"}}
INFO    log/replay_logger.go:61 skipping workflow run because last run is not older than configured re-run threshold        {"Namespace": "vsphere-preemption", "TaskQueue": "vsphere-preemption", "WorkerID": "1@vsphere-preemption-worker-6769bfcdf8-sjms5@", "WorkflowType": "PreemptVMsWorkflow", "WorkflowID": "cluster-cpu-above-80", "RunID": "dc429e3a-1d4a-4884-9cb1-c2dc959bbfd8", "Attempt": 1, "threshold": "1m0s", "currentRun": "2021-11-05 13:13:57.8286876 +0000 UTC", "lastRun": "2021-11-05 13:13:57.8286876 +0000 UTC"}
```

#### Troubleshooting

If the `worker` is not starting, inspect/verify the following:

- The `worker` logs (enable `DEBUG` environment flag if not already done)
- All [dependencies](#requirements) are installed and working correctly
- Environment [variables](#worker) are set correctly (especially **required**
  variables)

## Uninstall Worker

To uninstall the `worker`, run:

```console
kubectl -n vmware-preemption delete -f release.yaml
kubectl -n vmware-preemption delete secret generic vsphere-credentials
kubectl delete namespace vmware-preemption
```

## Build Custom Image

**Note:** This step is only required if you made code changes to the Go code.

This example uses [`ko`](https://github.com/google/ko) to build and push
container artifacts.

```console
# only when using kind: 
# export KIND_CLUSTER_NAME=kind
# export KO_DOCKER_REPO=kind.local

export KO_DOCKER_REPO=my-docker-username
export KO_COMMIT=$(git rev-parse --short=8 HEAD)
export KO_TAG=$(git describe --abbrev=0 --tags)

# build, push and run the worker in the configured Kubernetes context 
# and vmware-preemption Kubernetes namespace
ko resolve -BRf config | kubectl -n vmware-preemption apply -f -
```

To delete the deployment:

```console
ko -n vmware-preemption delete -f config
```
