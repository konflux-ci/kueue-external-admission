package manager

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"time"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/monitoring"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
)

type ResultManager struct {
	logger              logr.Logger
	resultsRegistry     map[string]result.AsyncAdmissionResult
	incomingResults     <-chan result.AsyncAdmissionResult
	resultNotifications chan<- result.AdmissionResult
	resultSnapshot      chan<- map[string]result.AsyncAdmissionResult
	admitterCommands    chan<- admitterCmdFunc
}

func NewResultManager(logger logr.Logger,
	admitterCommands chan<- admitterCmdFunc,
	incomingResults <-chan result.AsyncAdmissionResult,
	resultNotifications chan<- result.AdmissionResult,
	resultSnapshot chan<- map[string]result.AsyncAdmissionResult,
) *ResultManager {
	return &ResultManager{
		logger:              logger,
		resultsRegistry:     make(map[string]result.AsyncAdmissionResult),
		incomingResults:     incomingResults,
		resultNotifications: resultNotifications,
		resultSnapshot:      resultSnapshot,
		admitterCommands:    admitterCommands,
	}
}

func (m *ResultManager) Run(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		m.logger.Info("Select loop iteration", "registrySize", len(m.resultsRegistry))
		select {
		case <-ctx.Done():
			m.logger.Info("Stopping readAsyncAdmissionResults, context done")
			return
		// Clean the local results registry. Remove results for which the admitter is not present.
		case <-ticker.C:
			m.removeStaleResults(m.admitterCommands)
		// Publish the current state of the results registry.
		case m.resultSnapshot <- maps.Clone(m.resultsRegistry):
			m.logger.Info("PUBLISHING results", "registrySize", len(m.resultsRegistry), "results", m.resultsRegistry)
		// Receive new results from the admitters.
		case newResult := <-m.incomingResults:
			m.handleNewResult(newResult)
		}
	}
}

func (m *ResultManager) handleNewResult(newResult result.AsyncAdmissionResult) {
	admissionCheckName := newResult.AdmissionResult.CheckName()
	m.logger.Info(
		"Received async admission result",
		"admissionCheck", admissionCheckName,
		"result", newResult.AdmissionResult.ShouldAdmit(),
		"details", newResult.AdmissionResult.Details(),
		"error", newResult.Error,
	)

	// Report metrics
	admissionMetrics := monitoring.NewAdmissionMetrics(admissionCheckName)
	if newResult.Error != nil {
		m.logger.Error(newResult.Error, "Error in async admission result")
		admissionMetrics.RecordError("admission_check_failed")
	}
	admissionMetrics.RecordAdmissionCheckStatus(newResult.AdmissionResult.ShouldAdmit())

	// Check if there is an old result for this admission check. If not, use a default value.
	lastResult, ok := m.resultsRegistry[admissionCheckName]
	if !ok {
		m.logger.Info("No last sync result found, using default value", "admissionCheck", admissionCheckName)
		lastResult = result.AsyncAdmissionResult{
			AdmissionResult: result.NewAdmissionResultBuilder(admissionCheckName).SetAdmissionDenied().Build(),
		}
	}
	// Update the registry with the new result
	m.resultsRegistry[admissionCheckName] = newResult

	// Send notification if the result changed and there is no error
	changed := !reflect.DeepEqual(newResult, lastResult)
	if changed {
		if newResult.Error == nil {
			m.logger.Info("Sending admission result to the channel", "admissionCheck", admissionCheckName)
			m.resultNotifications <- newResult.AdmissionResult
		} else {
			m.logger.Info("Admission result changed but has error, not notifying",
				"admissionCheck", admissionCheckName,
				"error", newResult.Error)
		}
	} else {
		m.logger.Info("Admission result did not change, not notifying", "admissionCheck", admissionCheckName)
	}
}

func (m *ResultManager) removeStaleResults(admitterCommands chan<- admitterCmdFunc) {
	m.logger.Info(
		"REMOVING stale results",
		"registrySize", len(m.resultsRegistry),
	)

	listAdmitters, resultChan := ListAdmitters()
	admitterCommands <- listAdmitters

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
