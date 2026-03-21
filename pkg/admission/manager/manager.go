// Package manager implements the actor pattern for managing admission checks.
//
// The actor pattern is used here to ensure thread-safe operations and clear separation of concerns.
// Each manager runs in its own goroutine and manages its own state, communicating with other
// managers through channels. This design provides several benefits:
//
// 1. Thread Safety: Each actor (manager) runs in a single goroutine, eliminating race conditions
// 2. Clear Communication: Actors communicate only through well-defined message channels
// 3. Encapsulation: Each actor encapsulates its own state and behavior
// 4. Scalability: The pattern allows for easy addition of new actors without affecting existing ones
//
// The AdmissionManager orchestrates two sub-managers:
// - AdmitterManager: Manages individual admission check admitters
// - ResultManager: Manages admission results and notifications
//
// Communication flow:
// - External clients send commands to AdmissionManager via method calls
// - AdmissionManager forwards commands to appropriate sub-managers via channels
// - Sub-managers process commands and send results back through channels
// - Results are aggregated and notifications are sent to interested parties
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

// AdmissionManager is the main actor that orchestrates admission check operations.
// It follows the actor pattern by running in a single goroutine and managing its state
// through message passing with sub-managers.
//
// The manager coordinates between:
// - AdmitterManager: Handles individual admission check admitters
// - ResultManager: Manages admission results and change notifications
//
// All communication with sub-managers happens through channels, ensuring thread safety
// and clear separation of concerns.
type AdmissionManager struct {
	logger              logr.Logger
	startTime           time.Time
	admitterCmd         chan admitterCmdFunc             // Channel for sending commands to AdmitterManager actor
	incomingResults     chan result.AsyncAdmissionResult // Channel for receiving admission results from admitters
	resultNotifications chan result.AdmissionResult      // Channel for broadcasting result change notifications
	resultCmd           chan resultCmdFunc               // Channel for sending commands to ResultManager actor
}

// NewManager creates a new AdmissionManager actor.
// This initializes the manager with all necessary channels for communication
// with sub-managers following the actor pattern.
//
// Parameters:
//   - logger: The logger instance for structured logging
//
// Returns:
//   - *AdmissionManager: A new AdmissionManager instance with initialized channels
func NewManager(logger logr.Logger) *AdmissionManager {
	return &AdmissionManager{
		logger:              logger.WithName("manager"),
		incomingResults:     make(chan result.AsyncAdmissionResult),
		admitterCmd:         make(chan admitterCmdFunc),
		resultNotifications: make(chan result.AdmissionResult, 100),
		resultCmd:           make(chan resultCmdFunc),
	}
}

// Start begins the actor pattern execution by launching sub-manager actors.
// This method demonstrates the actor pattern by:
// 1. Creating AdmitterManager and ResultManager actors
// 2. Starting each actor in its own goroutine
// 3. Blocking until the context is cancelled
//
// Each sub-manager runs independently and communicates through the channels
// established during AdmissionManager creation.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//
// Returns:
//   - error: Returns context.Err() when the context is cancelled
func (s *AdmissionManager) Start(ctx context.Context) error {
	s.logger.Info("Starting AdmissionService")
	s.startTime = time.Now()

	// Create and start the AdmitterManager actor
	admitterManager := NewAdmitterManager(
		s.logger.WithName("admitter-manager"),
		s.admitterCmd,
		s.incomingResults,
	)
	go admitterManager.Run(ctx)

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	// Create and start the ResultManager actor
	resultManager := NewResultManager(
		s.logger.WithName("result-manager"),
		s.admitterCmd,
		s.incomingResults,
		s.resultNotifications,
		s.resultCmd,
		ticker.C,
	)
	go resultManager.Run(ctx)

	// Block until context is cancelled - this is the main actor's run loop
	<-ctx.Done()
	s.logger.Info("Stopping AdmissionService, context done")
	// TODO: close channels, wait for sub managers to finish?
	return ctx.Err()
}

// SetAdmitter sends a command to the AdmitterManager actor to register a new admitter.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - admissionCheckName: The name of the admission check to register
//   - admitter: The admitter implementation that will handle admission decisions
//
// Returns:
//   - error: Returns context.Err() if context is cancelled, nil on success
func (s *AdmissionManager) SetAdmitter(ctx context.Context,
	admissionCheckName string, admitter admission.Admitter) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.admitterCmd <- SetAdmitter(admissionCheckName, admitter):
		return nil
	}
}

