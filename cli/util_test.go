package cli

import (
	"testing"

	ce "github.com/cloudevents/sdk-go/v2"
	"gotest.tools/assert"
)

func Test_checkNotEmpty(t *testing.T) {
	type args struct {
		name  string
		value string
	}
	tests := []struct {
		name    string
		args    args
		wantErr string
	}{
		{name: "server is empty", args: args{name: "server", value: ""}, wantErr: "\"server\" must not be empty"},
		{name: "tag is not empty", args: args{name: "tag", value: "preemptible"}, wantErr: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkNotEmpty(tt.args.name, tt.args.value)
			if err != nil {
				assert.ErrorContains(t, err, tt.wantErr)
			}

			if tt.wantErr == "" {
				assert.NilError(t, err)
			}
		})
	}
}

func Test_parseJsonEvent(t *testing.T) {
	valid := ce.NewEvent()
	valid.SetID("1")
	valid.SetSource("https://unit.test")
	valid.SetType("TestEvent")
	err := valid.SetData(ce.ApplicationJSON, map[string]string{"key": "value"})
	assert.NilError(t, err, "set cloud event data")

	type args struct {
		event string
	}
	tests := []struct {
		name    string
		args    args
		want    ce.Event
		wantErr string
	}{
		{name: "invalid JSON", args: args{event: `{invalid}`}, want: ce.NewEvent(), wantErr: "invalid"},
		{name: "invalid cloudevent", args: args{event: invalidCloudEvent}, want: ce.NewEvent(), wantErr: "invalid"},
		{name: "valid cloudevent", args: args{event: validCloudEvent}, want: valid, wantErr: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseJsonEvent(tt.args.event)
			assert.DeepEqual(t, got, tt.want)

			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
			}

			if tt.wantErr == "" {
				assert.NilError(t, err)
			}
		})
	}
}
