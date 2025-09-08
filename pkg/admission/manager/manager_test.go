package manager

import (
	"context"
	"fmt"
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
	service := NewManager(logr.Discard())
	Expect(service).ToNot(BeNil(), "Expected non-nil AdmissionService")
}

func TestAdmissionService_ConcurrentAccess(t *testing.T) {
	RegisterTestingT(t)
	service := NewManager(logr.Discard())

	// Start the service
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = service.Start(ctx)
	}()

	// Create a test admitter
	admitter := newMockAdmitter("test-key", true, []string{"detail1"})

	err := service.SetAdmitter(ctx, "test-key", admitter)
	Expect(err).ToNot(HaveOccurred(), "Failed to set admitter")

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
			err = service.RemoveAdmitter(ctx, "concurrent-test")
			Expect(err).ToNot(HaveOccurred(), "Failed to remove admitter")
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
	service := NewManager(logr.Discard())

	// Start the service
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = service.Start(ctx)
	}()

	// Create multiple admitters
	admitter1 := newMockAdmitter("key1", true, []string{"detail1"})
	admitter2 := newMockAdmitter("key2", false, []string{"detail2"})

	// Store admitters
	var err error
	err = service.SetAdmitter(ctx, "key1", admitter1)
	Expect(err).ToNot(HaveOccurred(), "Failed to set admitter1")
	err = service.SetAdmitter(ctx, "key2", admitter2)
	Expect(err).ToNot(HaveOccurred(), "Failed to set admitter2")

	// Retrieve both
	cmd, resultChan := GetSnapshot()
	service.resultCmd <- cmd
	results1 := <-resultChan

	Expect(results1).ToNot(BeNil(), "Expected non-nil first admitter")
}

func TestAdmissionManager_shouldAdmitWorkload_NoError(t *testing.T) {
	RegisterTestingT(t)
	service := NewManager(logr.Discard())

	tests := []struct {
		name            string
		checkNames      []string
		resultSnapshot  map[string]result.AsyncAdmissionResult
		expectAdmit     bool
		expectedDetails map[string][]string
	}{
		{
			name:       "Happy Path - All checks admit",
			checkNames: []string{"security-check", "resource-check", "policy-check"},
			resultSnapshot: map[string]result.AsyncAdmissionResult{
				"security-check": {
					AdmissionResult: result.NewAdmissionResultBuilder("security-check").
						SetAdmissionAllowed().
						AddDetails("Security check passed").
						Build(),
					Error: nil,
				},
				"resource-check": {
					AdmissionResult: result.NewAdmissionResultBuilder("resource-check").
						SetAdmissionAllowed().
						AddDetails("Resource check passed").
						Build(),
					Error: nil,
				},
				"policy-check": {
					AdmissionResult: result.NewAdmissionResultBuilder("policy-check").
						SetAdmissionAllowed().
						AddDetails("Policy check passed").
						Build(),
					Error: nil,
				},
			},
			expectAdmit: true,
			expectedDetails: map[string][]string{
				"security-check": {"Security check passed"},
				"resource-check": {"Resource check passed"},
				"policy-check":   {"Policy check passed"},
			},
		},
		{
			name:       "Mixed Results - Some deny",
			checkNames: []string{"check1", "check2", "check3"},
			resultSnapshot: map[string]result.AsyncAdmissionResult{
				"check1": {
					AdmissionResult: result.NewAdmissionResultBuilder("check1").
						SetAdmissionAllowed().
						AddDetails("Check 1 passed").
						Build(),
					Error: nil,
				},
				"check2": {
					AdmissionResult: result.NewAdmissionResultBuilder("check2").
						SetAdmissionDenied().
						AddDetails("Check 2 failed", "Resource quota exceeded").
						Build(),
					Error: nil,
				},
				"check3": {
					AdmissionResult: result.NewAdmissionResultBuilder("check3").
						SetAdmissionAllowed().
						AddDetails("Check 3 passed").
						Build(),
					Error: nil,
				},
			},
			expectAdmit: false,
			expectedDetails: map[string][]string{
				"check1": {"Check 1 passed"},
				"check2": {"Check 2 failed", "Resource quota exceeded"},
				"check3": {"Check 3 passed"},
			},
		},
		{
			name:       "Missing Check",
			checkNames: []string{"existing-check", "missing-check"},
			resultSnapshot: map[string]result.AsyncAdmissionResult{
				"existing-check": {
					AdmissionResult: result.NewAdmissionResultBuilder("existing-check").
						SetAdmissionAllowed().
						AddDetails("Existing check passed").
						Build(),
					Error: nil,
				},
			},
			expectAdmit: false,
			expectedDetails: map[string][]string{
				"existing-check": {"Existing check passed"},
				"missing-check":  {"No last result found for AdmissionCheck. Denied by default."},
			},
		},
		{
			name:       "Empty Result",
			checkNames: []string{"test-check"},
			resultSnapshot: map[string]result.AsyncAdmissionResult{
				"test-check": {}, // Empty AsyncAdmissionResult
			},
			expectAdmit: false,
			expectedDetails: map[string][]string{
				"test-check": {"No last result found for AdmissionCheck. Denied by default."},
			},
		},
		{
			name:       "Nil AdmissionResult",
			checkNames: []string{"test-check"},
			resultSnapshot: map[string]result.AsyncAdmissionResult{
				"test-check": {
					AdmissionResult: nil,
					Error:           nil,
				},
			},
			expectAdmit: false,
			expectedDetails: map[string][]string{
				"test-check": {"No last result found for AdmissionCheck. Denied by default."},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RegisterTestingT(t)

			// Test shouldAdmitWorkload
			finalResult, err := service.shouldAdmitWorkload(tt.checkNames, tt.resultSnapshot)

			// Assertions - No error expected
			Expect(err).ToNot(HaveOccurred(), "Expected no error for test case: %s", tt.name)
			Expect(finalResult).ToNot(BeNil(), "Expected non-nil result for test case: %s", tt.name)
			Expect(finalResult.ShouldAdmit()).To(
				Equal(tt.expectAdmit),
				"Expected ShouldAdmit() to be %v for test case: %s",
				tt.expectAdmit,
				tt.name,
			)

			if tt.expectedDetails != nil {
				Expect(finalResult.GetProviderDetails()).To(HaveLen(len(tt.expectedDetails)),
					"Expected correct number of provider details for test case: %s",
					tt.name,
				)
				for checkName, expectedDetails := range tt.expectedDetails {
					Expect(finalResult.GetProviderDetails()[checkName]).To(
						Equal(expectedDetails),
						"Expected correct details for %s in test case: %s",
						checkName,
						tt.name,
					)
				}
			}
		})
	}
}

