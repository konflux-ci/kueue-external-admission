package watcher

// AdmissionResult represents the result of an admission check
type AdmissionResult interface {
	ShouldAdmit() bool
	GetFiringAlerts() map[string][]string
}

// defaultAdmissionResult is the default implementation of AdmissionResult
type defaultAdmissionResult struct {
	shouldAdmit  bool
	firingAlerts map[string][]string // admissionCheckName -> list of firing alert names
}

// NewAdmissionResult creates a new AdmissionResult with default values
func NewAdmissionResult() *defaultAdmissionResult {
	return &defaultAdmissionResult{
		shouldAdmit:  true,
		firingAlerts: make(map[string][]string),
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

// setAdmissionDenied marks the admission as denied
func (r *defaultAdmissionResult) setAdmissionDenied() {
	r.shouldAdmit = false
}

func (r *defaultAdmissionResult) setAdmissionAllowed() {
	r.shouldAdmit = true
}

// addFiringAlerts adds firing alerts for a specific admission check
func (r *defaultAdmissionResult) addFiringAlerts(checkName string, alerts []string) {
	if len(alerts) > 0 {
		r.firingAlerts[checkName] = alerts
	}
}
