package result

type aggregatedAdmissionResult struct {
	shouldAdmit     bool
	providerDetails map[string][]string
}

var _ AggregatedAdmissionResult = &aggregatedAdmissionResult{}

func (r *aggregatedAdmissionResult) ShouldAdmit() bool {
	return r.shouldAdmit
}

func (r *aggregatedAdmissionResult) GetProviderDetails() map[string][]string {
	return r.providerDetails
}

type aggregatedAdmissionResultBuilder struct {
	shouldAdmit     bool
	providerDetails map[string][]string
}

func NewAggregatedAdmissionResult() AggregateAdmissionResultBuilder {
	return &aggregatedAdmissionResultBuilder{
		shouldAdmit:     true,
		providerDetails: make(map[string][]string),
	}
}

var _ AggregateAdmissionResultBuilder = &aggregatedAdmissionResultBuilder{}

// AddProviderDetails implements AggregateAdmissionResultBuilder.
func (a *aggregatedAdmissionResultBuilder) AddProviderDetails(checkName string, details []string) AggregateAdmissionResultBuilder {
	a.providerDetails[checkName] = details
	return a
}

// SetAdmissionAllowed implements AggregateAdmissionResultBuilder.
func (a *aggregatedAdmissionResultBuilder) SetAdmissionAllowed() AggregateAdmissionResultBuilder {
	a.shouldAdmit = true
	return a
}

// SetAdmissionDenied implements AggregateAdmissionResultBuilder.
func (a *aggregatedAdmissionResultBuilder) SetAdmissionDenied() AggregateAdmissionResultBuilder {
	a.shouldAdmit = false
	return a
}

// Build implements AggregateAdmissionResultBuilder.
func (a *aggregatedAdmissionResultBuilder) Build() AggregatedAdmissionResult {
	return &aggregatedAdmissionResult{
		shouldAdmit:     a.shouldAdmit,
		providerDetails: a.providerDetails,
	}
}
