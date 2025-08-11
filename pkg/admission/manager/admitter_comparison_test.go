package manager

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	konfluxciv1alpha1 "github.com/konflux-ci/kueue-external-admission/api/konflux-ci.dev/v1alpha1"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/factory"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
)

func TestAdmitterComparison_ReflectDeepEqual(t *testing.T) {
	RegisterTestingT(t)

	// Test with simple mock admitters
	t.Run("MockAdmitters", func(t *testing.T) {
		RegisterTestingT(t)

		// Same mock admitters should be equal
		mock1 := newMockAdmitterForAdmitterManager()
		mock2 := newMockAdmitterForAdmitterManager()

		Expect(mock1.Equal(mock2)).To(BeTrue(), "Expected identical mock admitters to be equal")
		Expect(reflect.DeepEqual(mock1, mock2)).To(BeTrue(), "Expected identical mock admitters to be equal with DeepEqual too")

		// Mock admitters with different state should not be equal
		mock3 := newMockAdmitterForAdmitterManager()
		mock3.syncCalled = true // Different state

		// The Equal method should ignore runtime state like syncCalled
		Expect(mock1.Equal(mock3)).To(BeTrue(), "Expected mock admitters to be equal despite different runtime state")
		Expect(reflect.DeepEqual(mock1, mock3)).To(BeFalse(), "Expected mock admitters with different state to not be equal with DeepEqual")

		// Mock admitters with different errors should not be equal
		mock4 := newMockAdmitterWithError(fmt.Errorf("test error"))
		mock5 := newMockAdmitterWithError(fmt.Errorf("different error"))

		Expect(mock4.Equal(mock5)).To(BeFalse(), "Expected mock admitters with different errors to not be equal")
		Expect(reflect.DeepEqual(mock4, mock5)).To(BeFalse(), "Expected mock admitters with different errors to not be equal with DeepEqual")

		// Same error message should be equal with Equal() but different error instances won't be equal with DeepEqual
		sameError := fmt.Errorf("same error")
		mock6 := newMockAdmitterWithError(sameError)
		mock7 := newMockAdmitterWithError(sameError)

		Expect(mock6.Equal(mock7)).To(BeTrue(), "Expected mock admitters with same error instance to be equal")
		Expect(reflect.DeepEqual(mock6, mock7)).To(BeTrue(), "Expected mock admitters with same error instance to be equal with DeepEqual too")

		// Different error instances with same message should be equal with Equal()
		mock8 := newMockAdmitterWithError(fmt.Errorf("same message"))
		mock9 := newMockAdmitterWithError(fmt.Errorf("same message"))

		Expect(mock8.Equal(mock9)).To(BeTrue(), "Expected mock admitters with same error message to be equal using Equal()")
		// Note: reflect.DeepEqual behavior with errors can be unpredictable, so we focus on our Equal() method
	})

	t.Run("RealAlertManagerAdmitters", func(t *testing.T) {
		RegisterTestingT(t)

		// Create identical AlertManager configurations
		config1 := &konfluxciv1alpha1.AlertManagerProviderConfig{
			Connection: konfluxciv1alpha1.AlertManagerConnectionConfig{
				URL: "http://alertmanager:9093",
			},
			AlertFilters: []konfluxciv1alpha1.AlertFiltersConfig{
				{
					AlertNames: []string{"alert1", "alert2"},
				},
			},
			CheckTTL: &metav1.Duration{Duration: 30 * time.Second},
		}

		config2 := &konfluxciv1alpha1.AlertManagerProviderConfig{
			Connection: konfluxciv1alpha1.AlertManagerConnectionConfig{
				URL: "http://alertmanager:9093",
			},
			AlertFilters: []konfluxciv1alpha1.AlertFiltersConfig{
				{
					AlertNames: []string{"alert1", "alert2"},
				},
			},
			CheckTTL: &metav1.Duration{Duration: 30 * time.Second},
		}

		// Create admitters with identical configurations
		factory := factory.NewFactory()

		externalConfig1 := &konfluxciv1alpha1.ExternalAdmissionConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "test-config"},
			Spec: konfluxciv1alpha1.ExternalAdmissionConfigSpec{
				Provider: konfluxciv1alpha1.ProviderConfig{
					AlertManager: config1,
				},
			},
		}

		externalConfig2 := &konfluxciv1alpha1.ExternalAdmissionConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "test-config"},
			Spec: konfluxciv1alpha1.ExternalAdmissionConfigSpec{
				Provider: konfluxciv1alpha1.ProviderConfig{
					AlertManager: config2,
				},
			},
		}

		admitter1, err1 := factory.NewAdmitter(externalConfig1, logr.Discard(), "test-check")
		Expect(err1).ToNot(HaveOccurred())

		admitter2, err2 := factory.NewAdmitter(externalConfig2, logr.Discard(), "test-check")
		Expect(err2).ToNot(HaveOccurred())

		// Test if identical configurations create equal admitters
		isEqualWithEqual := admitter1.Equal(admitter2)
		isEqualWithDeepEqual := reflect.DeepEqual(admitter1, admitter2)
		t.Logf("AlertManager admitters with identical configs - Equal(): %v, DeepEqual(): %v", isEqualWithEqual, isEqualWithDeepEqual)

		// The new Equal method should work correctly for identical configurations
		Expect(isEqualWithEqual).To(BeTrue(), "AlertManager admitters with identical configs should be equal using Equal() method")
		// DeepEqual should still fail due to internal state differences
		Expect(isEqualWithDeepEqual).To(BeFalse(), "AlertManager admitters with identical configs should NOT be equal with DeepEqual due to internal state")
	})

	t.Run("SameInstanceComparison", func(t *testing.T) {
		RegisterTestingT(t)

		// Same instance should always be equal to itself
		mock := newMockAdmitterForAdmitterManager()
		Expect(mock.Equal(mock)).To(BeTrue(), "Expected same instance to be equal to itself using Equal()")
		Expect(reflect.DeepEqual(mock, mock)).To(BeTrue(), "Expected same instance to be equal to itself using DeepEqual")

		// Test with real admitter
		factory := factory.NewFactory()
		config := &konfluxciv1alpha1.ExternalAdmissionConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "test-config"},
			Spec: konfluxciv1alpha1.ExternalAdmissionConfigSpec{
				Provider: konfluxciv1alpha1.ProviderConfig{
					AlertManager: &konfluxciv1alpha1.AlertManagerProviderConfig{
						Connection: konfluxciv1alpha1.AlertManagerConnectionConfig{
							URL: "http://alertmanager:9093",
						},
						AlertFilters: []konfluxciv1alpha1.AlertFiltersConfig{
							{AlertNames: []string{"alert1"}},
						},
						CheckTTL: &metav1.Duration{Duration: 30 * time.Second},
					},
				},
			},
		}

		admitter, err := factory.NewAdmitter(config, logr.Discard(), "test-check")
		Expect(err).ToNot(HaveOccurred())

		Expect(admitter.Equal(admitter)).To(BeTrue(), "Expected same admitter instance to be equal to itself using Equal()")
		Expect(reflect.DeepEqual(admitter, admitter)).To(BeTrue(), "Expected same admitter instance to be equal to itself using DeepEqual")
	})
}

