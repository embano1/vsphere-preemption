package cli

import (
	"io"
	"testing"

	"gotest.tools/v3/assert"
)

const (
	validCloudEvent   = `{"data":{"key":"value"},"datacontenttype":"application/json","id":"1","source":"https://unit.test","specversion":"1.0","type":"TestEvent"}`
	invalidCloudEvent = `{"id":"1","source":"kn-event/0.0.0","specversion":"1.0"}`
)

func Test_NewRunCommand(t *testing.T) {
	t.Run("validate basic metadata", func(t *testing.T) {
		cmd := NewRunCommand(&wfConfig{})
		cmd.SetOut(io.Discard)

		// usage
		assert.Equal(t, cmd.Name(), "run")
		assert.Check(t, len(cmd.Short) > 0, "command should have a nonempty short description")
		assert.Check(t, len(cmd.Long) > 0, "command should have a nonempty long description")
		assert.Check(t, len(cmd.Example) > 0, "command should have a nonempty example")

		// flags
		flags := []string{"tag", "criticality", "event", "reply-to"}
		checkFlag(t, cmd, flags)

		err := cmd.Execute()
		assert.Check(t, err != nil)
	})

	t.Run("fails if required run flags are not specified", func(t *testing.T) {
		cmd := NewRunCommand(&wfConfig{})
		cmd.SetOut(io.Discard)

		// empty tag
		cmd.SetArgs([]string{"--tag", ""})
		err := cmd.Execute()
		assert.ErrorContains(t, err, "\"tag\" must not be")

		// criticality not set
		cmd.SetArgs([]string{"--tag", "test-tag", "--criticality", ""})
		err = cmd.Execute()
		assert.ErrorContains(t, err, "\"criticality\" must not be")

		// 	invalid criticality
		cmd.SetArgs([]string{"--criticality", "notvalid"})
		err = cmd.Execute()
		assert.ErrorContains(t, err, "criticality \"notvalid\" invalid")
	})

	t.Run("fails if specified event is invalid", func(t *testing.T) {
		cmd := NewRunCommand(&wfConfig{})
		cmd.SetOut(io.Discard)

		// empty tag
		cmd.SetArgs([]string{"--tag", "test-tag", "--criticality", "HIGH", "--event", invalidCloudEvent})
		err := cmd.Execute()
		assert.ErrorContains(t, err, "specified cloudevent is invalid")
	})
}
