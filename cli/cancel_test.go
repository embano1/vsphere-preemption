package cli

import (
	"io"
	"testing"

	"gotest.tools/v3/assert"
)

func Test_NewCancelCommand(t *testing.T) {
	t.Run("validate basic metadata", func(t *testing.T) {
		cmd := NewCancelCommand(&wfConfig{})
		cmd.SetOut(io.Discard)

		// usage
		assert.Equal(t, cmd.Name(), "cancel")
		assert.Check(t, len(cmd.Short) > 0, "command should have a nonempty short description")
		assert.Check(t, len(cmd.Long) > 0, "command should have a nonempty long description")
		assert.Check(t, len(cmd.Example) > 0, "command should have a nonempty example")

		// flags
		flags := []string{"run-id"}
		checkFlag(t, cmd, flags)

		err := cmd.Execute()
		assert.Check(t, err != nil)
	})
}