func TestAdmitterComparison_ProblemsWithDeepEqual(t *testing.T) {
	RegisterTestingT(t)

	t.Run("EqualMethodSolvesProblem", func(t *testing.T) {
		RegisterTestingT(t)

		// This test demonstrates that the new Equal method solves the problem
		// where identical configurations would always be replaced

		factory := factory.NewFactory()
		config := &konfluxciv1alpha1.ExternalAdmissionConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "test-config"},
			Spec: konfluxciv1alpha1.ExternalAdmissionConfigSpec{
				Provider: konfluxciv1alpha1.ProviderConfig{
					AlertManager: &konfluxciv1alpha1.AlertManagerProviderConfig{
						Connection: konfluxciv1alpha1.AlertManagerConnectionConfig{
							URL: "http://alertmanager:9093",
						},
						AlertFilters: []konfluxciv1alpha1.AlertFiltersConfig{
							{AlertNames: []string{"alert1"}},
						},
						CheckTTL: &metav1.Duration{Duration: 30 * time.Second},
					},
				},
			},
		}

		// Create two admitters with identical configurations
		admitter1, err1 := factory.NewAdmitter(config, logr.Discard(), "test-check")
		Expect(err1).ToNot(HaveOccurred())

		admitter2, err2 := factory.NewAdmitter(config, logr.Discard(), "test-check")
		Expect(err2).ToNot(HaveOccurred())

		// The new Equal method should recognize they are functionally equivalent
		Expect(admitter1.Equal(admitter2)).To(BeTrue(),
			"Solution: Equal method recognizes functionally equivalent admitters")

		// DeepEqual will still fail due to internal state differences
		Expect(reflect.DeepEqual(admitter1, admitter2)).To(BeFalse(),
			"DeepEqual still fails due to internal state differences")

		// Now the SetAdmitter logic will:
		// 1. Skip replacement when configurations are identical
		// 2. Avoid unnecessary goroutine churn
		// 3. Optimize redundant updates

		t.Logf("Solution: Equal method allows skipping redundant updates with identical configs")
		t.Logf("This reduces goroutine churn and improves performance")
	})
}

