# Preemption CLI

`preemptctl` allows for manual execution of the preemption worfklow. The program
can also be used in scripts and for periodic (time-based) execution
("CronJobs").

## Requirements

The Temporal workflow engine and preemption `worker` must be installed as per the
steps outlined in the [documention](./../../README.md).

Binaries of the CLI can be downloaded in the
[releases](https://github.com/embano1/vsphere-preemption/releases).

## Usage

General information about the command structure. 

To ease parsing and automation, JSON logging output can be configured with the
global `--json` flag (sent to standard output). Detailed logging can be
configured with the global `--verbose` flag.

```console
vSphere Preemption CLI interacts with vSphere preemption workflows in the Temporal engine,
e.g. to run or cancel a preemption workflow.

Usage:
  preempctl [command]

Available Commands:
  completion  generate the autocompletion script for the specified shell
  help        Help about any command
  workflow    Perform operations on a preemption workflow

Flags:
  -h, --help      help for preempctl
      --json      JSON-encoded log output
      --verbose   enable verbose logging
  -v, --version   version for preempctl

Use "preempctl [command] --help" for more information about a command.
```

### Running Preemption

To start (trigger) a preemption workflow certain global flags, e.g. `--server`
and Temporal `--namespace` must be set. In addition, `run` accepts several
flags, e.g. to specify the vSphere `tag` used to identify (search) preemptible
virtual machines, the `criticality` to influence the shutdown behavior (`HIGH`
forces immediate shutdown) and flags to customize the `event` sent when invoking
the workflow.

The workflow can also be instructed to emit an event after each successful run
via the `--reply-to` flag. The receiver must be a valid HTTP(S) CloudEvents
endpoint, e.g. a `broker` in Knative.


```console
Send a signal to a preemption worker to trigger a preemption workflow. 
If the workflow is not running, it will be started.

Usage:
  preempctl workflow run [flags]

Examples:
# trigger preemption on a custom Temporal server with run default workflow values
preemptctl workflow run --server temporal01.prod.corp.local:7233

# trigger preemption with a custom cloudevent provided to the workflow and request a reply to a specified broker
preemptctl workflow run --server temporal01.prod.corp.local:7233 --event \
'{"data":{"threshold":70,"current":87},"datacontenttype":"application/json","id":"757098cc-b275-41b6-ab52-f2966f9d714c","source":"preemptctl","specversion":"1.0","time":"2021-11-24T20:26:00.98041Z","type":"ThresholdExceededEvent"}' \
--reply-to https://broker.corp.local


Flags:
  -c, --criticality string   criticality of the workflow request (LOW, MEDIUM, HIGH) (default "LOW")
  -e, --event string         custom CloudEvent JSON string provided in workflow request (optional)
  -h, --help                 help for run
      --reply-to string      send preemption event to this address after workflow completion (optional)
  -t, --tag string           vSphere tag to use to identify preemptible virtual machines (default "preemptible")

Global Flags:
      --json               JSON-encoded log output
  -n, --namespace string   Temporal namespace to use (default "vsphere-preemption")
  -q, --queue string       Temporal task queue where workflow requests are sent to (default "vsphere-preemption")
  -s, --server string      Temporal frontend server and port (default "localhost:7233")
      --verbose            enable verbose logging

```

### Retrieve Preemption Workflow Status

To retrieve the status and results of the currently running, last or a specific
workflow, use the `preemptctl workflow status` command.

```console
Retrieve status information of a preemption workflow run

Usage:
  preempctl workflow status [flags]

Examples:
# retrieve status for the active preemption workflow
preemptctl workflow status

# retrieve status for the specified preemption workflow run id
preemptctl workflow status --run-id 5d438391-281c-47d3-9e04-562c128195db

Flags:
  -h, --help            help for status
  -r, --run-id string   retrieve preemption status for specified workflow run id (empty for current run)

Global Flags:
      --json               JSON-encoded log output
  -n, --namespace string   Temporal namespace to use (default "vsphere-preemption")
  -q, --queue string       Temporal task queue where workflow requests are sent to (default "vsphere-preemption")
  -s, --server string      Temporal frontend server and port (default "localhost:7233")
      --verbose            enable verbose logging
```

### Cancel a Preemption Workflow

To cancel (terminate) a running preemption workflow use the `preemptctl workflow
cancel` command.

```console
Cancel a running preemption workflow. Can be used with --runID to cancel a specific workflow run

Usage:
  preempctl workflow cancel [flags]

Examples:
# cancel the currently running preemption workflow (if any)
preemptctl workflow cancel --server temporal01.prod.corp.local:7233

# cancel the specified preemption workflow run id
preemptctl workflow cancel --server temporal01.prod.corp.local:7233 --run-id 5d438391-281c-47d3-9e04-562c128195db


Flags:
  -h, --help            help for cancel
  -r, --run-id string   cancel preemption for specified workflow run id (empty for current run)

Global Flags:
      --json               JSON-encoded log output
  -n, --namespace string   Temporal namespace to use (default "vsphere-preemption")
  -q, --queue string       Temporal task queue where workflow requests are sent to (default "vsphere-preemption")
  -s, --server string      Temporal frontend server and port (default "localhost:7233")
      --verbose            enable verbose logging
```