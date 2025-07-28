package admission

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
	. "github.com/onsi/gomega"
	"github.com/prometheus/alertmanager/api/v2/models"
)

// mockAdmitter is a simple mock implementation of the Admitter interface for testing
type mockAdmitter struct {
	shouldAdmit bool
	details     map[string][]string
	err         error
}

func (m *mockAdmitter) ShouldAdmit(ctx context.Context) (result.AggregatedAdmissionResult, error) {
	if m.err != nil {
		return nil, m.err
	}

	builder := result.NewAggregatedAdmissionResultBuilder()
	if !m.shouldAdmit {
		builder.SetAdmissionDenied()
	}

	for key, values := range m.details {
		builder.AddProviderDetails(key, values)
	}

	return builder.Build(), nil
}

func (m *mockAdmitter) Sync(ctx context.Context, asyncAdmissionResults chan<- result.AsyncAdmissionResult) error {
	return nil
}

func newMockAdmitter(shouldAdmit bool, details map[string][]string) *mockAdmitter {
	return &mockAdmitter{
		shouldAdmit: shouldAdmit,
		details:     details,
	}
}

func TestAdmissionService_Creation(t *testing.T) {
	RegisterTestingT(t)
	service, eventsCh := NewAdmissionService(logr.Discard())
	Expect(service).ToNot(BeNil(), "Expected non-nil AdmissionService")
	Expect(eventsCh).ToNot(BeNil(), "Expected non-nil events channel")
}

func TestAdmissionService_ConcurrentAccess(t *testing.T) {
	RegisterTestingT(t)
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
			retrievedAdmitter, exists := service.getAdmitterEntry("test-key")
			Expect(exists).To(BeTrue(), "Expected to find admitter")
			Expect(retrievedAdmitter).ToNot(BeNil(), "Expected non-nil retrieved admitter")

			service.RemoveAdmitter("concurrent-test")
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestAdmissionService_InterfaceFlexibility(t *testing.T) {
	RegisterTestingT(t)
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
	retrievedAdmitter, exists := service.getAdmitterEntry("test-key")
	Expect(exists).To(BeTrue(), "Expected to find admitter")
	Expect(retrievedAdmitter).ToNot(BeNil(), "Expected non-nil retrieved admitter")

	// Test the ShouldAdmit method through the interface
	Expect(retrievedAdmitter.LastResult.AdmissionResult.ShouldAdmit()).To(
		BeTrue(),
		"Expected workload to be admitted (no alerts)",
	)
}

func TestAdmissionService_RetrieveMultipleAdmitters(t *testing.T) {
	RegisterTestingT(t)
	service, _ := NewAdmissionService(logr.Discard())

	// Create multiple admitters
	admitter1 := newMockAdmitter(true, map[string][]string{"provider1": {"detail1"}})
	admitter2 := newMockAdmitter(false, map[string][]string{"provider2": {"detail2"}})

	// Store admitters
	service.SetAdmitter("key1", admitter1)
	service.SetAdmitter("key2", admitter2)

	// Retrieve both
	retrieved1, exists1 := service.getAdmitterEntry("key1")
	retrieved2, exists2 := service.getAdmitterEntry("key2")

	Expect(exists1).To(BeTrue(), "Expected to find first admitter")
	Expect(exists2).To(BeTrue(), "Expected to find second admitter")
	Expect(retrieved1).ToNot(BeNil(), "Expected non-nil first admitter")
	Expect(retrieved2).ToNot(BeNil(), "Expected non-nil second admitter")

	// Test that they are different instances
	Expect(retrieved1).ToNot(BeIdenticalTo(retrieved2), "Expected different admitter instances")
}
