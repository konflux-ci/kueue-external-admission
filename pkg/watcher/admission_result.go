package watcher

// AdmissionResult represents the result of an admission check
type AdmissionResult interface {
	ShouldAdmit() bool
	GetProviderDetails() map[string][]string
}

// defaultAdmissionResult is the default implementation of AdmissionResult
type defaultAdmissionResult struct {
	shouldAdmit     bool
	providerDetails map[string][]string // admissionCheckName -> list of provider-specific details (e.g., alert names)
}

// NewAdmissionResult creates a new AdmissionResult with default values
func NewAdmissionResult() *defaultAdmissionResult {
	return &defaultAdmissionResult{
		shouldAdmit:     true,
		providerDetails: make(map[string][]string),
	}
}

// ShouldAdmit returns whether the workload should be admitted
func (r *defaultAdmissionResult) ShouldAdmit() bool {
	return r.shouldAdmit
}

// GetProviderDetails returns the map of provider-specific details per admission check
func (r *defaultAdmissionResult) GetProviderDetails() map[string][]string {
	return r.providerDetails
}

// setAdmissionDenied marks the admission as denied
func (r *defaultAdmissionResult) setAdmissionDenied() {
	r.shouldAdmit = false
}

func (r *defaultAdmissionResult) setAdmissionAllowed() {
	r.shouldAdmit = true
}

// addProviderDetails adds provider-specific details for a specific admission check
func (r *defaultAdmissionResult) addProviderDetails(checkName string, details []string) {
	if len(details) > 0 {
		r.providerDetails[checkName] = details
	}
}
