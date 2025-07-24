package watcher

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// Admitter determines whether admission should be allowed
type Admitter interface {
	ShouldAdmit(context.Context) (AdmissionResult, error)
}

// AdmissionService manages Admitters for different AdmissionChecks
// Uses sync.Map internally but exposes only type-safe wrapper methods
// This ensures consistent logging and proper type safety
type AdmissionService struct {
	admitters     sync.Map // private sync.Map - hides direct access to Store/Load/Delete/Range
	logger        logr.Logger
	eventsChannel chan<- event.GenericEvent // Channel for notifying about alert state changes
}

// NewAdmissionService creates a new AdmissionService
// The internal sync.Map is ready to use without explicit initialization
func NewAdmissionService(logger logr.Logger) (*AdmissionService, <-chan event.GenericEvent) {
	eventsCh := make(chan event.GenericEvent)

	return &AdmissionService{
		// sync.Map requires no initialization - zero value is ready to use
		logger:        logger,
		eventsChannel: eventsCh,
	}, eventsCh
}

// SetAdmitter sets or updates an Admitter for a given AdmissionCheck
func (s *AdmissionService) SetAdmitter(admissionCheckName string, admitter Admitter) {
	s.admitters.Store(admissionCheckName, admitter)
	s.logger.Info("Set admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
}

// RemoveAdmitter removes the Admitter for a given AdmissionCheck
func (s *AdmissionService) RemoveAdmitter(admissionCheckName string) {
	s.admitters.Delete(admissionCheckName)
	s.logger.Info("Removed admitter for AdmissionCheck", "admissionCheck", admissionCheckName)
}

// GetAdmitter gets the Admitter for a given AdmissionCheck
func (s *AdmissionService) GetAdmitter(admissionCheckName string) (Admitter, bool) {
	value, exists := s.admitters.Load(admissionCheckName)
	if !exists {
		return nil, false
	}
	admitter, ok := value.(Admitter)
	return admitter, ok
}

// ShouldAdmitWorkload aggregates admission decisions from multiple admitters
func (s *AdmissionService) ShouldAdmitWorkload(ctx context.Context, checkNames []string) (AdmissionResult, error) {
	s.logger.V(1).Info("Checking admission for workload", "checks", checkNames)

	builder := NewAdmissionResult()
	hasAnyCheck := false

	for _, checkName := range checkNames {
		if admitter, exists := s.GetAdmitter(checkName); exists {
			hasAnyCheck = true
			result, err := admitter.ShouldAdmit(ctx)
			if err != nil {
				s.logger.Error(err, "Failed to check admission", "check", checkName)
				return nil, fmt.Errorf("failed to check admission for %s: %w", checkName, err)
			}

			if !result.ShouldAdmit() {
				builder.SetAdmissionDenied()
			}

			// Aggregate provider details from all checks
			for _, details := range result.GetProviderDetails() {
				builder.AddProviderDetails(checkName, details)
			}
		}
	}

	// If no admission checks were found, allow admission
	if !hasAnyCheck {
		s.logger.V(1).Info("No admission checks found, allowing admission")
		builder.SetAdmissionAllowed()
	}

	finalResult := builder.Build()

	s.logger.V(1).Info("Workload admission decision completed",
		"shouldAdmit", finalResult.ShouldAdmit(),
		"providerDetails", finalResult.GetProviderDetails(),
		"checksEvaluated", len(checkNames),
	)

	return finalResult, nil
}
