package main

import (
	"flag"
	"testing"
	"time"

	. "github.com/onsi/gomega"
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
			RegisterTestingT(t)
			var flags ControllerFlags
			fs := flag.NewFlagSet("test", flag.ContinueOnError)
			flags.AddFlags(fs)

			err := fs.Parse(tt.args)
			Expect(err).ToNot(HaveOccurred(), "Failed to parse flags")

			Expect(flags.EnableLeaderElection).To(Equal(tt.expected.EnableLeaderElection))
			Expect(flags.LeaseDuration).To(Equal(tt.expected.LeaseDuration))
			Expect(flags.RenewDeadline).To(Equal(tt.expected.RenewDeadline))
			Expect(flags.RetryPeriod).To(Equal(tt.expected.RetryPeriod))
		})
	}
}

func TestControllerFlags_InvalidDuration(t *testing.T) {
	RegisterTestingT(t)
	var flags ControllerFlags
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.AddFlags(fs)

	// Test invalid duration format
	err := fs.Parse([]string{"--leader-elect-lease-duration=invalid"})
	Expect(err).To(HaveOccurred(), "Expected error for invalid duration format")
}
