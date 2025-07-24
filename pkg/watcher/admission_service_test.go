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

	konfluxciv1alpha1 "github.com/konflux-ci/kueue-external-admission/api/konflux-ci.dev/v1alpha1"
)

// createTestAlertManagerConfig creates a test AlertManagerProviderConfig for testing
func createTestAlertManagerConfig(url string, alertNames []string) *konfluxciv1alpha1.AlertManagerProviderConfig {
	return &konfluxciv1alpha1.AlertManagerProviderConfig{
		Connection: konfluxciv1alpha1.AlertManagerConnectionConfig{
			URL: url,
		},
		AlertFilters: []konfluxciv1alpha1.AlertFiltersConfig{
			{
				AlertNames: alertNames,
			},
		},
	}
}

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
	admitter, err := NewAlertManagerAdmitter(
		createTestAlertManagerConfig("http://test", []string{"test-alert"}),
		logr.Discard(),
	)
	if err != nil {
		t.Errorf("Expected no error creating admitter, got %v", err)
	}

	service.SetAdmitter("test-key", admitter)

	done := make(chan bool, 10)

	// Test concurrent access
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- true }()

			// Test SetAdmitter
			testAdmitter, err := NewAlertManagerAdmitter(
				createTestAlertManagerConfig("http://test-concurrent", []string{"test-alert"}),
				logr.Discard(),
			)
			if err != nil {
				t.Errorf("Expected no error creating test admitter, got %v", err)
				return
			}

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

	// Create AlertManager admitter with test server URL
	alertManagerAdmitter, err := NewAlertManagerAdmitter(
		createTestAlertManagerConfig(testServer.URL, []string{"test-alert"}),
		logr.Discard(),
	)
	if err != nil {
		t.Errorf("Expected no error creating AlertManager admitter, got %v", err)
	}

	// Store as Admitter interface
	service.SetAdmitter("test-key", alertManagerAdmitter)

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
	admitter1, err := NewAlertManagerAdmitter(
		createTestAlertManagerConfig("http://test1", []string{"alert1"}),
		logr.Discard(),
	)
	if err != nil {
		t.Errorf("Expected no error creating admitter1, got %v", err)
	}
	admitter2, err := NewAlertManagerAdmitter(
		createTestAlertManagerConfig("http://test2", []string{"alert2"}),
		logr.Discard(),
	)
	if err != nil {
		t.Errorf("Expected no error creating admitter2, got %v", err)
	}

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
