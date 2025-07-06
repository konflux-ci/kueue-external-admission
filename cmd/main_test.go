package main

import (
	"flag"
	"testing"
	"time"
)

func TestControllerFlags_AddFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected ControllerFlags
	}{
		{
			name: "default values",
			args: []string{},
			expected: ControllerFlags{
				EnableLeaderElection: false,
				LeaseDuration:        15 * time.Second,
				RenewDeadline:        10 * time.Second,
				RetryPeriod:          2 * time.Second,
			},
		},
		{
			name: "custom lease duration",
			args: []string{"--leader-elect-lease-duration=30s"},
			expected: ControllerFlags{
				EnableLeaderElection: false,
				LeaseDuration:        30 * time.Second,
				RenewDeadline:        10 * time.Second,
				RetryPeriod:          2 * time.Second,
			},
		},
		{
			name: "all custom values",
			args: []string{
				"--leader-elect=true",
				"--leader-elect-lease-duration=45s",
				"--leader-elect-renew-deadline=20s",
				"--leader-elect-retry-period=5s",
			},
			expected: ControllerFlags{
				EnableLeaderElection: true,
				LeaseDuration:        45 * time.Second,
				RenewDeadline:        20 * time.Second,
				RetryPeriod:          5 * time.Second,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var flags ControllerFlags
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			flags.AddFlags(fs)

			err := fs.Parse(tt.args)
			if err != nil {
				t.Fatalf("Failed to parse flags: %v", err)
			}

			if flags.EnableLeaderElection != tt.expected.EnableLeaderElection {
				t.Errorf("EnableLeaderElection = %v, want %v", flags.EnableLeaderElection, tt.expected.EnableLeaderElection)
			}
			if flags.LeaseDuration != tt.expected.LeaseDuration {
				t.Errorf("LeaseDuration = %v, want %v", flags.LeaseDuration, tt.expected.LeaseDuration)
			}
			if flags.RenewDeadline != tt.expected.RenewDeadline {
				t.Errorf("RenewDeadline = %v, want %v", flags.RenewDeadline, tt.expected.RenewDeadline)
			}
			if flags.RetryPeriod != tt.expected.RetryPeriod {
				t.Errorf("RetryPeriod = %v, want %v", flags.RetryPeriod, tt.expected.RetryPeriod)
			}
		})
	}
}

func TestControllerFlags_InvalidDuration(t *testing.T) {
	var flags ControllerFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.AddFlags(fs)

	// Test invalid duration format
	err := fs.Parse([]string{"--leader-elect-lease-duration=invalid"})
	if err == nil {
		t.Error("Expected error for invalid duration format, got nil")
	}
}
