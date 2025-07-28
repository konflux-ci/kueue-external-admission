package admission

import (
	"context"

	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
)

// Admitter determines whether admission should be allowed
type Admitter interface {
	Sync(context.Context, chan<- result.AsyncAdmissionResult) error
}

type MultiCheckAdmitter interface {
	ShouldAdmitWorkload(ctx context.Context, checkNames []string) (result.AggregatedAdmissionResult, error)
}
