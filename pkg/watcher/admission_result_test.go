package watcher

import (
	"testing"
)

func TestAdmissionResultBuilder_NewDefault(t *testing.T) {
	builder := NewAdmissionResult()
	result := builder.Build()

	if !result.ShouldAdmit() {
		t.Error("Expected default result to allow admission")
	}

	if len(result.GetProviderDetails()) != 0 {
		t.Error("Expected default result to have no provider details")
	}
}

func TestAdmissionResultBuilder_MethodChaining(t *testing.T) {
	result := NewAdmissionResult().
		SetAdmissionDenied().
		AddProviderDetails("check1", []string{"alert1", "alert2"}).
		Build()

	if result.ShouldAdmit() {
		t.Error("Expected admission to be denied for this test case")
	}

	details := result.GetProviderDetails()
	if len(details) != 1 {
		t.Errorf("Expected 1 check in provider details, got %d", len(details))
	}

	if len(details["check1"]) != 2 {
		t.Errorf("Expected 2 details for check1, got %d", len(details["check1"]))
	}
}

func TestAdmissionResult_Interface(t *testing.T) {
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
	if len(details) != 1 {
		t.Errorf("Expected 1 check in provider details, got %d", len(details))
	}

	if len(details["check1"]) != 2 {
		t.Errorf("Expected 2 details for check1, got %d", len(details["check1"]))
	}

	if result.ShouldAdmit() {
		t.Error("Expected admission to be denied for this test case")
	}
}

func TestAdmissionResultBuilder_BuilderMethods(t *testing.T) {
	builder := NewAdmissionResult()

	// Test admission control
	builder.SetAdmissionDenied()
	result1 := builder.Build()
	if result1.ShouldAdmit() {
		t.Error("Expected admission to be denied after SetAdmissionDenied")
	}

	builder.SetAdmissionAllowed()
	result2 := builder.Build()
	if !result2.ShouldAdmit() {
		t.Error("Expected admission to be allowed after SetAdmissionAllowed")
	}

	// Test AddProviderDetails
	builder.AddProviderDetails("check1", []string{"alert1", "alert2"})
	result3 := builder.Build()
	details := result3.GetProviderDetails()
	if len(details["check1"]) != 2 {
		t.Error("Expected two details for check1 after AddProviderDetails")
	}

	// Test AddProviderDetails with empty slice (should not add)
	builder.AddProviderDetails("check2", []string{})
	result4 := builder.Build()
	if _, exists := result4.GetProviderDetails()["check2"]; exists {
		t.Error("Expected check2 to not exist after adding empty details")
	}
}

func TestAdmissionResult_Immutability(t *testing.T) {
	builder := NewAdmissionResult()
	builder.AddProviderDetails("check1", []string{"alert1", "alert2"})

	result1 := builder.Build()
	result2 := builder.Build()

	// Modify result1's details (this should not affect result2 due to deep copy)
	details1 := result1.GetProviderDetails()
	details1["check1"][0] = "modified"

	details2 := result2.GetProviderDetails()
	if details2["check1"][0] == "modified" {
		t.Error("Expected immutability: modifying result1 should not affect result2")
	}
}
