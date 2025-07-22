package watcher

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/go-logr/logr"
)

func TestAdmissionService_Creation(t *testing.T) {
	// Test that we can create an AdmissionService and use it
	service, eventsCh := NewAdmissionService(logr.Discard())
	if service == nil {
		t.Error("Expected service to be created")
	}

	if eventsCh == nil {
		t.Error("Expected events channel to be created")
	}
}

func TestAdmissionService_ConcurrentAccess(t *testing.T) {
	service, _ := NewAdmissionService(logr.Discard())
	admitter := NewAlertManagerAdmitter("http://test", []string{"test-alert"}, logr.Discard())

	done := make(chan bool, 10)

	// Start 10 concurrent goroutines
	for g := 0; g < 10; g++ {
		go func() {
			for i := 0; i < 100; i++ {
				service.SetAdmitter(fmt.Sprintf("check-%d", i%10), admitter)
				time.Sleep(time.Millisecond) // Small delay for concurrency
				service.GetAdmitter(fmt.Sprintf("check-%d", i%10))
				// Note: We no longer expose Range or ListAdmitters to maintain encapsulation
			}
			done <- true
		}()
	}

	// Wait for all 10 goroutines to finish
	for i := 0; i < 10; i++ {
		<-done
	}

	// Test that we can still retrieve admitters after concurrent access
	_, exists := service.GetAdmitter("check-1")
	if !exists {
		t.Error("Expected to find check-1 after concurrent operations")
	}
}

func TestAdmissionService_InterfaceFlexibility(t *testing.T) {
	service, _ := NewAdmissionService(logr.Discard())

	// Create different types of admitters - showing interface flexibility
	alertManagerAdmitter := NewAlertManagerAdmitter("http://alertmanager", []string{"test-alert"}, logr.Discard())

	// Set admitters using the interface (not concrete type)
	service.SetAdmitter("alertmanager-check", alertManagerAdmitter)

	// Retrieve using interface
	admitter, exists := service.GetAdmitter("alertmanager-check")
	if !exists {
		t.Error("Expected to find the admitter")
	}

	if admitter == nil {
		t.Error("Expected non-nil admitter")
	}

	// Verify it implements the interface correctly
	result := admitter.ShouldAdmit(context.Background())
	if result == nil {
		t.Error("Expected non-nil admission result")
	}
}

func TestAdmissionService_RetrieveMultipleAdmitters(t *testing.T) {
	service, _ := NewAdmissionService(logr.Discard())

	// Create real admitters instead of MockAdmitter
	admitter1 := NewAlertManagerAdmitter("http://test1", []string{"alert1"}, logr.Discard())
	admitter2 := NewAlertManagerAdmitter("http://test2", []string{"alert2"}, logr.Discard())

	// Test that we can set and retrieve multiple admitters
	service.SetAdmitter("check-1", admitter1)
	service.SetAdmitter("check-2", admitter2)

	// Verify we can retrieve them
	retrievedAdmitter1, exists1 := service.GetAdmitter("check-1")
	if !exists1 {
		t.Error("Expected to find check-1")
	}
	if retrievedAdmitter1 != admitter1 {
		t.Error("Retrieved admitter1 doesn't match the original")
	}

	retrievedAdmitter2, exists2 := service.GetAdmitter("check-2")
	if !exists2 {
		t.Error("Expected to find check-2")
	}
	if retrievedAdmitter2 != admitter2 {
		t.Error("Retrieved admitter2 doesn't match the original")
	}
}