func TestAdmitterComparison_AlternativeApproaches(t *testing.T) {
	RegisterTestingT(t)

	t.Run("PointerComparison", func(t *testing.T) {
		RegisterTestingT(t)

		// Pointer comparison - much simpler and more reliable
		mock1 := newMockAdmitterForAdmitterManager()
		mock2 := newMockAdmitterForAdmitterManager()

		// Different instances should have different pointers
		Expect(mock1 == mock2).To(BeFalse(), "Expected different instances to have different pointers")

		// Same instance should have same pointer
		mock3 := mock1
		Expect(mock1 == mock3).To(BeTrue(), "Expected same instance to have same pointer")

		// Pointer comparison is simple, fast, and reliable
		// It only skips replacement when the exact same instance is provided
		// This is probably the safest approach
	})

	t.Run("ConfigurationBasedComparison", func(t *testing.T) {
		RegisterTestingT(t)

		// This would require implementing a custom comparison method
		// that compares the relevant configuration fields only

		// For example, for AlertManager admitters, we might want to compare:
		// - URL
		// - AlertFilters
		// - CheckTTL
		// But NOT internal state like HTTP clients, loggers, etc.

		// This test demonstrates what a more robust comparison might look like
		config1 := &konfluxciv1alpha1.AlertManagerProviderConfig{
			Connection: konfluxciv1alpha1.AlertManagerConnectionConfig{
				URL: "http://alertmanager:9093",
			},
			AlertFilters: []konfluxciv1alpha1.AlertFiltersConfig{
				{AlertNames: []string{"alert1"}},
			},
			CheckTTL: &metav1.Duration{Duration: 30 * time.Second},
		}

		config2 := &konfluxciv1alpha1.AlertManagerProviderConfig{
			Connection: konfluxciv1alpha1.AlertManagerConnectionConfig{
				URL: "http://alertmanager:9093",
			},
			AlertFilters: []konfluxciv1alpha1.AlertFiltersConfig{
				{AlertNames: []string{"alert1"}},
			},
			CheckTTL: &metav1.Duration{Duration: 30 * time.Second},
		}

		// These configurations should be considered equal
		Expect(reflect.DeepEqual(config1, config2)).To(BeTrue(), "Expected identical configurations to be equal")

		// Different URL should not be equal
		config3 := &konfluxciv1alpha1.AlertManagerProviderConfig{
			Connection: konfluxciv1alpha1.AlertManagerConnectionConfig{
				URL: "http://different:9093",
			},
			AlertFilters: []konfluxciv1alpha1.AlertFiltersConfig{
				{AlertNames: []string{"alert1"}},
			},
			CheckTTL: &metav1.Duration{Duration: 30 * time.Second},
		}

		Expect(reflect.DeepEqual(config1, config3)).To(BeFalse(), "Expected different URLs to not be equal")

		// This approach would require:
		// 1. Adding a method to compare configurations
		// 2. Storing the original config alongside the admitter
		// 3. Comparing configs instead of admitter instances
	})
}

