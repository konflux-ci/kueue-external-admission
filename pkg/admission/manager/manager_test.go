package manager

import (
	"context"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
	. "github.com/onsi/gomega"
)

// mockAdmitter is a simple mock implementation of the Admitter interface for testing
type mockAdmitter struct {
	shouldAdmit bool
	details     []string
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

		builder.AddDetails(m.details...)

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

func (m *mockAdmitter) Equal(other admission.Admitter) bool {
	// Type assertion to check if the other admitter is also a mock admitter
	otherMock, ok := other.(*mockAdmitter)
	if !ok {
		return false
	}

	// Compare the relevant configuration fields
	if m.shouldAdmit != otherMock.shouldAdmit ||
		m.checkName != otherMock.checkName ||
		len(m.details) != len(otherMock.details) {
		return false
	}

	// Compare details slice
	for i, detail := range m.details {
		if detail != otherMock.details[i] {
			return false
		}
	}

	// Compare errors
	if (m.err == nil) != (otherMock.err == nil) {
		return false
	}
	if m.err != nil && otherMock.err != nil {
		return m.err.Error() == otherMock.err.Error()
	}

	return true
}

func newMockAdmitter(checkName string, shouldAdmit bool, details []string) *mockAdmitter {
	return &mockAdmitter{
		shouldAdmit: shouldAdmit,
		details:     details,
		checkName:   checkName,
	}
}

func TestAdmissionService_Creation(t *testing.T) {
	RegisterTestingT(t)
	service := NewManager(logr.Discard(), 1*time.Second)
	Expect(service).ToNot(BeNil(), "Expected non-nil AdmissionService")
}

func TestAdmissionService_ConcurrentAccess(t *testing.T) {
	RegisterTestingT(t)
	service := NewManager(logr.Discard(), 1*time.Second)

	// Start the service
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = service.Start(ctx)
	}()

	// Create a test admitter
	admitter := newMockAdmitter("test-key", true, []string{"detail1"})

	service.SetAdmitter(ctx, "test-key", admitter)

	done := make(chan bool, 10)

	// Test concurrent access
	for i := 0; i < 2; i++ {
		go func() {
			defer func() { done <- true }()

			// Test SetAdmitter
			testAdmitter := newMockAdmitter("concurrent-test", true, []string{"detail1"})
			t.Log("trying to set admitter")
			err := service.SetAdmitter(ctx, "concurrent-test", testAdmitter)
			Expect(err).ToNot(HaveOccurred(), "Failed to set admitter")
			t.Log("SetAdmitter")
			time.Sleep(3 * time.Second)
			// Test retrieving admitter
			t.Log("trying to retrieve results")
			Eventually(func(g Gomega) {
				results, err := service.ShouldAdmitWorkload(ctx, []string{"concurrent-test"})
				g.Expect(err).ToNot(HaveOccurred(), "Expected no error")
				g.Expect(results).ToNot(BeNil(), "Expected non-nil result")
				g.Expect(results.ShouldAdmit()).To(BeTrue(), "Expected workload to be admitted")
				g.Expect(results.GetProviderDetails()["concurrent-test"]).To(Equal([]string{"detail1"}), "Expected 1 detail")
			}, 10*time.Second).Should(Succeed())
			t.Log("retrieved admitter")

			t.Log("removing admitter")
			service.RemoveAdmitter(ctx, "concurrent-test")
			t.Log("removed admitter")
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 2; i++ {
		<-done
	}
}

func TestAdmissionService_RetrieveMultipleAdmitters(t *testing.T) {
	RegisterTestingT(t)
	service := NewManager(logr.Discard(), 1*time.Second)

	// Start the service
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = service.Start(ctx)
	}()

	// Give the service a moment to start
	time.Sleep(100 * time.Millisecond)

	// Create multiple admitters
	admitter1 := newMockAdmitter("key1", true, []string{"detail1"})
	admitter2 := newMockAdmitter("key2", false, []string{"detail2"})

	// Store admitters
	service.SetAdmitter(ctx, "key1", admitter1)
	service.SetAdmitter(ctx, "key2", admitter2)

	// Give some time for the admitters to be set
	time.Sleep(200 * time.Millisecond)

	// Retrieve both
	cmd, resultChan := GetSnapshot()
	service.resultCmd <- cmd
	results1 := <-resultChan

	Expect(results1).ToNot(BeNil(), "Expected non-nil first admitter")
}
