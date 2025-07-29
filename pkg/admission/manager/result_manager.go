package manager

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"time"

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

func (m *ResultManager) Run(
	ctx context.Context,
	incomingResults <-chan result.AsyncAdmissionResult,
	resultNotifications chan<- result.AdmissionResult,
	resultSnapshot chan<- map[string]result.AsyncAdmissionResult,
	admitterCommands chan<- admitterCmdFunc,
) {
	resultsRegistry := m.resultsRegistry
	ticker := time.NewTicker(1 * time.Minute)
	for {
		m.logger.Info("Select loop iteration", "registrySize", len(resultsRegistry))
		select {
		case <-ctx.Done():
			m.logger.Info("Stopping readAsyncAdmissionResults, context done")
			return

		// Clean the local results registry. Remove results for which the admitter is not present.
		case <-ticker.C:
			m.removeStaleResults(admitterCommands)

		// Publish the current state of the results registry.
		case resultSnapshot <- maps.Clone(resultsRegistry):
			m.logger.Info("PUBLISHING results", "registrySize", len(resultsRegistry), "results", resultsRegistry)

		// Receive new results from the admitters.
		case newResult := <-incomingResults:
			// move to a separate method
			// check if it can be simplified
			m.logger.Info(
				"RECEIVED new result",
				"admissionCheck", newResult.AdmissionResult.CheckName(),
				"registrySize", len(resultsRegistry),
			)

			admissionCheckName := newResult.AdmissionResult.CheckName()
			m.logger.Info(
				"Received async admission result",
				"admissionCheck", admissionCheckName,
				"result", newResult.AdmissionResult.ShouldAdmit(),
				"details", newResult.AdmissionResult.Details(),
				"error", newResult.Error,
			)

			admissionMetrics := monitoring.NewAdmissionMetrics(admissionCheckName)

			if newResult.Error != nil {
				m.logger.Error(newResult.Error, "Error in async admission result")
				admissionMetrics.RecordError("admission_check_failed")
			}

			lastResult, ok := resultsRegistry[admissionCheckName]
			if !ok {
				m.logger.Info("No last sync result found, using default value", "admissionCheck", admissionCheckName)
				lastResult = result.AsyncAdmissionResult{
					AdmissionResult: result.NewAdmissionResultBuilder(admissionCheckName).SetAdmissionDenied().Build(),
				}
			}

			changed := !reflect.DeepEqual(newResult, lastResult)
			m.logger.Info(
				"STORING result",
				"admissionCheck", admissionCheckName,
				"changed", changed,
				"beforeSize", len(resultsRegistry),
			)
			resultsRegistry[admissionCheckName] = newResult

			// Debug: Check what's actually in the stored result
			storedResult := resultsRegistry[admissionCheckName]
			if storedResult.AdmissionResult != nil {
				m.logger.Info("STORED result details",
					"admissionCheck", admissionCheckName,
					"shouldAdmit", storedResult.AdmissionResult.ShouldAdmit(),
					"checkName", storedResult.AdmissionResult.CheckName(),
					"details", storedResult.AdmissionResult.Details(),
					"error", storedResult.Error,
				)
			} else {
				m.logger.Info("STORED result has NIL AdmissionResult!", "admissionCheck", admissionCheckName)
			}

			m.logger.Info(
				"AFTER storing",
				"registrySize", len(resultsRegistry),
				"keys", slices.Collect(maps.Keys(resultsRegistry)),
			)

			if changed {
				if newResult.Error == nil {
					// Only send successful admission results to the channel
					resultNotifications <- newResult.AdmissionResult
				} else {
					m.logger.Info("Admission result changed but has error, not notifying",
						"admissionCheck", admissionCheckName,
						"error", newResult.Error)
				}
			}
			admissionMetrics.RecordAdmissionCheckStatus(newResult.AdmissionResult.ShouldAdmit())
		}
	}
}

func (m *ResultManager) removeStaleResults(admitterCommands chan<- admitterCmdFunc) {
	m.logger.Info(
		"REMOVING stale results",
		"registrySize", len(m.resultsRegistry),
	)

	resultChan := make(chan map[string]bool)
	admitterCommands <- ListAdmitters(resultChan)

	select {
	case <-time.After(10 * time.Second):
		m.logger.Error(fmt.Errorf("timeout waiting for admitters"), "Timeout waiting for admitters")
		return
	case admitters := <-resultChan:
		maps.DeleteFunc(m.resultsRegistry, func(key string, _ result.AsyncAdmissionResult) bool {
			_, ok := admitters[key]
			return !ok
		})
	}

	m.logger.Info("AFTER removal", "registrySize", len(m.resultsRegistry))
}