func TestSetAdmitter_ComparisonBehavior(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t.Run("SameInstanceSkipped", func(t *testing.T) {
		RegisterTestingT(t)

		// Use the same admitter instance - should be skipped
		mockAdmitter := newMockAdmitterForAdmitterManager()

		// Add admitter first time
		setCmd1 := SetAdmitter("test-check", mockAdmitter)
		setCmd1(manager, ctx)

		Expect(len(manager.admitters)).To(Equal(1))
		originalCallCount := mockAdmitter.syncCallCount

		// Add same admitter instance again - should be skipped
		setCmd2 := SetAdmitter("test-check", mockAdmitter)
		setCmd2(manager, ctx)

		// Should still be 1 admitter and sync should not be called again
		Expect(len(manager.admitters)).To(Equal(1))
		Expect(mockAdmitter.syncCallCount).To(Equal(originalCallCount), "Expected Sync not to be called again for same instance")
	})

	t.Run("DifferentInstancesReplaced", func(t *testing.T) {
		RegisterTestingT(t)

		// Use different admitter instances - should be replaced
		mockAdmitter1 := newMockAdmitterForAdmitterManager()
		mockAdmitter2 := newMockAdmitterForAdmitterManager()

		// Add first admitter
		setCmd1 := SetAdmitter("test-check-2", mockAdmitter1)
		setCmd1(manager, ctx)

		Expect(len(manager.admitters)).To(Equal(2)) // We already have one from previous test

		// Add different admitter instance - should replace
		setCmd2 := SetAdmitter("test-check-2", mockAdmitter2)
		setCmd2(manager, ctx)

		// Should still be 2 admitters total, but the second one should be replaced
		Expect(len(manager.admitters)).To(Equal(2))
		Expect(manager.admitters["test-check-2"].Admitter).To(Equal(mockAdmitter2), "Expected new admitter to replace old one")
	})

	t.Run("RealAdmittersWithIdenticalConfigSkipped", func(t *testing.T) {
		RegisterTestingT(t)

		// This test demonstrates that real admitters with identical configs are now skipped
		// thanks to the new Equal method

		factory := factory.NewFactory()
		config := &konfluxciv1alpha1.ExternalAdmissionConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "test-config"},
			Spec: konfluxciv1alpha1.ExternalAdmissionConfigSpec{
				Provider: konfluxciv1alpha1.ProviderConfig{
					AlertManager: &konfluxciv1alpha1.AlertManagerProviderConfig{
						Connection: konfluxciv1alpha1.AlertManagerConnectionConfig{
							URL: "http://alertmanager:9093",
						},
						AlertFilters: []konfluxciv1alpha1.AlertFiltersConfig{
							{AlertNames: []string{"alert1"}},
						},
						CheckTTL: &metav1.Duration{Duration: 30 * time.Second},
					},
				},
			},
		}

		// Create first admitter
		admitter1, err1 := factory.NewAdmitter(config, logr.Discard(), "test-check-3")
		Expect(err1).ToNot(HaveOccurred())

		setCmd1 := SetAdmitter("test-check-3", admitter1)
		setCmd1(manager, ctx)

		Expect(len(manager.admitters)).To(Equal(3)) // We have 2 from previous tests
		originalAdmitter := manager.admitters["test-check-3"].Admitter

		// Create second admitter with IDENTICAL config
		admitter2, err2 := factory.NewAdmitter(config, logr.Discard(), "test-check-3")
		Expect(err2).ToNot(HaveOccurred())

		// Verify they are functionally equivalent
		Expect(admitter1.Equal(admitter2)).To(BeTrue(), "Expected admitters with identical config to be equal")

		setCmd2 := SetAdmitter("test-check-3", admitter2)
		setCmd2(manager, ctx)

		// With identical config, the admitter should NOT be replaced
		Expect(len(manager.admitters)).To(Equal(3))
		currentAdmitter := manager.admitters["test-check-3"].Admitter
		Expect(currentAdmitter).To(Equal(originalAdmitter), "Expected admitter to NOT be replaced with identical config")
		Expect(currentAdmitter).ToNot(Equal(admitter2), "Expected original admitter to remain")

		t.Logf("Result: Real admitters with identical configurations are now skipped, improving performance")
	})
}
