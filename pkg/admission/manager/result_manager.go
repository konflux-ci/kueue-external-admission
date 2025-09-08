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

// ResultManager is an actor that manages admission results and notifications.
// It follows the actor pattern by running in a single goroutine and processing
// results from admitters, maintaining a registry of current results, and sending
// notifications when results change.
type ResultManager struct {
	logger              logr.Logger
	resultsRegistry     map[string]result.AsyncAdmissionResult
	incomingResults     <-chan result.AsyncAdmissionResult
	resultNotifications chan<- result.AdmissionResult
	resultCmd           <-chan resultCmdFunc
	admitterCommands    chan<- admitterCmdFunc
	cleanupChan         <-chan time.Time
}

// resultCmdFunc is a function type that represents commands sent to the ResultManager actor.
// Each command function receives the ResultManager instance and a context for execution.
type resultCmdFunc func(m *ResultManager, ctx context.Context)

// NewResultManager creates a new ResultManager actor.
//
// Parameters:
//   - logger: The logger instance for structured logging
//   - admitterCommands: Channel for sending commands to the AdmitterManager
//   - incomingResults: Channel for receiving admission results from admitters
//   - resultNotifications: Channel for sending result change notifications
//   - resultCmd: Channel for receiving commands to manage results
//   - cleanupChan: Channel for receiving cleanup timer signals
//
// Returns:
//   - *ResultManager: A new ResultManager instance with initialized channels
func NewResultManager(logger logr.Logger,
	admitterCommands chan<- admitterCmdFunc,
	incomingResults <-chan result.AsyncAdmissionResult,
	resultNotifications chan<- result.AdmissionResult,
	resultCmd <-chan resultCmdFunc,
	cleanupChan <-chan time.Time,
) *ResultManager {
	return &ResultManager{
		logger:              logger,
		resultsRegistry:     make(map[string]result.AsyncAdmissionResult),
		incomingResults:     incomingResults,
		resultNotifications: resultNotifications,
		resultCmd:           resultCmd,
		admitterCommands:    admitterCommands,
		cleanupChan:         cleanupChan,
	}
}

// Run starts the ResultManager actor's main event loop.
// This method processes incoming results, commands, and cleanup signals.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
func (m *ResultManager) Run(ctx context.Context) {
	for {
		m.logger.Info("Select loop iteration", "registrySize", len(m.resultsRegistry))
		select {
		case <-ctx.Done():
			m.logger.Info("Stopping readAsyncAdmissionResults, context done")
			return
		// Clean the local results registry. Remove results for which the admitter is not present.
		case <-m.cleanupChan:
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

// handleNewResult processes a new admission result from an admitter.
// It updates the results registry and sends notifications if the result has changed.
//
// Parameters:
//   - newResult: The new admission result to process
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
	// Note: Using reflect.DeepEqual here is appropriate as we need to compare
	// the entire AsyncAdmissionResult struct including nested fields
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

// removeStaleResults removes admission results for admitters that are no longer active.
// It queries the AdmitterManager to get the list of active admitters and removes
// results for any admitters that are no longer registered.
//
// Parameters:
//   - admitterCommands: Channel for sending commands to the AdmitterManager
func (m *ResultManager) removeStaleResults(admitterCommands chan<- admitterCmdFunc) {
	m.logger.Info(
		"REMOVING stale results",
		"registrySize", len(m.resultsRegistry),
	)
	initialRegistrySize := len(m.resultsRegistry)
	listAdmitters, admitterChan := ListAdmitters()
	admitterCommands <- listAdmitters

	select {
	case <-time.After(10 * time.Second):
		m.logger.Error(fmt.Errorf("timeout waiting for admitters"), "Timeout waiting for admitters")
		return
	case admitters := <-admitterChan:
		maps.DeleteFunc(m.resultsRegistry, func(key string, _ result.AsyncAdmissionResult) bool {
			_, ok := admitters[key]
			return !ok
		})
	}

	m.logger.Info("AFTER removal", "registrySize", len(m.resultsRegistry), "initialRegistrySize", initialRegistrySize)
}

// GetSnapshot creates a command function to retrieve a snapshot of all current admission results.
// This is used by the AdmissionManager to get the current state of all admission results
// for making admission decisions.
//
// Returns:
//   - resultCmdFunc: A command function that can be sent to the ResultManager
//   - A channel that will receive the results snapshot
func GetSnapshot() (resultCmdFunc, <-chan map[string]result.AsyncAdmissionResult) {
	resultChan := make(chan map[string]result.AsyncAdmissionResult, 1)
	return func(m *ResultManager, ctx context.Context) {
		defer close(resultChan)
		resultChan <- maps.Clone(m.resultsRegistry)
	}, resultChan
}
