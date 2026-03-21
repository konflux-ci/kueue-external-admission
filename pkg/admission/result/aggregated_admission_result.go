package result

// aggregatedAdmissionResult is the immutable implementation of AggregatedAdmissionResult.
// It encapsulates the overall admission decision and details from multiple admission checks.
type aggregatedAdmissionResult struct {
	shouldAdmit     bool                // Whether the workload should be admitted based on all checks
	providerDetails map[string][]string // Map of check names to their detail strings
}

// Compile-time check to ensure aggregatedAdmissionResult implements AggregatedAdmissionResult interface
var _ AggregatedAdmissionResult = &aggregatedAdmissionResult{}

// ShouldAdmit returns true if the workload should be admitted based on all checks, false otherwise.
func (r *aggregatedAdmissionResult) ShouldAdmit() bool {
	return r.shouldAdmit
}

// GetProviderDetails returns a copy of the map containing details from all admission checks.
func (r *aggregatedAdmissionResult) GetProviderDetails() map[string][]string {
	return r.providerDetails
}

// aggregatedAdmissionResultBuilder is the mutable builder implementation for constructing
// AggregatedAdmissionResult instances.
// It allows for fluent method chaining to build aggregated admission results from multiple checks.
type aggregatedAdmissionResultBuilder struct {
	shouldAdmit     bool                // Whether the workload should be admitted based on all checks
	providerDetails map[string][]string // Map of check names to their detail strings
}

// NewAggregatedAdmissionResultBuilder creates a new AggregatedAdmissionResultBuilder with default values.
// The builder starts with admission allowed by default and an empty provider details map.
//
// Returns:
//   - AggregatedAdmissionResultBuilder: A new builder instance ready for configuration
func NewAggregatedAdmissionResultBuilder() AggregatedAdmissionResultBuilder {
	return &aggregatedAdmissionResultBuilder{
		shouldAdmit:     true,
		providerDetails: make(map[string][]string),
	}
}

// Compile-time check to ensure aggregatedAdmissionResultBuilder implements AggregatedAdmissionResultBuilder interface
var _ AggregatedAdmissionResultBuilder = &aggregatedAdmissionResultBuilder{}

// AddProviderDetails adds details from a specific admission check and returns the builder for chaining.
//
// Parameters:
//   - checkName: The name of the admission check that produced these details
//   - details: List of detail strings from the admission check
func (a *aggregatedAdmissionResultBuilder) AddProviderDetails(
	checkName string,
	details []string,
) AggregatedAdmissionResultBuilder {
	a.providerDetails[checkName] = details
	return a
}

// SetAdmissionAllowed sets the overall admission decision to allowed and returns the builder for chaining.
func (a *aggregatedAdmissionResultBuilder) SetAdmissionAllowed() AggregatedAdmissionResultBuilder {
	a.shouldAdmit = true
	return a
}

// SetAdmissionDenied sets the overall admission decision to denied and returns the builder for chaining.
func (a *aggregatedAdmissionResultBuilder) SetAdmissionDenied() AggregatedAdmissionResultBuilder {
	a.shouldAdmit = false
	return a
}

// Build creates the final immutable AggregatedAdmissionResult instance.
// After calling Build(), the builder should not be used further.
//
// Returns:
//   - AggregatedAdmissionResult: An immutable aggregated admission result with the configured values
func (a *aggregatedAdmissionResultBuilder) Build() AggregatedAdmissionResult {
	return &aggregatedAdmissionResult{
		shouldAdmit:     a.shouldAdmit,
		providerDetails: a.providerDetails,
	}
}
