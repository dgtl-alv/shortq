package main

import "testing"

func TestCommandFromArgs(t *testing.T) {
	tests := []struct {
		args    []string
		command string
		wantErr bool
	}{
		{command: "server"},
		{args: []string{"analytics-worker"}, command: "analytics-worker"},
		{args: []string{"unknown"}, wantErr: true},
		{args: []string{"analytics-worker", "extra"}, wantErr: true},
	}
	for _, test := range tests {
		command, err := commandFromArgs(test.args)
		if (err != nil) != test.wantErr || command != test.command {
			t.Fatalf("commandFromArgs(%q) = %q, %v", test.args, command, err)
		}
	}
}
