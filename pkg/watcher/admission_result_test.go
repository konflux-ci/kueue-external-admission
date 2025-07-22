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

	if len(result.GetErrors()) != 0 {
		t.Error("Expected no errors in new AdmissionResult")
	}
}

func TestAdmissionResult_Interface(t *testing.T) {
	// Create a concrete instance and test the interface methods
	result := &defaultAdmissionResult{
		shouldAdmit:  false,
		firingAlerts: map[string][]string{"check1": {"alert1", "alert2"}},
		errors:       map[string]error{"check2": &testError{"test error"}},
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

	errors := result.GetErrors()
	if len(errors) != 1 {
		t.Error("Expected one error entry")
	}

	if errors["check2"] == nil {
		t.Error("Expected error for check2")
	}
}

func TestAdmissionResult_InternalMethods(t *testing.T) {
	result := &defaultAdmissionResult{
		shouldAdmit:  true,
		firingAlerts: make(map[string][]string),
		errors:       make(map[string]error),
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

	// Test addError
	testErr := &testError{"test error"}
	result.addError("check1", testErr)
	errors := result.GetErrors()
	if errors["check1"] != testErr {
		t.Error("Expected test error for check1 after addError")
	}
}

// testError is a simple error implementation for testing
type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}
