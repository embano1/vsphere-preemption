package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	preemption "github.com/embano1/vsphere-preemption"
)

type statusConfig struct {
	*wfConfig
	runID string
}

func NewStatusCommand(wfConfig *wfConfig) *cobra.Command {
	cfg := &statusConfig{
		wfConfig: wfConfig,
	}

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Retrieve status information of a preemption workflow run",
		// TODO: add run-id
		Example: `# retrieve status for the active preemption workflow
preemptctl workflow status

# retrieve status for the specified preemption workflow run id
preemptctl workflow status --run-id 5d438391-281c-47d3-9e04-562c128195db`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return getStatus(cmd, cfg)
		},
	}

	flags := cmd.PersistentFlags()
	flags.StringVarP(&cfg.runID, "run-id", "r", "", "retrieve preemption status for specified workflow run id (empty for current run)")

	return cmd
}

func getStatus(cmd *cobra.Command, cfg *statusConfig) error {
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
		zap.String("queryType", preemption.WorkFlowQueryType),
	)

	logger.Debug("sending workflow status query")

	// runID is optional, defaults to current run if empty
	res, err := tc.QueryWorkflow(ctx, wfID, cfg.runID, preemption.WorkFlowQueryType)
	if err != nil {
		return fmt.Errorf("query workflow: %w", err)
	}

	var result string
	if err = res.Get(&result); err != nil {
		return fmt.Errorf("decode query result: %w", err)
	}

	logger.Info("retrieved workflow status", zap.String("status", result))
	return nil
}
