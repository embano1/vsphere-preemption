/*
Copyright © 2021 NAME HERE <EMAIL ADDRESS>

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package cli

import (
	"fmt"

	"github.com/benbjohnson/clock"
	"github.com/spf13/cobra"
)

var (
	buildCommit = "undefined" // build injection
	buildTag    = "undefined" // build injection
)

func NewRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "preempctl",
		Short: "vSphere Preemption CLI",
		Long: `
vSphere Preemption CLI interacts with vSphere preemption workflows in the Temporal engine, 
e.g. to run or cancel a preemption workflow.`,
		Version:       fmt.Sprintf("version: %s\ncommit: %s\n", buildTag, buildCommit),
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	rootCmd.PersistentFlags().Bool("verbose", false, "enable verbose logging")
	rootCmd.PersistentFlags().Bool("json", false, "JSON-encoded log output")
	rootCmd.AddCommand(NewWorkflowCommand(clock.New()))

	return rootCmd
}
