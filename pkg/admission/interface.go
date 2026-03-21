package admission

import (
	"context"

	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
)

// Admitter determines whether admission should be allowed
type Admitter interface {
	Sync(context.Context, chan<- result.AsyncAdmissionResult) error
	// Equal reports whether this admitter is functionally equivalent to another admitter.
	// Implementations should compare the relevant configuration fields that determine
	// the admitter's behavior, but ignore internal state like HTTP clients, loggers, etc.
	Equal(other Admitter) bool
}

type MultiCheckAdmitter interface {
	ShouldAdmitWorkload(ctx context.Context, checkNames []string) (result.AggregatedAdmissionResult, error)
}
