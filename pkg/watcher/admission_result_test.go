package watcher

import (
	"testing"
)

func TestAdmissionResult_NewDefault(t *testing.T) {
	result := NewAdmissionResult()

	// Test initial state
	if !result.ShouldAdmit() {
		t.Error("Expected new AdmissionResult to allow admission by default")
	}

	if len(result.GetFiringAlerts()) != 0 {
		t.Error("Expected no firing alerts in new AdmissionResult")
	}

}

func TestAdmissionResult_Interface(t *testing.T) {
	// Create a concrete instance and test the interface methods
	result := &defaultAdmissionResult{
		shouldAdmit:  false,
		firingAlerts: map[string][]string{"check1": {"alert1", "alert2"}},
	}

	// Test interface methods
	if result.ShouldAdmit() {
		t.Error("Expected admission to be denied")
	}

	alerts := result.GetFiringAlerts()
	if len(alerts) != 1 {
		t.Error("Expected one firing alert entry")
	}

	if len(alerts["check1"]) != 2 {
		t.Error("Expected two alerts for check1")
	}

}

func TestAdmissionResult_InternalMethods(t *testing.T) {
	result := &defaultAdmissionResult{
		shouldAdmit:  true,
		firingAlerts: make(map[string][]string),
	}

	// Test setAdmissionDenied
	result.setAdmissionDenied()
	if result.ShouldAdmit() {
		t.Error("Expected admission to be denied after setAdmissionDenied")
	}

	// Test addFiringAlerts
	result.addFiringAlerts("check1", []string{"alert1", "alert2"})
	alerts := result.GetFiringAlerts()
	if len(alerts["check1"]) != 2 {
		t.Error("Expected two alerts for check1 after addFiringAlerts")
	}

	// Test addFiringAlerts with empty slice (should not add)
	result.addFiringAlerts("check2", []string{})
	if _, exists := alerts["check2"]; exists {
		t.Error("Expected no entry for check2 when adding empty alerts")
	}
}
