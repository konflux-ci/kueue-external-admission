package manager

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/monitoring"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
)

// AdmitterManager is an actor that manages individual admission check admitters.
// It follows the actor pattern by running in a single goroutine and processing
// commands through channels to add, remove, and list admitters.
type AdmitterManager struct {
	logger           logr.Logger
	admitters        map[string]*AdmitterEntry
	admitterCommands chan admitterCmdFunc
	incomingResults  chan<- result.AsyncAdmissionResult
}

// AdmitterEntry represents a registered admitter with its associated metadata.
type AdmitterEntry struct {
	Admitter           admission.Admitter // The admitter implementation
	AdmissionCheckName string             // Name of the admission check
	Cancel             context.CancelFunc // Function to cancel the admitter's context
}

// admitterCmdFunc is a function type that represents commands sent to the AdmitterManager actor.
// Each command function receives the AdmitterManager instance and a context for execution.
type admitterCmdFunc func(admitterManager *AdmitterManager, ctx context.Context)

// NewAdmitterManager creates a new AdmitterManager actor.
//
// Parameters:
//   - logger: The logger instance for structured logging
//   - admitterCommands: Channel for receiving commands to manage admitters
//   - incomingResults: Channel for sending admission results to the ResultManager
//
// Returns:
//   - *AdmitterManager: A new AdmitterManager instance with initialized channels
func NewAdmitterManager(
	logger logr.Logger,
	admitterCommands chan admitterCmdFunc,
	incomingResults chan<- result.AsyncAdmissionResult,
) *AdmitterManager {
	return &AdmitterManager{
		logger:           logger,
		admitters:        make(map[string]*AdmitterEntry),
		admitterCommands: admitterCommands,
		incomingResults:  incomingResults,
	}
}

// Run starts the AdmitterManager actor's main event loop.
// This method processes commands from the admitterCommands channel and manages
// the lifecycle of individual admitters.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
func (m *AdmitterManager) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			m.logger.Info("Stopping ManageAdmitters, context done")
			for admissionCheckName := range m.admitters {
				RemoveAdmitter(admissionCheckName)(m, ctx)
			}
			return
		case cmd := <-m.admitterCommands:
			cmd(m, ctx)
		}
	}
}

// SetAdmitter creates a command function to register a new admitter with the AdmitterManager.
// If an admitter with the same name already exists and is functionally equivalent, it will be skipped.
// If a different admitter exists, it will be replaced.
//
// Parameters:
//   - admissionCheckName: The name of the admission check to register
//   - admitter: The admitter implementation that will handle admission decisions
//
// Returns:
//   - admitterCmdFunc: A command function that can be sent to the AdmitterManager
func SetAdmitter(admissionCheckName string, admitter admission.Admitter) admitterCmdFunc {
	return func(m *AdmitterManager, ctx context.Context) {
		entry, ok := m.admitters[admissionCheckName]
		// Check if the new admitter is functionally equivalent to the existing one
		if ok && entry.Admitter.Equal(admitter) {
			m.logger.Info("Admitter already set, skipping", "admissionCheck", admissionCheckName)
			return
		}

		if ok {
			m.logger.Info("Replacing existing admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
			RemoveAdmitter(admissionCheckName)(m, ctx)
			m.logger.Info("Removed old admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
		}

		subCtx, cancel := context.WithCancel(ctx)
		m.admitters[admissionCheckName] = &AdmitterEntry{
			AdmissionCheckName: admissionCheckName,
			Admitter:           admitter,
			Cancel:             cancel,
		}

		// Start the admitter's sync process in a goroutine
		go func() {
			admissionMetrics := monitoring.NewAdmissionMetrics(admissionCheckName)
			for {
				err := admitter.Sync(subCtx, m.incomingResults)
				if err == nil || err == context.Canceled {
					m.logger.Info("Admitter sync completed", "admissionCheck", admissionCheckName)
					return
				}

				// Record the sync failure in metrics
				admissionMetrics.RecordSyncFailure()

				retryIn := 15 * time.Second
				m.logger.Error(err, "Failed to sync admitter, retrying", "admissionCheck", admissionCheckName, "retryIn", retryIn)

				// Wait before retrying, but respect context cancellation
				select {
				case <-ctx.Done():
					m.logger.Info("Context cancelled, stopping sync retries", "admissionCheck", admissionCheckName)
					return
				case <-time.After(retryIn):
					// Continue to retry
				}
			}
		}()
		// Set initial status to true just to make sure that the metric is set
		monitoring.NewAdmissionMetrics(admissionCheckName).RecordAdmissionCheckStatus(false)
		m.logger.Info("Added admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
	}
}

// RemoveAdmitter creates a command function to remove an existing admitter from the AdmitterManager.
// This will cancel the admitter's context and clean up associated resources.
//
// Parameters:
//   - admissionCheckName: The name of the admission check to remove
//
// Returns:
//   - admitterCmdFunc: A command function that can be sent to the AdmitterManager
func RemoveAdmitter(admissionCheckName string) admitterCmdFunc {
	return func(m *AdmitterManager, ctx context.Context) {
		entry, ok := m.admitters[admissionCheckName]
		if !ok {
			m.logger.Error(fmt.Errorf("admitter not found"), "Admitter not found", "admissionCheck", admissionCheckName)
			return
		}
		entry.Cancel()
		delete(m.admitters, admissionCheckName)
		admissionMetrics := monitoring.NewAdmissionMetrics(admissionCheckName)
		admissionMetrics.DeleteAdmissionCheckStatus()
		m.logger.Info("Removed admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
	}
}

// ListAdmitters creates a command function to retrieve the list of currently registered admitters.
// This is used by the ResultManager to identify which admitters are still active.
//
// Returns:
//   - admitterCmdFunc: A command function that can be sent to the AdmitterManager
//   - A channel that will receive a map of admitter names to their active status
func ListAdmitters() (admitterCmdFunc, <-chan map[string]bool) {
	resultChan := make(chan map[string]bool, 1)
	return func(m *AdmitterManager, ctx context.Context) {
		defer close(resultChan)
		admitters := make(map[string]bool)
		for admissionCheckName := range m.admitters {
			admitters[admissionCheckName] = true
		}
		resultChan <- admitters
	}, resultChan
}
