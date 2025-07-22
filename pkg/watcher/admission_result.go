package watcher

// AdmissionResult represents the result of an admission check
type AdmissionResult interface {
	ShouldAdmit() bool
	GetFiringAlerts() map[string][]string
	GetErrors() map[string]error
}

// defaultAdmissionResult is the default implementation of AdmissionResult
type defaultAdmissionResult struct {
	shouldAdmit  bool
	firingAlerts map[string][]string // admissionCheckName -> list of firing alert names
	errors       map[string]error    // admissionCheckName -> error
}

// NewAdmissionResult creates a new AdmissionResult with default values
func NewAdmissionResult() AdmissionResult {
	return &defaultAdmissionResult{
		shouldAdmit:  true,
		firingAlerts: make(map[string][]string),
		errors:       make(map[string]error),
	}
}

// ShouldAdmit returns whether the workload should be admitted
func (r *defaultAdmissionResult) ShouldAdmit() bool {
	return r.shouldAdmit
}

// GetFiringAlerts returns the map of firing alerts per admission check
func (r *defaultAdmissionResult) GetFiringAlerts() map[string][]string {
	return r.firingAlerts
}

// GetErrors returns the map of errors per admission check
func (r *defaultAdmissionResult) GetErrors() map[string]error {
	return r.errors
}

// setAdmissionDenied marks the admission as denied
func (r *defaultAdmissionResult) setAdmissionDenied() {
	r.shouldAdmit = false
}

// addFiringAlerts adds firing alerts for a specific admission check
func (r *defaultAdmissionResult) addFiringAlerts(checkName string, alerts []string) {
	if len(alerts) > 0 {
		r.firingAlerts[checkName] = alerts
	}
}

// addError adds an error for a specific admission check
func (r *defaultAdmissionResult) addError(checkName string, err error) {
	r.errors[checkName] = err
}
