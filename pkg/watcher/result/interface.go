package result

// AdmissionResult represents the result of an admission check
type AdmissionResult interface {
	ShouldAdmit() bool
	CheckName() string
	Details() []string
}

// AdmissionResultBuilder allows providers to construct AdmissionResult instances
type AdmissionResultBuilder interface {
	SetAdmissionDenied() AdmissionResultBuilder
	SetAdmissionAllowed() AdmissionResultBuilder
	AddDetails(details ...string) AdmissionResultBuilder
	Build() AdmissionResult
}

// AdmissionResult represents the result of an admission check
type AggregatedAdmissionResult interface {
	ShouldAdmit() bool
	GetProviderDetails() map[string][]string
}

// AdmissionResultBuilder allows providers to construct AdmissionResult instances
type AggregatedAdmissionResultBuilder interface {
	SetAdmissionDenied() AggregatedAdmissionResultBuilder
	SetAdmissionAllowed() AggregatedAdmissionResultBuilder
	AddProviderDetails(checkName string, details []string) AggregatedAdmissionResultBuilder
	Build() AggregatedAdmissionResult
}
