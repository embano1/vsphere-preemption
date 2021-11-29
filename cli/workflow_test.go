package cli

import (
	"testing"

	"github.com/benbjohnson/clock"
	"github.com/spf13/cobra"
	"gotest.tools/v3/assert"
)

func Test_NewWorkflowCommand(t *testing.T) {
	t.Run("validate basic metadata", func(t *testing.T) {
		mockClock := clock.NewMock()
		cmd := NewWorkflowCommand(mockClock)
		// usage
		assert.Equal(t, cmd.Name(), "workflow")
		assert.Check(t, len(cmd.Short) > 0, "command should have a nonempty short description")

		// flags
		flags := []string{"server", "namespace", "queue"}
		checkFlag(t, cmd, flags)

		// subcommands
		subcommands := []string{"run", "status", "cancel"}
		hasSubcommand(t, cmd, subcommands)

		// invalid server specified
		cmd.SetArgs([]string{"--server"})
		err := cmd.Execute()
		assert.Check(t, err != nil)
	})
}

func checkFlag(t *testing.T, command *cobra.Command, flags []string) {
	for _, f := range flags {
		assert.Check(t, command.Flag(f) != nil, "command should have a %q flag", f)
	}
}

func hasSubcommand(t *testing.T, cmd *cobra.Command, subcommands []string) {
	for _, c := range subcommands {
		_, _, err := cmd.Find([]string{c})
		assert.NilError(t, err)
	}
}
