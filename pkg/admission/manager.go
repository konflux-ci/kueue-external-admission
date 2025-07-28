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

// Helper function for debugging
func getMapKeys(m map[string]result.AsyncAdmissionResult) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

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
	logger               logr.Logger
	admitterCommands     chan AdmitterChangeRequest                  // Commands to add/remove admitters
	incomingResults      chan result.AsyncAdmissionResult            // Admission results from admitters
	resultNotifications  chan result.AdmissionResult                 // Notifications about result changes
	removalNotifications chan string                                 // Notifications about admitter removals
	resultSnapshot       chan map[string]result.AsyncAdmissionResult // Snapshots of current results
}

// NewManager creates a new AdmissionService
// The internal sync.Map is ready to use without explicit initialization
func NewManager(logger logr.Logger) *AdmissionManager {
	return &AdmissionManager{
		// sync.Map requires no initialization - zero value is ready to use
		logger:               logger,
		incomingResults:      make(chan result.AsyncAdmissionResult),
		admitterCommands:     make(chan AdmitterChangeRequest),
		resultNotifications:  make(chan result.AdmissionResult, 100),
		removalNotifications: make(chan string),
		resultSnapshot:       make(chan map[string]result.AsyncAdmissionResult),
	}
}

func (s *AdmissionManager) Start(ctx context.Context) error {
	s.logger.Info("Starting AdmissionService")
	go s.manageAdmitters(ctx, s.admitterCommands)
	go s.readAsyncAdmissionResults(
		ctx,
		s.incomingResults,
		s.resultNotifications,
		s.resultSnapshot,
		s.removalNotifications,
	)

	<-ctx.Done()
	s.logger.Info("Stopping AdmissionService, context done")
	return ctx.Err()
}

func (s *AdmissionManager) SetAdmitter(admissionCheckName string, admitter Admitter) {
	s.admitterCommands <- AdmitterChangeRequest{
		AdmissionCheckName:        admissionCheckName,
		AdmitterChangeRequestType: AdmitterChangeRequestAdd,
		Admitter:                  admitter,
	}
}

func (s *AdmissionManager) RemoveAdmitter(admissionCheckName string) {
	s.admitterCommands <- AdmitterChangeRequest{
		AdmissionCheckName:        admissionCheckName,
		AdmitterChangeRequestType: AdmitterChangeRequestRemove,
	}
}

func (s *AdmissionManager) AdmissionResultChanged() <-chan result.AdmissionResult {
	return s.resultNotifications
}

func (s *AdmissionManager) readAsyncAdmissionResults(
	ctx context.Context,
	incomingResults <-chan result.AsyncAdmissionResult,
	resultNotifications chan<- result.AdmissionResult,
	resultSnapshot chan<- map[string]result.AsyncAdmissionResult,
	removalNotifications <-chan string,
) {

	resultsRegistry := make(map[string]result.AsyncAdmissionResult)

	for {
		s.logger.Info("Select loop iteration", "registrySize", len(resultsRegistry))
		select {
		case <-ctx.Done():
			s.logger.Info("Stopping readAsyncAdmissionResults, context done")
			return
		case admissionCheckName := <-removalNotifications:
			s.logger.Info(
				"REMOVING admitter from registry",
				"admissionCheck", admissionCheckName,
				"registrySize", len(resultsRegistry),
			)
			// Admitter was removed from the manager, remove its latest result from the cache
			delete(resultsRegistry, admissionCheckName)
			s.logger.Info("AFTER removal", "registrySize", len(resultsRegistry))
		case resultSnapshot <- maps.Clone(resultsRegistry):
			s.logger.Info("PUBLISHING results", "registrySize", len(resultsRegistry), "results", resultsRegistry)
		case newResult := <-incomingResults:
			s.logger.Info(
				"RECEIVED new result",
				"admissionCheck", newResult.AdmissionResult.CheckName(),
				"registrySize", len(resultsRegistry),
			)
			admissionCheckName := newResult.AdmissionResult.CheckName()
			s.logger.Info(
				"Received async admission result",
				"admissionCheck", admissionCheckName,
				"result", newResult.AdmissionResult.ShouldAdmit(),
				"details", newResult.AdmissionResult.Details(),
				"error", newResult.Error,
			)

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
			s.logger.Info(
				"STORING result",
				"admissionCheck", admissionCheckName,
				"changed", changed,
				"beforeSize", len(resultsRegistry),
			)
			resultsRegistry[admissionCheckName] = newResult

			// Debug: Check what's actually in the stored result
			storedResult := resultsRegistry[admissionCheckName]
			if storedResult.AdmissionResult != nil {
				s.logger.Info("STORED result details",
					"admissionCheck", admissionCheckName,
					"shouldAdmit", storedResult.AdmissionResult.ShouldAdmit(),
					"checkName", storedResult.AdmissionResult.CheckName(),
					"details", storedResult.AdmissionResult.Details(),
					"error", storedResult.Error,
				)
			} else {
				s.logger.Info("STORED result has NIL AdmissionResult!", "admissionCheck", admissionCheckName)
			}

			s.logger.Info("AFTER storing", "registrySize", len(resultsRegistry), "keys", getMapKeys(resultsRegistry))

			if changed {
				if newResult.Error == nil {
					// Only send successful admission results to the channel
					resultNotifications <- newResult.AdmissionResult
				} else {
					s.logger.Info("Admission result changed but has error, not notifying",
						"admissionCheck", admissionCheckName,
						"error", newResult.Error)
				}
			}
			admissionMetrics.RecordAdmissionCheckStatus(newResult.AdmissionResult.ShouldAdmit())
		}
	}
}

