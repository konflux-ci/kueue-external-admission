package admission

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
)

// Admitter determines whether admission should be allowed
type Admitter interface {
	Sync(context.Context, chan<- result.AsyncAdmissionResult) error
}

type MultiCheckAdmitter interface {
	ShouldAdmitWorkload(ctx context.Context, checkNames []string) (result.AggregatedAdmissionResult, error)
}

type AdmitterChangeRequestType = string

const (
	AdmitterChangeRequestAdd    AdmitterChangeRequestType = "add"
	AdmitterChangeRequestRemove AdmitterChangeRequestType = "remove"
)

type AdmitterChangeRequest struct {
	AdmissionCheckName string
	AdmitterChangeRequestType
	// TODO: consider moving the factory to the admission service
	Admitter Admitter
}

type AdmitterEntry struct {
	Admitter           Admitter
	AdmissionCheckName string
	Cancel             context.CancelFunc
}

// AdmissionManager manages Admitters for different AdmissionChecks
type AdmissionManager struct {
	logger                 logr.Logger
	admitterChangeRequests chan AdmitterChangeRequest                  // Channel for admitter change requests
	asyncAdmissionResults  chan result.AsyncAdmissionResult            // Channel for async admission results
	admissionResultChanged chan result.AdmissionResult                 // Channel for admission result changes
	admitterRemoved        chan string                                 // Channel for admitter removal
	publishResults         chan map[string]result.AsyncAdmissionResult // Channel for results
}

// NewAdmissionService creates a new AdmissionService
// The internal sync.Map is ready to use without explicit initialization
func NewAdmissionService(logger logr.Logger) *AdmissionManager {
	return &AdmissionManager{
		// sync.Map requires no initialization - zero value is ready to use
		logger:                 logger,
		asyncAdmissionResults:  make(chan result.AsyncAdmissionResult),
		admitterChangeRequests: make(chan AdmitterChangeRequest),
		admissionResultChanged: make(chan result.AdmissionResult),
		admitterRemoved:        make(chan string),
		publishResults:         make(chan map[string]result.AsyncAdmissionResult),
	}
}

func (s *AdmissionManager) Start(ctx context.Context) error {
	s.logger.Info("Starting AdmissionService")
	go s.manageAdmitters(ctx, s.admitterChangeRequests)
	go s.readAsyncAdmissionResults(ctx, s.asyncAdmissionResults, s.admissionResultChanged, s.publishResults)

	<-ctx.Done()
	s.logger.Info("Stopping AdmissionService, context done")
	return ctx.Err()
}

func (s *AdmissionManager) SetAdmitter(admissionCheckName string, admitter Admitter) {
	s.admitterChangeRequests <- AdmitterChangeRequest{
		AdmissionCheckName:        admissionCheckName,
		AdmitterChangeRequestType: AdmitterChangeRequestAdd,
		Admitter:                  admitter,
	}
}

func (s *AdmissionManager) RemoveAdmitter(admissionCheckName string) {
	s.admitterChangeRequests <- AdmitterChangeRequest{
		AdmissionCheckName:        admissionCheckName,
		AdmitterChangeRequestType: AdmitterChangeRequestRemove,
	}
}

func (s *AdmissionManager) AdmissionResultChanged() <-chan result.AdmissionResult {
	return s.admissionResultChanged
}

func (s *AdmissionManager) readAsyncAdmissionResults(
	ctx context.Context,
	asyncAdmissionResults <-chan result.AsyncAdmissionResult,
	admissionResultChangedChannel chan<- result.AdmissionResult,
	publishResults chan<- map[string]result.AsyncAdmissionResult,
) {

	resultsRegistry := make(map[string]result.AsyncAdmissionResult)

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("Stopping readAsyncAdmissionResults, context done")
			return
		case admissionCheckName := <-s.admitterRemoved:
			// Admitter was removed from the manager, remove its latest result from the cache
			delete(resultsRegistry, admissionCheckName)
		case publishResults <- maps.Clone(resultsRegistry):
			s.logger.Info("Publishing results", "results", resultsRegistry)
		case newResult := <-asyncAdmissionResults:
			s.logger.Info(
				"Received async admission result",
				"result", newResult.AdmissionResult.ShouldAdmit(),
				"details", newResult.AdmissionResult.Details(),
				"error", newResult.Error,
			)
			admissionCheckName := newResult.AdmissionResult.CheckName()

			admissionMetrics := NewAdmissionMetrics(admissionCheckName)

			if newResult.Error != nil {
				s.logger.Error(newResult.Error, "Error in async admission result")
				admissionMetrics.RecordError("admission_check_failed")
			}

			lastResult, ok := resultsRegistry[admissionCheckName]
			if !ok {
				s.logger.Info("No last sync result found, using default value", "admissionCheck", admissionCheckName)
				lastResult = result.AsyncAdmissionResult{
					AdmissionResult: result.NewAdmissionResultBuilder(admissionCheckName).SetAdmissionDenied().Build(),
				}
			}

			changed := !reflect.DeepEqual(newResult, lastResult)
			resultsRegistry[admissionCheckName] = newResult

			if newResult.Error != nil && changed {
				s.logger.Info(
					"Admission result for %s changed from %v to %v. emitting event",
					"admissionCheck", admissionCheckName,
					"lastResult", lastResult,
					"newResult", newResult,
				)
				admissionResultChangedChannel <- newResult.AdmissionResult
			}
			admissionMetrics.RecordAdmissionCheckStatus(newResult.AdmissionResult.ShouldAdmit())
		}
	}
}

