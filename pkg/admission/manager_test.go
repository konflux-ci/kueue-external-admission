package admission

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
	. "github.com/onsi/gomega"
)

// mockAdmitter is a simple mock implementation of the Admitter interface for testing
type mockAdmitter struct {
	shouldAdmit bool
	details     map[string][]string
	err         error
	checkName   string
}

func (m *mockAdmitter) Sync(ctx context.Context, asyncAdmissionResults chan<- result.AsyncAdmissionResult) error {
	if m.err != nil {
		return m.err
	}

	// Simulate async behavior by sending a result immediately and then continue monitoring
	go func() {
		// Send initial result
		builder := result.NewAdmissionResultBuilder(m.checkName)
		if !m.shouldAdmit {
			builder.SetAdmissionDenied()
		} else {
			builder.SetAdmissionAllowed()
		}

		for _, values := range m.details {
			builder.AddDetails(values...)
		}

		asyncResult := result.AsyncAdmissionResult{
			AdmissionResult: builder.Build(),
			Error:           m.err,
		}

		select {
		case asyncAdmissionResults <- asyncResult:
		case <-ctx.Done():
			return
		}
	}()

	return nil
}

func newMockAdmitter(checkName string, shouldAdmit bool, details map[string][]string) *mockAdmitter {
	return &mockAdmitter{
		shouldAdmit: shouldAdmit,
		details:     details,
		checkName:   checkName,
	}
}

func TestAdmissionService_Creation(t *testing.T) {
	RegisterTestingT(t)
	service := NewAdmissionService(logr.Discard())
	Expect(service).ToNot(BeNil(), "Expected non-nil AdmissionService")
}

func TestAdmissionService_ConcurrentAccess(t *testing.T) {
	RegisterTestingT(t)
	service := NewAdmissionService(logr.Discard())

	// Start the service
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = service.Start(ctx)
	}()

	// Create a test admitter
	admitter := newMockAdmitter("test-key", true, map[string][]string{"test": {"detail1"}})

	service.SetAdmitter("test-key", admitter)

	done := make(chan bool, 10)

	// Test concurrent access
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- true }()

			// Test SetAdmitter
			testAdmitter := newMockAdmitter("concurrent-test", true, map[string][]string{"concurrent": {"detail1"}})
			t.Log("trying to set admitter")
			service.SetAdmitter("concurrent-test", testAdmitter)
			t.Log("SetAdmitter")

			// Test retrieving admitter
			t.Log("trying to retrieve results")
			results := <-service.publishResults
			t.Log("retrieved admitter")
			Expect(results).ToNot(BeNil(), "Expected non-nil retrieved admitter")

			t.Log("removing admitter")
			service.RemoveAdmitter("concurrent-test")
			t.Log("removed admitter")
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestAdmissionService_InterfaceFlexibility(t *testing.T) {
	RegisterTestingT(t)
	service := NewAdmissionService(logr.Discard())

	// Start the service
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = service.Start(ctx)
	}()

	// Give the service a moment to start
	time.Sleep(100 * time.Millisecond)

	// Create mock admitter
	mockAdmitter := newMockAdmitter("test-key", true, map[string][]string{})

	// Store as Admitter interface
	service.SetAdmitter("test-key", mockAdmitter)

	// Give some time for the admitter to be set and sync
	time.Sleep(500 * time.Millisecond)

	// Retrieve as interface
	results, exists := <-service.publishResults
	Expect(exists).To(BeTrue(), "Expected to find admitter")
	Expect(results).ToNot(BeNil(), "Expected non-nil retrieved admitter")

	// Test the ShouldAdmitWorkload method through the interface
	result, err := service.ShouldAdmitWorkload(ctx, []string{"test-key"})
	Expect(err).ToNot(HaveOccurred(), "Expected no error")
	Expect(result.ShouldAdmit()).To(
		BeTrue(),
		"Expected workload to be admitted",
	)
}

func TestAdmissionService_RetrieveMultipleAdmitters(t *testing.T) {
	RegisterTestingT(t)
	service := NewAdmissionService(logr.Discard())

	// Start the service
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = service.Start(ctx)
	}()

	// Give the service a moment to start
	time.Sleep(100 * time.Millisecond)

	// Create multiple admitters
	admitter1 := newMockAdmitter("key1", true, map[string][]string{"provider1": {"detail1"}})
	admitter2 := newMockAdmitter("key2", false, map[string][]string{"provider2": {"detail2"}})

	// Store admitters
	service.SetAdmitter("key1", admitter1)
	service.SetAdmitter("key2", admitter2)

	// Give some time for the admitters to be set
	time.Sleep(200 * time.Millisecond)

	// Retrieve both
	results1, exists1 := <-service.publishResults
	results2, exists2 := <-service.publishResults

	Expect(exists1).To(BeTrue(), "Expected to find first admitter")
	Expect(exists2).To(BeTrue(), "Expected to find second admitter")
	Expect(results1).ToNot(BeNil(), "Expected non-nil first admitter")
	Expect(results2).ToNot(BeNil(), "Expected non-nil second admitter")

	// Test that they are different instances
	Expect(results1).ToNot(BeIdenticalTo(results2), "Expected different admitter instances")
}