func (s *AdmissionManager) manageAdmitters(ctx context.Context, admitterCommands chan AdmitterChangeRequest) {

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
		if err := admitter.Sync(ctx, s.incomingResults); err != nil {
			retryIn := 15 * time.Second
			s.logger.Error(err, "Failed to sync admitter", "admissionCheck", admissionCheckName, "retryIn", retryIn)
			go func() {
				time.Sleep(retryIn)
				admitterCommands <- AdmitterChangeRequest{
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
		case changeRequest := <-admitterCommands:
			// TODO: generate and id for the admitter
			switch changeRequest.AdmitterChangeRequestType {
			case AdmitterChangeRequestAdd:
				setAdmitter(ctx, changeRequest.AdmissionCheckName, changeRequest.Admitter)
			case AdmitterChangeRequestRemove:
				removeAdmitter(changeRequest.AdmissionCheckName)
				// TODO: there might be a race condition here, if the admitter is removed and the result is published
				// need to consider a periodic cleanup of the results registry
				s.removalNotifications <- changeRequest.AdmissionCheckName
			}
		}
	}
}

func (s *AdmissionManager) ShouldAdmitWorkload(
	ctx context.Context, checkNames []string,
) (result.AggregatedAdmissionResult, error) {
	return s.shouldAdmitWorkload(ctx, checkNames, s.resultSnapshot)
}

// ShouldAdmitWorkload aggregates admission decisions from multiple admitters
// it uses the last result from the admitters to determine the admission decision
func (s *AdmissionManager) shouldAdmitWorkload(
	ctx context.Context,
	checkNames []string,
	resultSnapshot <-chan map[string]result.AsyncAdmissionResult,
) (result.AggregatedAdmissionResult, error) {
	s.logger.Info("Checking admission for workload", "checks", checkNames)

	builder := result.NewAggregatedAdmissionResultBuilder()
	builder.SetAdmissionAllowed()

	var resultsRegistry map[string]result.AsyncAdmissionResult

	select {
	case <-ctx.Done():
		s.logger.Info("Stopping shouldAdmitWorkload, context done")
		return nil, ctx.Err()
	case resultsRegistry = <-resultSnapshot:
		s.logger.Info("Received results from channel", "results", resultsRegistry, "count", len(resultsRegistry))

		// Debug: Check each result in detail
		for checkName, asyncResult := range resultsRegistry {
			if asyncResult.AdmissionResult != nil {
				s.logger.Info("Channel result details",
					"check", checkName,
					"shouldAdmit", asyncResult.AdmissionResult.ShouldAdmit(),
					"checkName", asyncResult.AdmissionResult.CheckName(),
					"details", asyncResult.AdmissionResult.Details(),
					"error", asyncResult.Error,
				)
			} else {
				s.logger.Info("Channel result has NIL AdmissionResult!", "check", checkName)
			}
		}
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
