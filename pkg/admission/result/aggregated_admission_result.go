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

func NewAggregatedAdmissionResultBuilder() AggregatedAdmissionResultBuilder {
	return &aggregatedAdmissionResultBuilder{
		shouldAdmit:     true,
		providerDetails: make(map[string][]string),
	}
}

var _ AggregatedAdmissionResultBuilder = &aggregatedAdmissionResultBuilder{}

// AddProviderDetails implements AggregateAdmissionResultBuilder.
func (a *aggregatedAdmissionResultBuilder) AddProviderDetails(
	checkName string,
	details []string,
) AggregatedAdmissionResultBuilder {
	a.providerDetails[checkName] = details
	return a
}

// SetAdmissionAllowed implements AggregateAdmissionResultBuilder.
func (a *aggregatedAdmissionResultBuilder) SetAdmissionAllowed() AggregatedAdmissionResultBuilder {
	a.shouldAdmit = true
	return a
}

// SetAdmissionDenied implements AggregateAdmissionResultBuilder.
func (a *aggregatedAdmissionResultBuilder) SetAdmissionDenied() AggregatedAdmissionResultBuilder {
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
