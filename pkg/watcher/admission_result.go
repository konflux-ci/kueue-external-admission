package watcher

// AdmissionResult represents the result of an admission check
type AdmissionResult interface {
	ShouldAdmit() bool
	GetProviderDetails() map[string][]string
}

// AdmissionResultBuilder allows providers to construct AdmissionResult instances
type AdmissionResultBuilder interface {
	SetAdmissionDenied() AdmissionResultBuilder
	SetAdmissionAllowed() AdmissionResultBuilder
	AddProviderDetails(checkName string, details []string) AdmissionResultBuilder
	Build() AdmissionResult
}

// admissionResultBuilder is the builder implementation
type admissionResultBuilder struct {
	shouldAdmit     bool
	providerDetails map[string][]string
}

// admissionResult is the immutable result returned by Build()
type admissionResult struct {
	shouldAdmit     bool
	providerDetails map[string][]string
}

// NewAdmissionResult creates a new AdmissionResultBuilder with default values
func NewAdmissionResult() AdmissionResultBuilder {
	return &admissionResultBuilder{
		shouldAdmit:     true,
		providerDetails: make(map[string][]string),
	}
}

// Builder methods - return the builder for method chaining
func (b *admissionResultBuilder) SetAdmissionDenied() AdmissionResultBuilder {
	b.shouldAdmit = false
	return b
}

func (b *admissionResultBuilder) SetAdmissionAllowed() AdmissionResultBuilder {
	b.shouldAdmit = true
	return b
}

func (b *admissionResultBuilder) AddProviderDetails(checkName string, details []string) AdmissionResultBuilder {
	if len(details) > 0 {
		b.providerDetails[checkName] = details
	}
	return b
}

// Build creates the final immutable AdmissionResult
func (b *admissionResultBuilder) Build() AdmissionResult {
	// Create a copy of the provider details to ensure immutability
	detailsCopy := make(map[string][]string)
	for k, v := range b.providerDetails {
		detailsCopy[k] = append([]string(nil), v...)
	}

	return &admissionResult{
		shouldAdmit:     b.shouldAdmit,
		providerDetails: detailsCopy,
	}
}

// AdmissionResult implementation
func (r *admissionResult) ShouldAdmit() bool {
	return r.shouldAdmit
}

func (r *admissionResult) GetProviderDetails() map[string][]string {
	return r.providerDetails
}
