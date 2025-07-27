package result

// AsyncAdmissionResult is a result of an admission check that is returned asynchronously
type AsyncAdmissionResult struct {
	AdmissionResult AdmissionResult
	Error           error
}

// admissionResult is the immutable result returned by Build()
type admissionResult struct {
	shouldAdmit bool
	details     []string
	checkName   string
}

var _ AdmissionResult = &admissionResult{}

func (r *admissionResult) CheckName() string {
	return r.checkName
}

func (r *admissionResult) Details() []string {
	return r.details
}
func (r *admissionResult) ShouldAdmit() bool {
	return r.shouldAdmit
}

// admissionResultBuilder is the builder implementation
type admissionResultBuilder struct {
	shouldAdmit bool
	details     []string
	checkName   string
}

var _ AdmissionResultBuilder = &admissionResultBuilder{}

// NewAdmissionResultBuilder creates a new AdmissionResultBuilder with default values
func NewAdmissionResultBuilder(checkName string) AdmissionResultBuilder {
	return &admissionResultBuilder{shouldAdmit: true, details: []string{}, checkName: checkName}
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

func (b *admissionResultBuilder) AddDetails(details ...string) AdmissionResultBuilder {
	b.details = append(b.details, details...)
	return b
}

// Build creates the final immutable AdmissionResult
func (b *admissionResultBuilder) Build() AdmissionResult {
	return &admissionResult{
		shouldAdmit: b.shouldAdmit,
		details:     b.details,
		checkName:   b.checkName,
	}
}
