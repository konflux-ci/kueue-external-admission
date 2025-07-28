package manager

import (
	"context"
	"maps"
	"reflect"
	"slices"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/monitoring"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
)

type ResultManager struct {
	logger          logr.Logger
	resultsRegistry map[string]result.AsyncAdmissionResult
}

func NewResultManager(logger logr.Logger) *ResultManager {
	return &ResultManager{
		logger:          logger,
		resultsRegistry: make(map[string]result.AsyncAdmissionResult),
	}
}

func (s *ResultManager) Run(
	ctx context.Context,
	incomingResults <-chan result.AsyncAdmissionResult,
	resultNotifications chan<- result.AdmissionResult,
	resultSnapshot chan<- map[string]result.AsyncAdmissionResult,
	removalNotifications <-chan string,
) {
	resultsRegistry := s.resultsRegistry

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

			admissionMetrics := monitoring.NewAdmissionMetrics(admissionCheckName)

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

			s.logger.Info(
				"AFTER storing",
				"registrySize", len(resultsRegistry),
				"keys", slices.Collect(maps.Keys(resultsRegistry)),
			)

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
