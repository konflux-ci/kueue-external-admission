package watcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr"
	"github.com/prometheus/alertmanager/api/v2/models"
)

// mockAdmitter is a simple mock implementation of the Admitter interface for testing
type mockAdmitter struct {
	shouldAdmit bool
	details     map[string][]string
	err         error
}

func (m *mockAdmitter) ShouldAdmit(ctx context.Context) (AdmissionResult, error) {
	if m.err != nil {
		return nil, m.err
	}

	builder := NewAdmissionResult()
	if !m.shouldAdmit {
		builder.SetAdmissionDenied()
	}

	for key, values := range m.details {
		builder.AddProviderDetails(key, values)
	}

	return builder.Build(), nil
}

func newMockAdmitter(shouldAdmit bool, details map[string][]string) *mockAdmitter {
	return &mockAdmitter{
		shouldAdmit: shouldAdmit,
		details:     details,
	}
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
	admitter := newMockAdmitter(true, map[string][]string{"test": {"detail1"}})

	service.SetAdmitter("test-key", admitter)

	done := make(chan bool, 10)

	// Test concurrent access
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- true }()

			// Test SetAdmitter
			testAdmitter := newMockAdmitter(true, map[string][]string{"concurrent": {"detail1"}})
			service.SetAdmitter("concurrent-test", testAdmitter)

			// Test retrieving admitter
			retrievedAdmitter, exists := service.GetAdmitter("test-key")
			if !exists || retrievedAdmitter == nil {
				t.Error("Expected to retrieve admitter, got nil or not found")
			}

			service.RemoveAdmitter("concurrent-test")
		}()
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

	// Create mock admitter
	mockAdmitter := newMockAdmitter(true, map[string][]string{})

	// Store as Admitter interface
	service.SetAdmitter("test-key", mockAdmitter)

	// Retrieve as interface
	retrievedAdmitter, exists := service.GetAdmitter("test-key")
	if !exists || retrievedAdmitter == nil {
		t.Error("Expected to retrieve admitter, got nil or not found")
	}

	// Test the ShouldAdmit method through the interface
	result, err := retrievedAdmitter.ShouldAdmit(context.Background())
	if err != nil {
		t.Errorf("Expected no error from ShouldAdmit, got %v", err)
	}
	if !result.ShouldAdmit() {
		t.Error("Expected workload to be admitted (no alerts), but it was denied")
	}
}

func TestAdmissionService_RetrieveMultipleAdmitters(t *testing.T) {
	service, _ := NewAdmissionService(logr.Discard())

	// Create multiple admitters
	admitter1 := newMockAdmitter(true, map[string][]string{"provider1": {"detail1"}})
	admitter2 := newMockAdmitter(false, map[string][]string{"provider2": {"detail2"}})

	// Store admitters
	service.SetAdmitter("key1", admitter1)
	service.SetAdmitter("key2", admitter2)

	// Retrieve both
	retrieved1, exists1 := service.GetAdmitter("key1")
	retrieved2, exists2 := service.GetAdmitter("key2")

	if !exists1 || !exists2 || retrieved1 == nil || retrieved2 == nil {
		t.Error("Expected to retrieve both admitters")
	}

	// Test that they are different instances
	if retrieved1 == retrieved2 {
		t.Error("Expected different admitter instances")
	}
}
