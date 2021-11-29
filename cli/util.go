package cli

import (
	"context"
	"encoding/json"
	"fmt"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/spf13/cobra"
	sdk "go.temporal.io/sdk/client"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	preemption "github.com/embano1/vsphere-preemption"
)

func parseJsonEvent(event string) (ce.Event, error) {
	var e ce.Event
	if err := json.Unmarshal([]byte(event), &e); err != nil {
		return ce.NewEvent(), fmt.Errorf("read specified JSON cloudevent: %w", err)
	}

	if err := e.Validate(); err != nil {
		return ce.NewEvent(), fmt.Errorf("specified cloudevent is invalid: %w", err)
	}
	return e, nil
}

func checkNotEmpty(name, value string) error {
	if value == "" {
		return fmt.Errorf("flag %q must not be empty", name)
	}

	return nil
}

func getLogger(cmd *cobra.Command) (*zap.Logger, error) {
	fields := []zap.Field{
		zap.String("commit", buildCommit),
		zap.String("tag", buildTag),
	}

	debug, err := cmd.Flags().GetBool("verbose")
	if err != nil {
		return nil, fmt.Errorf("get debug flag value: %w", err)
	}

	level := zapcore.InfoLevel
	if debug {
		level = zapcore.DebugLevel
	}

	jsonOut, err := cmd.Flags().GetBool("json")
	if err != nil {
		return nil, fmt.Errorf("get json flag value: %w", err)
	}

	var config zap.Config
	if jsonOut {
		config = zap.NewProductionConfig()
		config.Level = zap.NewAtomicLevelAt(level)
		config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	} else {
		config = zap.NewDevelopmentConfig()
		config.Level = zap.NewAtomicLevelAt(level)
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	config.OutputPaths = []string{"stdout"}
	config.ErrorOutputPaths = []string{"stderr"}
	logger, err := config.Build(zap.Fields(fields...))
	if err != nil {
		return nil, err
	}

	logger.Debug("created logger", zap.Bool("debug", debug))
	return logger, nil
}

func newTemporalClient(_ context.Context, address, namespace string, logger *zap.Logger) (sdk.Client, error) {
	tc, err := sdk.NewClient(sdk.Options{
		HostPort:  address,
		Namespace: namespace,
		Logger:    preemption.NewZapAdapter(logger),
	})

	if err != nil {
		return nil, err
	}

	return tc, nil
}
