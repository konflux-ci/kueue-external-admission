package watcher

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// Admitter determines whether admission should be allowed
type Admitter interface {
	ShouldAdmit(context.Context) (AdmissionResult, error)
	Sync(context.Context, chan<- AsyncAdmissionResult) error
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
	LastResult         AsyncAdmissionResult
}

// AdmissionService manages Admitters for different AdmissionChecks
// Uses sync.Map internally but exposes only type-safe wrapper methods
// This ensures consistent logging and proper type safety
type AdmissionService struct {
	admitters sync.Map // private sync.Map - hides direct access to Store/Load/Delete/Range
	logger    logr.Logger
	// TODO: remove this channel it doesn't belong here. should pass it to the monitor directly from main
	eventsChannel          chan<- event.GenericEvent
	admitterChangeRequests chan AdmitterChangeRequest // Channel for notifying about admitter change requests
	asyncAdmissionResults  chan AsyncAdmissionResult  // Channel for notifying about async admission results
	admissionResultChanged chan AdmissionResult
}

// NewAdmissionService creates a new AdmissionService
// The internal sync.Map is ready to use without explicit initialization
func NewAdmissionService(logger logr.Logger) (*AdmissionService, <-chan event.GenericEvent) {
	eventsCh := make(chan event.GenericEvent)

	return &AdmissionService{
		// sync.Map requires no initialization - zero value is ready to use
		logger:                 logger,
		eventsChannel:          eventsCh,
		asyncAdmissionResults:  make(chan AsyncAdmissionResult),
		admitterChangeRequests: make(chan AdmitterChangeRequest),
	}, eventsCh
}

func (s *AdmissionService) Start(ctx context.Context) error {
	s.logger.Info("Starting AdmissionService")
	go s.manageAdmitters(ctx, s.admitterChangeRequests)
	go s.readAsyncAdmissionResults(ctx, s.asyncAdmissionResults, s.admissionResultChanged)

	<-ctx.Done()
	s.logger.Info("Stopping AdmissionService, context done")
	return ctx.Err()
}

func (s *AdmissionService) SetAdmitter(admissionCheckName string, admitter Admitter) {
	s.admitterChangeRequests <- AdmitterChangeRequest{
		AdmissionCheckName:        admissionCheckName,
		AdmitterChangeRequestType: AdmitterChangeRequestAdd,
		Admitter:                  admitter,
	}
}

func (s *AdmissionService) RemoveAdmitter(admissionCheckName string) {
	s.admitterChangeRequests <- AdmitterChangeRequest{
		AdmissionCheckName:        admissionCheckName,
		AdmitterChangeRequestType: AdmitterChangeRequestRemove,
	}
}

func (s *AdmissionService) readAsyncAdmissionResults(
	ctx context.Context,
	asyncAdmissionResults <-chan AsyncAdmissionResult,
	admissionResultChangedChannel chan<- AdmissionResult,
) {
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("Stopping readAsyncAdmissionResults, context done")
			return
		case result := <-asyncAdmissionResults:
			s.logger.Info(
				"Received async admission result",
				"result", result.AdmissionResult.ShouldAdmit(),
				"providerDetails", result.AdmissionResult.GetProviderDetails(),
				"error", result.Error,
			)
			// TODO: this is a hack to get the admission check name from the provider details
			// we should use a more robust way to get the admission check name
			// I think it's needed to to create aggregated provider details struct in addition
			admissionCheckName := slices.Collect(maps.Keys(result.AdmissionResult.GetProviderDetails()))[0]

			admitterEntry, exists := s.getAdmitterEntry(admissionCheckName)
			if !exists {
				s.logger.Info("Admitter not found, skip storing last sync result", "admissionCheck", admissionCheckName)
				continue
			}

			admissionMetrics := NewAdmissionMetrics(admissionCheckName)

			if result.Error != nil {
				s.logger.Error(result.Error, "Error in async admission result")
				admissionMetrics.RecordError("admission_check_failed")
				continue
			}
			// TODO:compare the entire admission result struct instead of just the should admit.
			// shouldAdmit might be the same, but the reason would be different.

			lastResult := admitterEntry.LastResult.AdmissionResult.ShouldAdmit()
			changed := result.AdmissionResult.ShouldAdmit() != lastResult
			// Update the last result
			admitterEntry.LastResult = result

			if result.Error != nil && changed {
				s.logger.Info(
					"Admission result for %s changed from %v to %v. emitting event",
					"admissionCheck", admissionCheckName,
					lastResult,
					result.AdmissionResult.ShouldAdmit(),
				)
				admissionResultChangedChannel <- result.AdmissionResult
			}
			admissionMetrics.RecordAdmissionCheckStatus(result.AdmissionResult.ShouldAdmit())
		}
	}
}