// RemoveAdmitter sends a command to the AdmitterManager actor to remove an existing admitter.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - admissionCheckName: The name of the admission check to remove
//
// Returns:
//   - error: Returns context.Err() if context is cancelled, nil on success
func (s *AdmissionManager) RemoveAdmitter(ctx context.Context, admissionCheckName string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case s.admitterCmd <- RemoveAdmitter(admissionCheckName):
		return nil
	}
}

// AdmissionResultChanged returns a channel for receiving admission result change notifications.
// This channel is populated by the ResultManager actor when admission results change.
//
// Returns:
//   - <-chan result.AdmissionResult: A read-only channel that receives admission result notifications
func (s *AdmissionManager) AdmissionResultChanged() <-chan result.AdmissionResult {
	return s.resultNotifications
}

// ShouldAdmitWorkload queries the ResultManager actor for current admission results
// and determines if a workload should be admitted based on the specified checks.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - checkNames: List of admission check names to evaluate for the workload
//
// Returns:
//   - result.AggregatedAdmissionResult: The aggregated admission decision with details from all checks
//   - error: Returns context.Err() if context is cancelled, or an error if admission check fails
func (s *AdmissionManager) ShouldAdmitWorkload(
	ctx context.Context, checkNames []string,
) (result.AggregatedAdmissionResult, error) {
	cmd, resultChan := GetSnapshot()
	s.resultCmd <- cmd

	select {
	case <-ctx.Done():
		s.logger.Info("Stopping shouldAdmitWorkload, context done")
		return nil, ctx.Err()
	case snapshot := <-resultChan:
		s.logger.Info("Received results from channel", "results", snapshot, "count", len(snapshot))
		return s.shouldAdmitWorkload(checkNames, snapshot)
	}
}

// shouldAdmitWorkload is the internal method that aggregates admission decisions
// from multiple admitters. It processes the result snapshot received from the
// ResultManager actor and determines the final admission decision.
//
// Parameters:
//   - checkNames: List of admission check names to evaluate for the workload
//   - resultSnapshot: Map of admission check names to their latest async results from the ResultManager
//
// Returns:
//   - result.AggregatedAdmissionResult: The aggregated admission decision with details from all checks
//   - error: Returns an error if any admission check fails during evaluation
func (s *AdmissionManager) shouldAdmitWorkload(
	checkNames []string,
	resultSnapshot map[string]result.AsyncAdmissionResult,
) (result.AggregatedAdmissionResult, error) {
	s.logger.Info("Checking admission for workload", "checks", checkNames)

	builder := result.NewAggregatedAdmissionResultBuilder()
	builder.SetAdmissionDenied()

	// Debug: Check each result in detail
	for checkName, asyncResult := range resultSnapshot {
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

	shouldAdmit := true
	for _, checkName := range checkNames {
		lastResult, exists := resultSnapshot[checkName]
		if !exists || lastResult == (result.AsyncAdmissionResult{}) {
			shouldAdmit = false
			builder.AddProviderDetails(checkName, []string{"No last result found for AdmissionCheck. Denied by default."})
			continue
		}

		err := lastResult.Error
		if err != nil {
			s.logger.Error(err, "Failed to check admission", "check", checkName)
			return nil, fmt.Errorf("failed to check admission for %s: %w", checkName, err)
		}
		shouldAdmit = shouldAdmit && lastResult.AdmissionResult.ShouldAdmit()
		builder.AddProviderDetails(checkName, lastResult.AdmissionResult.Details())

		// Record metrics
		admissionMetrics := monitoring.NewAdmissionMetrics(checkName)
		admissionMetrics.RecordDecision(lastResult.AdmissionResult.ShouldAdmit())
	}

	if shouldAdmit {
		builder.SetAdmissionAllowed()
	} else {
		builder.SetAdmissionDenied()
	}

	finalResult := builder.Build()

	s.logger.Info("Workload admission decision completed",
		"shouldAdmit", finalResult.ShouldAdmit(),
		"providerDetails", finalResult.GetProviderDetails(),
		"checksEvaluated", len(checkNames),
	)

	return finalResult, nil
}