func (s *AdmissionManager) manageAdmitters(ctx context.Context, changeRequests chan AdmitterChangeRequest) {

	admitters := make(map[string]*AdmitterEntry)

	removeAdmitter := func(admissionCheckName string) {
		entry, ok := admitters[admissionCheckName]
		if !ok {
			s.logger.Error(fmt.Errorf("admitter not found"), "Admitter not found", "admissionCheck", admissionCheckName)
			return
		}
		entry.Cancel()
		delete(admitters, admissionCheckName)
		admissionMetrics := NewAdmissionMetrics(admissionCheckName)
		admissionMetrics.DeleteAdmissionCheckStatus()
		s.logger.Info("Removed admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
	}

	setAdmitter := func(ctx context.Context, admissionCheckName string, admitter Admitter) {
		// need to handle a case where the admitter is already set
		entry, ok := admitters[admissionCheckName]
		if ok && reflect.DeepEqual(entry.Admitter, admitter) {
			s.logger.Info("Admitter already set, skipping", "admissionCheck", admissionCheckName)
			return
		} else if ok {
			s.logger.Info("Replacing existing admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
			removeAdmitter(admissionCheckName)
			s.logger.Info("Removed old admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
		}

		ctx, cancel := context.WithCancel(ctx)
		admitters[admissionCheckName] = &AdmitterEntry{
			AdmissionCheckName: admissionCheckName,
			Admitter:           admitter,
			Cancel:             cancel,
		}
		if err := admitter.Sync(ctx, s.asyncAdmissionResults); err != nil {
			retryIn := 15 * time.Second
			s.logger.Error(err, "Failed to sync admitter", "admissionCheck", admissionCheckName, "retryIn", retryIn)
			go func() {
				time.Sleep(retryIn)
				changeRequests <- AdmitterChangeRequest{
					AdmissionCheckName:        admissionCheckName,
					AdmitterChangeRequestType: AdmitterChangeRequestAdd,
					Admitter:                  admitter,
				}
			}()
		}
		admissionMetrics := NewAdmissionMetrics(admissionCheckName)
		// Set initial status to true just to make sure that the metric is set
		admissionMetrics.RecordAdmissionCheckStatus(true)
		s.logger.Info("Added admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
	}

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("Stopping ManageAdmitters, context done")
			for admissionCheckName := range admitters {
				removeAdmitter(admissionCheckName)
			}
			return
		case changeRequest := <-changeRequests:
			switch changeRequest.AdmitterChangeRequestType {
			case AdmitterChangeRequestAdd:
				setAdmitter(ctx, changeRequest.AdmissionCheckName, changeRequest.Admitter)
			case AdmitterChangeRequestRemove:
				removeAdmitter(changeRequest.AdmissionCheckName)
				s.admitterRemoved <- changeRequest.AdmissionCheckName
			}
		}
	}
}

func (s *AdmissionManager) ShouldAdmitWorkload(
	ctx context.Context, checkNames []string,
) (result.AggregatedAdmissionResult, error) {
	return s.shouldAdmitWorkload(ctx, checkNames, s.publishResults)
}

// ShouldAdmitWorkload aggregates admission decisions from multiple admitters
// it uses the last result from the admitters to determine the admission decision
func (s *AdmissionManager) shouldAdmitWorkload(
	ctx context.Context,
	checkNames []string,
	publishResults <-chan map[string]result.AsyncAdmissionResult,
) (result.AggregatedAdmissionResult, error) {
	s.logger.Info("Checking admission for workload", "checks", checkNames)

	builder := result.NewAggregatedAdmissionResultBuilder()
	builder.SetAdmissionAllowed()

	var resultsRegistry map[string]result.AsyncAdmissionResult

	select {
	case <-ctx.Done():
		s.logger.Info("Stopping shouldAdmitWorkload, context done")
		return nil, ctx.Err()
	case resultsRegistry = <-publishResults:
		s.logger.Info("Received results", "results", resultsRegistry)
	}

	for _, checkName := range checkNames {
		lastResult, exists := resultsRegistry[checkName]
		if !exists {
			continue
		}

		shouldAdmit := false
		if lastResult != (result.AsyncAdmissionResult{}) {
			err := lastResult.Error
			if err != nil {
				s.logger.Error(err, "Failed to check admission", "check", checkName)
				return nil, fmt.Errorf("failed to check admission for %s: %w", checkName, err)
			}
			shouldAdmit = lastResult.AdmissionResult.ShouldAdmit()
			// Aggregate provider details from all checks
			builder.AddProviderDetails(checkName, lastResult.AdmissionResult.Details())
		} else {
			s.logger.Info("No last result found for AdmissionCheck", "check", checkName)
			builder.AddProviderDetails(checkName, []string{"No last result found for AdmissionCheck. Denied by default."})
			shouldAdmit = false

		}

		// Record metrics
		admissionMetrics := NewAdmissionMetrics(checkName)
		admissionMetrics.RecordDecision(shouldAdmit)

		if !shouldAdmit {
			builder.SetAdmissionDenied()
		}
	}

	finalResult := builder.Build()

	s.logger.Info("Workload admission decision completed",
		"shouldAdmit", finalResult.ShouldAdmit(),
		"providerDetails", finalResult.GetProviderDetails(),
		"checksEvaluated", len(checkNames),
	)

	return finalResult, nil
}
