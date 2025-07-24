package watcher

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestAdmissionResultBuilder_NewDefault(t *testing.T) {
	RegisterTestingT(t)
	builder := NewAdmissionResult()
	result := builder.Build()

	Expect(result.ShouldAdmit()).To(BeTrue(), "Expected default result to allow admission")
	Expect(result.GetProviderDetails()).To(BeEmpty(), "Expected default result to have no provider details")
}

func TestAdmissionResultBuilder_MethodChaining(t *testing.T) {
	RegisterTestingT(t)
	result := NewAdmissionResult().
		SetAdmissionDenied().
		AddProviderDetails("check1", []string{"alert1", "alert2"}).
		Build()

	Expect(result.ShouldAdmit()).To(BeFalse(), "Expected admission to be denied for this test case")

	details := result.GetProviderDetails()
	Expect(details).To(HaveLen(1), "Expected 1 check in provider details")
	Expect(details["check1"]).To(HaveLen(2), "Expected 2 details for check1")
}

func TestAdmissionResult_Interface(t *testing.T) {
	RegisterTestingT(t)
	// Test that our implementation satisfies the interface
	var _ AdmissionResult = &admissionResult{
		shouldAdmit:     false,
		providerDetails: map[string][]string{"check1": {"alert1", "alert2"}},
	}

	// Test with actual implementation
	result := &admissionResult{
		shouldAdmit:     false,
		providerDetails: map[string][]string{"check1": {"alert1", "alert2"}},
	}

	details := result.GetProviderDetails()
	Expect(details).To(HaveLen(1), "Expected 1 check in provider details")
	Expect(details["check1"]).To(HaveLen(2), "Expected 2 details for check1")
	Expect(result.ShouldAdmit()).To(BeFalse(), "Expected admission to be denied for this test case")
}

func TestAdmissionResultBuilder_BuilderMethods(t *testing.T) {
	RegisterTestingT(t)
	builder := NewAdmissionResult()

	// Test admission control
	builder.SetAdmissionDenied()
	result1 := builder.Build()
	Expect(result1.ShouldAdmit()).To(BeFalse(), "Expected admission to be denied after SetAdmissionDenied")

	builder.SetAdmissionAllowed()
	result2 := builder.Build()
	Expect(result2.ShouldAdmit()).To(BeTrue(), "Expected admission to be allowed after SetAdmissionAllowed")

	// Test AddProviderDetails
	builder.AddProviderDetails("check1", []string{"alert1", "alert2"})
	result3 := builder.Build()
	details := result3.GetProviderDetails()
	Expect(details["check1"]).To(HaveLen(2), "Expected two details for check1 after AddProviderDetails")

	// Test AddProviderDetails with empty slice (should not add)
	builder.AddProviderDetails("check2", []string{})
	result4 := builder.Build()
	Expect(result4.GetProviderDetails()).ToNot(HaveKey("check2"), "Expected check2 to not exist after adding empty details")
}

func TestAdmissionResult_Immutability(t *testing.T) {
	RegisterTestingT(t)
	builder := NewAdmissionResult()
	builder.AddProviderDetails("check1", []string{"alert1", "alert2"})

	result1 := builder.Build()
	result2 := builder.Build()

	// Modify result1's details (this should not affect result2 due to deep copy)
	details1 := result1.GetProviderDetails()
	details1["check1"][0] = "modified"

	details2 := result2.GetProviderDetails()
	Expect(details2["check1"][0]).ToNot(Equal("modified"), "Expected immutability: modifying result1 should not affect result2")
}
