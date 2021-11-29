package cli

import (
	"time"

	"github.com/benbjohnson/clock"
	"github.com/spf13/cobra"
)

const (
	rpcTimeout = time.Second * 5
)

// workflow config
type wfConfig struct {
	address   string
	namespace string
	queue     string
	clock     clock.Clock
}

func NewWorkflowCommand(clock clock.Clock) *cobra.Command {
	cfg := &wfConfig{
		clock: clock,
	}

	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Perform operations on a preemption workflow",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return validateWorkflowFlags(cfg)
		},
	}

	flags := cmd.PersistentFlags()
	flags.StringVarP(&cfg.address, "server", "s", "localhost:7233", "Temporal frontend server and port")
	flags.StringVarP(&cfg.namespace, "namespace", "n", "vsphere-preemption", "Temporal namespace to use")
	flags.StringVarP(&cfg.queue, "queue", "q", "vsphere-preemption", "Temporal task queue where workflow requests are sent to")

	cmd.AddCommand(NewRunCommand(cfg))
	cmd.AddCommand(NewStatusCommand(cfg))
	cmd.AddCommand(NewCancelCommand(cfg))

	return cmd
}

func validateWorkflowFlags(cfg *wfConfig) error {
	if err := checkNotEmpty("server", cfg.address); err != nil {
		return err
	}

	if err := checkNotEmpty("namespace", cfg.namespace); err != nil {
		return err
	}

	if err := checkNotEmpty("queue", cfg.queue); err != nil {
		return err
	}

	return nil
}