func (s *AdmissionService) AdmissionResultChanged() <-chan AdmissionResult {
	return s.admissionResultChanged
}

func (s *AdmissionService) manageAdmitters(ctx context.Context, changeRequests chan AdmitterChangeRequest) {

	removeAdmitter := func(admissionCheckName string) {
		entry, ok := s.admitters.Load(admissionCheckName)
		if !ok {
			s.logger.Error(fmt.Errorf("admitter not found"), "Admitter not found", "admissionCheck", admissionCheckName)
			return
		}
		entry.(AdmitterEntry).Cancel()
		s.admitters.Delete(admissionCheckName)
		admissionMetrics := NewAdmissionMetrics(admissionCheckName)
		admissionMetrics.DeleteAdmissionCheckStatus()
		s.logger.Info("Removed admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
	}

	setAdmitter := func(ctx context.Context, admissionCheckName string, admitter Admitter) {
		ctx, cancel := context.WithCancel(ctx)
		s.admitters.Store(
			admissionCheckName,
			AdmitterEntry{
				Admitter: admitter,
				Cancel:   cancel,
			},
		)
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
			s.admitters.Range(func(key, value any) bool {
				removeAdmitter(key.(string))
				return true
			})
			return
		case changeRequest := <-changeRequests:
			switch changeRequest.AdmitterChangeRequestType {
			case AdmitterChangeRequestAdd:
				setAdmitter(ctx, changeRequest.AdmissionCheckName, changeRequest.Admitter)
			case AdmitterChangeRequestRemove:
				removeAdmitter(changeRequest.AdmissionCheckName)
			}
		}
	}
}

// getAdmitter gets the Admitter for a given AdmissionCheck
func (s *AdmissionService) getAdmitterEntry(admissionCheckName string) (AdmitterEntry, bool) {
	value, exists := s.admitters.Load(admissionCheckName)
	if !exists {
		return AdmitterEntry{}, false
	}
	return value.(AdmitterEntry), true
}

// ShouldAdmitWorkload aggregates admission decisions from multiple admitters
// it uses the last result from the admitters to determine the admission decision
func (s *AdmissionService) ShouldAdmitWorkload(ctx context.Context, checkNames []string) (AdmissionResult, error) {
	s.logger.Info("Checking admission for workload", "checks", checkNames)

	builder := NewAdmissionResult()
	builder.SetAdmissionAllowed()

	for _, checkName := range checkNames {
		admitterEntry, exists := s.getAdmitterEntry(checkName)
		if !exists {
			continue
		}
		// Create metrics recorder for this admission check
		admissionMetrics := NewAdmissionMetrics(checkName)

		shouldAdmit := false
		var err error
		if admitterEntry.LastResult != (AsyncAdmissionResult{}) {
			shouldAdmit = admitterEntry.LastResult.AdmissionResult.ShouldAdmit()
			err = admitterEntry.LastResult.Error
			if err != nil {
				s.logger.Error(err, "Failed to check admission", "check", checkName)

				return nil, fmt.Errorf("failed to check admission for %s: %w", checkName, err)
			}
		}

		// Record successful admission decision
		admissionMetrics.RecordDecision(shouldAdmit)

		if !shouldAdmit {
			builder.SetAdmissionDenied()
		}

		// Aggregate provider details from all checks
		for _, details := range admitterEntry.LastResult.AdmissionResult.GetProviderDetails() {
			builder.AddProviderDetails(checkName, details)

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