func TestAdmissionManager_shouldAdmitWorkload_WithError(t *testing.T) {
	RegisterTestingT(t)
	service := NewManager(logr.Discard())

	tests := []struct {
		name           string
		checkNames     []string
		resultSnapshot map[string]result.AsyncAdmissionResult
		errorContains  []string
	}{
		{
			name:       "Network timeout error",
			checkNames: []string{"working-check", "failing-check"},
			resultSnapshot: map[string]result.AsyncAdmissionResult{
				"working-check": {
					AdmissionResult: result.NewAdmissionResultBuilder("working-check").
						SetAdmissionAllowed().
						AddDetails("Working check passed").
						Build(),
					Error: nil,
				},
				"failing-check": {
					AdmissionResult: nil,
					Error:           fmt.Errorf("network timeout: unable to reach admission service"),
				},
			},
			errorContains: []string{"failed to check admission for failing-check", "network timeout"},
		},
		{
			name:       "Database connection error",
			checkNames: []string{"check1"},
			resultSnapshot: map[string]result.AsyncAdmissionResult{
				"check1": {
					AdmissionResult: nil,
					Error:           fmt.Errorf("database connection failed: connection refused"),
				},
			},
			errorContains: []string{"failed to check admission for check1", "database connection failed"},
		},
		{
			name:       "Service unavailable error",
			checkNames: []string{"external-service-check"},
			resultSnapshot: map[string]result.AsyncAdmissionResult{
				"external-service-check": {
					AdmissionResult: nil,
					Error:           fmt.Errorf("service unavailable: 503 Service Unavailable"),
				},
			},
			errorContains: []string{"failed to check admission for external-service-check", "service unavailable"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RegisterTestingT(t)

			// Test shouldAdmitWorkload
			finalResult, err := service.shouldAdmitWorkload(tt.checkNames, tt.resultSnapshot)

			// Assertions - Error expected
			Expect(err).To(HaveOccurred(), "Expected error for test case: %s", tt.name)
			if len(tt.errorContains) > 0 {
				for _, expectedSubstring := range tt.errorContains {
					Expect(err.Error()).To(
						ContainSubstring(expectedSubstring),
						"Expected error message to contain '%s' for test case: %s",
						expectedSubstring,
						tt.name,
					)
				}
			}
			Expect(finalResult).To(BeNil(), "Expected nil result when error occurs for test case: %s", tt.name)
		})
	}
}
