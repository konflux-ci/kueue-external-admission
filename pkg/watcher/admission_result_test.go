package watcher

import (
	"testing"
)

func TestAdmissionResult_NewDefault(t *testing.T) {
	result := NewAdmissionResult()

	if !result.ShouldAdmit() {
		t.Error("Expected default result to allow admission")
	}

	if len(result.GetProviderDetails()) != 0 {
		t.Error("Expected default result to have no provider details")
	}
}

func TestAdmissionResult_Interface(t *testing.T) {
	// Test that our implementation satisfies the interface
	var _ AdmissionResult = &defaultAdmissionResult{
		shouldAdmit:     false,
		providerDetails: map[string][]string{"check1": {"alert1", "alert2"}},
	}

	// Test with actual implementation
	result := &defaultAdmissionResult{
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

func TestAdmissionResult_InternalMethods(t *testing.T) {
	result := &defaultAdmissionResult{
		shouldAdmit:     true,
		providerDetails: make(map[string][]string),
	}

	// Test admission control
	result.setAdmissionDenied()
	if result.ShouldAdmit() {
		t.Error("Expected admission to be denied after setAdmissionDenied")
	}

	result.setAdmissionAllowed()
	if !result.ShouldAdmit() {
		t.Error("Expected admission to be allowed after setAdmissionAllowed")
	}

	// Test addProviderDetails
	result.addProviderDetails("check1", []string{"alert1", "alert2"})
	details := result.GetProviderDetails()
	if len(details["check1"]) != 2 {
		t.Error("Expected two details for check1 after addProviderDetails")
	}

	// Test addProviderDetails with empty slice (should not add)
	result.addProviderDetails("check2", []string{})
	if _, exists := details["check2"]; exists {
		t.Error("Expected check2 to not exist after adding empty details")
	}
}
