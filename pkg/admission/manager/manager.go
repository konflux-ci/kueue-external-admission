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

// AdmissionManager manages Admitters for different AdmissionChecks
type AdmissionManager struct {
	logger              logr.Logger
	startTime           time.Time
	admitterCmd    chan admitterCmdFunc             // Commands to add/remove admitters
	incomingResults     chan result.AsyncAdmissionResult // Admission results from admitters
	resultNotifications chan result.AdmissionResult      // Notifications about result changes
	resultCmd           chan resultCmdFunc               // result commands
}

// NewManager creates a new AdmissionService
func NewManager(logger logr.Logger) *AdmissionManager {
	return &AdmissionManager{
		logger:              logger.WithName("manager"),
		incomingResults:     make(chan result.AsyncAdmissionResult),
		admitterCmd:    make(chan admitterCmdFunc),
		resultNotifications: make(chan result.AdmissionResult, 100),
		resultCmd:           make(chan resultCmdFunc),
	}
}

func (s *AdmissionManager) Start(ctx context.Context) error {
	s.logger.Info("Starting AdmissionService")
	s.startTime = time.Now()

	admitterManager := NewAdmitterManager(
		s.logger.WithName("admitter-manager"),
		s.admitterCmd,
		s.incomingResults,
	)
	go admitterManager.Run(ctx)

	resultManager := NewResultManager(
		s.logger.WithName("result-manager"),
		s.admitterCmd,
		s.incomingResults,
		s.resultNotifications,
		s.resultCmd,
	)
	go resultManager.Run(ctx)

	<-ctx.Done()
	s.logger.Info("Stopping AdmissionService, context done")
	return ctx.Err()
}

func (s *AdmissionManager) SetAdmitter(admissionCheckName string, admitter admission.Admitter) {
	s.admitterCmd <- SetAdmitter(admissionCheckName, admitter)
}

func (s *AdmissionManager) RemoveAdmitter(admissionCheckName string) {
	s.admitterCmd <- RemoveAdmitter(admissionCheckName)
}

func (s *AdmissionManager) AdmissionResultChanged() <-chan result.AdmissionResult {
	return s.resultNotifications
}

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

// ShouldAdmitWorkload aggregates admission decisions from multiple admitters
// it uses the last result from the admitters to determine the admission decision
func (s *AdmissionManager) shouldAdmitWorkload(
	checkNames []string,
	resultSnapshot map[string]result.AsyncAdmissionResult,
) (result.AggregatedAdmissionResult, error) {
	s.logger.Info("Checking admission for workload", "checks", checkNames)

	builder := result.NewAggregatedAdmissionResultBuilder()
	builder.SetAdmissionAllowed()

	// TODO: Add a test for this
	if time.Since(s.startTime) < 30*time.Second {
		builder.SetAdmissionDenied()
		builder.AddProviderDetails("startup check", []string{"Admission checks not loaded yet, rejecting workload"})
		s.logger.Info("Admission checks not loaded yet, rejecting workload")
		return builder.Build(), nil
	}

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

	for _, checkName := range checkNames {
		lastResult, exists := resultSnapshot[checkName]
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
		admissionMetrics := monitoring.NewAdmissionMetrics(checkName)
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
