// Package result provides interfaces and implementations for admission check results.
// It defines the contract for individual admission results and aggregated results
// from multiple admission checks, along with builder patterns for constructing them.
package result

// AdmissionResult represents the result of a single admission check.
// It provides information about whether a workload should be admitted and
// includes details about the decision and the check name.
type AdmissionResult interface {
	// ShouldAdmit returns true if the workload should be admitted, false otherwise
	ShouldAdmit() bool
	// CheckName returns the name of the admission check that produced this result
	CheckName() string
	// Details returns a list of detail strings explaining the admission decision
	Details() []string
}

// AdmissionResultBuilder provides a fluent interface for constructing AdmissionResult instances.
// It follows the builder pattern to allow for easy construction of admission results
// with method chaining.
type AdmissionResultBuilder interface {
	// SetAdmissionDenied sets the admission decision to denied and returns the builder for chaining
	SetAdmissionDenied() AdmissionResultBuilder
	// SetAdmissionAllowed sets the admission decision to allowed and returns the builder for chaining
	SetAdmissionAllowed() AdmissionResultBuilder
	// AddDetails adds one or more detail strings to the result and returns the builder for chaining
	AddDetails(details ...string) AdmissionResultBuilder
	// Build creates the final immutable AdmissionResult instance
	Build() AdmissionResult
}

// AggregatedAdmissionResult represents the combined result of multiple admission checks.
// It provides the overall admission decision and details from all contributing checks.
type AggregatedAdmissionResult interface {
	// ShouldAdmit returns true if the workload should be admitted based on all checks, false otherwise
	ShouldAdmit() bool
	// GetProviderDetails returns a map of check names to their detail strings
	GetProviderDetails() map[string][]string
}

// AggregatedAdmissionResultBuilder provides a fluent interface for constructing AggregatedAdmissionResult instances.
// It allows building aggregated results from multiple admission checks with method chaining.
type AggregatedAdmissionResultBuilder interface {
	// SetAdmissionDenied sets the overall admission decision to denied and returns the builder for chaining
	SetAdmissionDenied() AggregatedAdmissionResultBuilder
	// SetAdmissionAllowed sets the overall admission decision to allowed and returns the builder for chaining
	SetAdmissionAllowed() AggregatedAdmissionResultBuilder
	// AddProviderDetails adds details from a specific admission check and returns the builder for chaining
	AddProviderDetails(checkName string, details []string) AggregatedAdmissionResultBuilder
	// Build creates the final immutable AggregatedAdmissionResult instance
	Build() AggregatedAdmissionResult
}
