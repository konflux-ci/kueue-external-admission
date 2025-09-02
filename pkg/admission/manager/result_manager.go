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
	resultCmd           <-chan resultCmdFunc
	admitterCommands    chan<- admitterCmdFunc
	cleanupInterval     time.Duration
}

type resultCmdFunc func(m *ResultManager, ctx context.Context)

func NewResultManager(logger logr.Logger,
	admitterCommands chan<- admitterCmdFunc,
	incomingResults <-chan result.AsyncAdmissionResult,
	resultNotifications chan<- result.AdmissionResult,
	resultCmd <-chan resultCmdFunc,
	cleanupInterval time.Duration,
) *ResultManager {
	return &ResultManager{
		logger:              logger,
		resultsRegistry:     make(map[string]result.AsyncAdmissionResult),
		incomingResults:     incomingResults,
		resultNotifications: resultNotifications,
		resultCmd:           resultCmd,
		admitterCommands:    admitterCommands,
		cleanupInterval:     cleanupInterval,
	}
}

func (m *ResultManager) Run(ctx context.Context) {
	ticker := time.NewTicker(m.cleanupInterval)
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
		// Run a command.
		case cmd := <-m.resultCmd:
			cmd(m, ctx)
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
			select {
			case m.resultNotifications <- newResult.AdmissionResult:
			default:
				m.logger.Error(fmt.Errorf("timeout waiting for result notifications"), "Timeout waiting for result notifications")
			}
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
	initialRegistrySize := len(m.resultsRegistry)
	listAdmitters, getAdmitters := ListAdmitters()
	admitterCommands <- listAdmitters

	select {
	case <-time.After(10 * time.Second):
		m.logger.Error(fmt.Errorf("timeout waiting for admitters"), "Timeout waiting for admitters")
		return
	case admitters := <-getAdmitters:
		maps.DeleteFunc(m.resultsRegistry, func(key string, _ result.AsyncAdmissionResult) bool {
			_, ok := admitters[key]
			return !ok
		})
	}

	m.logger.Info("AFTER removal", "registrySize", len(m.resultsRegistry), "initialRegistrySize", initialRegistrySize)
}

func GetSnapshot() (resultCmdFunc, <-chan map[string]result.AsyncAdmissionResult) {
	resultChan := make(chan map[string]result.AsyncAdmissionResult, 1)
	return func(m *ResultManager, ctx context.Context) {
		defer close(resultChan)
		resultChan <- maps.Clone(m.resultsRegistry)
	}, resultChan
}
