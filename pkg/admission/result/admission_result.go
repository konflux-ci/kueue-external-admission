package result

// AsyncAdmissionResult wraps an AdmissionResult with an optional error for asynchronous operations.
// This is used when admission checks are performed asynchronously and may return either
// a result or an error.
type AsyncAdmissionResult struct {
	AdmissionResult AdmissionResult // The admission result if the check succeeded
	Error           error           // The error if the check failed
}

// admissionResult is the immutable implementation of AdmissionResult returned by Build().
// It encapsulates the admission decision, details, and check name in an immutable structure.
type admissionResult struct {
	shouldAdmit bool     // Whether the workload should be admitted
	details     []string // List of detail strings explaining the decision
	checkName   string   // Name of the admission check that produced this result
}

// Compile-time check to ensure admissionResult implements AdmissionResult interface
var _ AdmissionResult = &admissionResult{}

// CheckName returns the name of the admission check that produced this result.
func (r *admissionResult) CheckName() string {
	return r.checkName
}

// Details returns a copy of the detail strings explaining the admission decision.
func (r *admissionResult) Details() []string {
	return r.details
}

// ShouldAdmit returns true if the workload should be admitted, false otherwise.
func (r *admissionResult) ShouldAdmit() bool {
	return r.shouldAdmit
}

// admissionResultBuilder is the mutable builder implementation for constructing AdmissionResult instances.
// It allows for fluent method chaining to build admission results step by step.
type admissionResultBuilder struct {
	shouldAdmit bool     // Whether the workload should be admitted
	details     []string // List of detail strings explaining the decision
	checkName   string   // Name of the admission check
}

// Compile-time check to ensure admissionResultBuilder implements AdmissionResultBuilder interface
var _ AdmissionResultBuilder = &admissionResultBuilder{}

// NewAdmissionResultBuilder creates a new AdmissionResultBuilder with default values.
// The builder starts with admission allowed by default and an empty details list.
//
// Parameters:
//   - checkName: The name of the admission check that will produce this result
//
// Returns:
//   - AdmissionResultBuilder: A new builder instance ready for configuration
func NewAdmissionResultBuilder(checkName string) AdmissionResultBuilder {
	return &admissionResultBuilder{shouldAdmit: true, details: []string{}, checkName: checkName}
}

// SetAdmissionDenied sets the admission decision to denied and returns the builder for chaining.
func (b *admissionResultBuilder) SetAdmissionDenied() AdmissionResultBuilder {
	b.shouldAdmit = false
	return b
}

// SetAdmissionAllowed sets the admission decision to allowed and returns the builder for chaining.
func (b *admissionResultBuilder) SetAdmissionAllowed() AdmissionResultBuilder {
	b.shouldAdmit = true
	return b
}

// AddDetails adds one or more detail strings to the result and returns the builder for chaining.
//
// Parameters:
//   - details: Variable number of detail strings to add to the result
func (b *admissionResultBuilder) AddDetails(details ...string) AdmissionResultBuilder {
	b.details = append(b.details, details...)
	return b
}

// Build creates the final immutable AdmissionResult instance.
// After calling Build(), the builder should not be used further.
//
// Returns:
//   - AdmissionResult: An immutable admission result with the configured values
func (b *admissionResultBuilder) Build() AdmissionResult {
	return &admissionResult{
		shouldAdmit: b.shouldAdmit,
		details:     b.details,
		checkName:   b.checkName,
	}
}
