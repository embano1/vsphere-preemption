package main

import (
	"context"

	preemption "github.com/embano1/vsphere-preemption"
	"github.com/kelseyhightower/envconfig"
	sdk "go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"go.uber.org/zap"
	"knative.dev/pkg/logging"
)

var (
	buildCommit = "undefined" // build injection
	buildTag    = "undefined" // build injection
)

func main() {
	var env preemption.EnvConfig
	if err := envconfig.Process("", &env); err != nil {
		panic("unable to parse environment variables: " + err.Error())
	}

	var logger *zap.Logger
	if env.Debug {
		zapLogger, err := zap.NewDevelopment()
		if err != nil {
			panic("unable to create logger: " + err.Error())
		}
		logger = zapLogger

	} else {
		zapLogger, err := zap.NewProduction()
		if err != nil {
			panic("unable to create logger: " + err.Error())
		}
		logger = zapLogger
	}

	logger = logger.With(zap.String("commit", buildCommit), zap.String("tag", buildTag))
	c, err := sdk.NewClient(sdk.Options{
		HostPort:  env.Address,
		Namespace: env.Namespace,
		Logger:    preemption.NewZapAdapter(logger),
	})

	if err != nil {
		logger.Sugar().Fatalw("could not create Temporal client", zap.Error(err))
	}
	defer c.Close()

	w := worker.New(c, env.Queue, worker.Options{})
	w.RegisterWorkflowWithOptions(
		preemption.PreemptVMsWorkflow,
		workflow.RegisterOptions{
			Name: preemption.WorkflowName,
		},
	)

	ctx := logging.WithLogger(context.Background(), logger.Sugar())
	client, err := preemption.NewClient(ctx)
	if err != nil {
		logger.Sugar().Fatalw("could not create preemption client", zap.Error(err))
	}
	w.RegisterActivity(client)

	// pull from TaskQueue and stop on interrupt
	err = w.Run(worker.InterruptCh())
	if err != nil {
		logger.Sugar().Fatalw("could not start temporal worker", zap.Error(err))
	}
}
