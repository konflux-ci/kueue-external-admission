package watcher

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

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

// ShouldAdmitWorkload checks all relevant admitters for a workload admission
func (s *AdmissionService) ShouldAdmitWorkload(ctx context.Context, admissionCheckNames []string) AdmissionResult {
	// Create the result to aggregate multiple admission checks
	aggregatedResult := &defaultAdmissionResult{
		shouldAdmit:  true,
		firingAlerts: make(map[string][]string),
		errors:       make(map[string]error),
	}

	// Check each relevant admission check
	for _, checkName := range admissionCheckNames {
		value, exists := s.admitters.Load(checkName)
		if !exists {
			s.logger.Info("No admitter found for AdmissionCheck, allowing admission", "admissionCheck", checkName)
			continue
		}

		admitter, ok := value.(Admitter)
		if !ok {
			s.logger.Error(nil, "Invalid admitter type found", "admissionCheck", checkName)
			continue
		}

		// Get admission result from the admitter
		result := admitter.ShouldAdmit(ctx)

		// Aggregate results
		if !result.ShouldAdmit() {
			aggregatedResult.setAdmissionDenied()

			// Merge firing alerts (prefix with admission check name for clarity)
			for source, alerts := range result.GetFiringAlerts() {
				key := fmt.Sprintf("%s.%s", checkName, source)
				aggregatedResult.addFiringAlerts(key, alerts)
			}

			// Merge errors
			for source, err := range result.GetErrors() {
				key := fmt.Sprintf("%s.%s", checkName, source)
				aggregatedResult.addError(key, err)
			}

			s.logger.Info("AdmissionCheck denied admission",
				"admissionCheck", checkName,
				"firingAlerts", result.GetFiringAlerts(),
				"errors", result.GetErrors())
		}
	}

	if aggregatedResult.ShouldAdmit() {
		s.logger.Info("All AdmissionChecks allow admission", "admissionChecks", admissionCheckNames)
	} else {
		s.logger.Info("Admission denied due to one or more failing checks",
			"admissionChecks", admissionCheckNames,
			"firingAlerts", aggregatedResult.GetFiringAlerts(),
			"errors", aggregatedResult.GetErrors())
	}

	return aggregatedResult
}
