package watcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/alertmanager/api/v2/models"
)

func TestWatcher(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Watcher Suite")
}

func TestAdmissionService_Creation(t *testing.T) {
	service, eventsCh := NewAdmissionService(logr.Discard())
	if service == nil {
		t.Error("Expected non-nil AdmissionService")
	}
	if eventsCh == nil {
		t.Error("Expected non-nil events channel")
	}
}

func TestAdmissionService_ConcurrentAccess(t *testing.T) {
	service, _ := NewAdmissionService(logr.Discard())
	// Create a test admitter
	admitter, err := NewAlertManagerAdmitter("http://test", []string{"test-alert"}, logr.Discard())
	if err != nil {
		t.Errorf("Expected no error creating admitter, got %v", err)
	}

	service.SetAdmitter("test-key", admitter)

	done := make(chan bool, 10)

	// Test concurrent access
	for i := 0; i < 10; i++ {
		go func(i int) {
			defer func() { done <- true }()

			// Simulate concurrent operations
			service.SetAdmitter("key-"+string(rune(i)), admitter)
			_, exists := service.GetAdmitter("test-key")
			if !exists {
				t.Error("Expected to find the admitter during concurrent access")
			}
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestAdmissionService_InterfaceFlexibility(t *testing.T) {
	service, _ := NewAdmissionService(logr.Discard())

	// Create test server that returns empty alerts
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/alerts" || r.URL.Path == "/api/v2/alerts" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if err := json.NewEncoder(w).Encode([]*models.GettableAlert{}); err != nil {
				http.Error(w, "Failed to encode response", http.StatusInternalServerError)
				return
			}
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer testServer.Close()

	// Create AlertManager admitter with test server URL
	alertManagerAdmitter, err := NewAlertManagerAdmitter(testServer.URL, []string{"test-alert"}, logr.Discard())
	if err != nil {
		t.Errorf("Expected no error creating admitter, got %v", err)
	}

	// Add the admitter to the service
	service.SetAdmitter("test-admitter", alertManagerAdmitter)

	// Retrieve using interface
	admitter, exists := service.GetAdmitter("test-admitter")
	if !exists {
		t.Error("Expected to find the admitter")
	}

	if admitter == nil {
		t.Error("Expected non-nil admitter")
	}

	// Verify it implements the interface correctly
	result, err := admitter.ShouldAdmit(context.Background())
	if err != nil {
		t.Errorf("Expected no error from admitter.ShouldAdmit, got %v", err)
	}
	if result == nil {
		t.Error("Expected non-nil admission result")
	}
}

func TestAdmissionService_RetrieveMultipleAdmitters(t *testing.T) {
	service, _ := NewAdmissionService(logr.Discard())

	// Create real admitters instead of MockAdmitter
	admitter1, err := NewAlertManagerAdmitter("http://test1", []string{"alert1"}, logr.Discard())
	if err != nil {
		t.Errorf("Expected no error creating admitter1, got %v", err)
	}
	admitter2, err := NewAlertManagerAdmitter("http://test2", []string{"alert2"}, logr.Discard())
	if err != nil {
		t.Errorf("Expected no error creating admitter2, got %v", err)
	}

	// Test that we can set and retrieve multiple admitters
	service.SetAdmitter("admitter1", admitter1)
	service.SetAdmitter("admitter2", admitter2)

	// Verify both are retrievable
	retrieved1, exists1 := service.GetAdmitter("admitter1")
	retrieved2, exists2 := service.GetAdmitter("admitter2")

	if !exists1 || !exists2 {
		t.Error("Expected both admitters to be retrievable")
	}
	if retrieved1 == nil || retrieved2 == nil {
		t.Error("Expected non-nil admitters")
	}

	// Verify different instances
	if retrieved1 == retrieved2 {
		t.Error("Expected different admitter instances")
	}
}
