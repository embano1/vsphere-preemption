package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	preemption "github.com/embano1/vsphere-preemption"
)

type cancelConfig struct {
	*wfConfig
	runID string
}

func NewCancelCommand(wfConfig *wfConfig) *cobra.Command {
	cfg := &cancelConfig{
		wfConfig: wfConfig,
	}

	cmd := &cobra.Command{
		Use:   "cancel",
		Short: "Cancel preemption workflow",
		Long:  `Cancel a running preemption workflow. Can be used with --runID to cancel a specific workflow run`,
		Example: `# cancel the currently running preemption workflow (if any)
preemptctl workflow cancel --server temporal01.prod.corp.local:7233

# cancel the specified preemption workflow run id
preemptctl workflow cancel --server temporal01.prod.corp.local:7233 --run-id 5d438391-281c-47d3-9e04-562c128195db
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cancelPreemption(cmd, cfg)
		},
	}

	flags := cmd.PersistentFlags()
	flags.StringVarP(&cfg.runID, "run-id", "r", "", "cancel preemption for specified workflow run id (empty for current run)")

	return cmd
}

func cancelPreemption(cmd *cobra.Command, cfg *cancelConfig) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), rpcTimeout)
	defer cancel()

	logger, err := getLogger(cmd)
	if err != nil {
		return fmt.Errorf("create logger: %w", err)
	}

	logger = logger.With(
		zap.String("address", cfg.address),
		zap.String("namespace", cfg.namespace),
		zap.String("queue", cfg.queue),
	)

	logger.Debug("creating temporal client")
	tc, err := newTemporalClient(ctx, cfg.address, cfg.namespace, logger)
	if err != nil {
		return fmt.Errorf("create temporal client: %w", err)
	}

	logger = logger.With(
		zap.String("workflow", preemption.WorkflowName),
		zap.String("workflowID", wfID),
		zap.String("runID", cfg.runID),
	)

	logger.Debug("cancelling workflow")
	err = tc.CancelWorkflow(ctx, wfID, cfg.runID)
	if err != nil {
		return fmt.Errorf("cancel workflow: %w", err)
	}

	logger.Info("successfully cancelled workflow")
	return nil
}
