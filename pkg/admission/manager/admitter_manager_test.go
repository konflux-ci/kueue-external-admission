package manager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"

	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
)

// mockAdmitterForAdmitterManager is a mock implementation of the Admitter interface for AdmitterManager testing
// No locking needed since AdmitterManager processes commands sequentially in a single goroutine
type mockAdmitterForAdmitterManager struct {
	syncCalled       bool
	syncError        error
	syncCallCount    int
	blockUntilCancel bool
}

func (m *mockAdmitterForAdmitterManager) Sync(ctx context.Context, asyncAdmissionResults chan<- result.AsyncAdmissionResult) error {
	m.syncCalled = true
	m.syncCallCount++

	if m.syncError != nil {
		return m.syncError
	}

	// Start background work in a goroutine (like real admitters do)
	go func() {
		if m.blockUntilCancel {
			<-ctx.Done()
		}
		// Real admitters would do their work here and send results to asyncAdmissionResults
	}()

	return nil
}

func newMockAdmitterForAdmitterManager() *mockAdmitterForAdmitterManager {
	return &mockAdmitterForAdmitterManager{}
}

func newMockAdmitterWithError(err error) *mockAdmitterForAdmitterManager {
	return &mockAdmitterForAdmitterManager{syncError: err}
}

func newMockAdmitterThatBlocksUntilCancel() *mockAdmitterForAdmitterManager {
	return &mockAdmitterForAdmitterManager{blockUntilCancel: true}
}

func TestNewAdmitterManager(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	Expect(manager).ToNot(BeNil(), "Expected non-nil AdmitterManager")
	Expect(manager.logger).To(Equal(logger), "Expected logger to be set correctly")
	Expect(manager.admitterCommands).To(Equal(admitterCommands), "Expected admitterCommands channel to be set correctly")
	// Note: We can't directly compare channels with different directions, so we'll just check it's not nil
	Expect(manager.incomingResults).ToNot(BeNil(), "Expected incomingResults channel to be set")
	Expect(manager.admitters).ToNot(BeNil(), "Expected admitters map to be initialized")
	Expect(len(manager.admitters)).To(Equal(0), "Expected admitters map to be empty initially")
}

func TestAdmitterManager_Run_ContextCancellation(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	// Add an admitter first
	mockAdmitter := newMockAdmitterThatBlocksUntilCancel()
	setCmd := SetAdmitter("test-check", mockAdmitter)
	admitterCommands <- setCmd

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan bool)
	go func() {
		manager.Run(ctx)
		done <- true
	}()

	// Wait for the SetAdmitter command to be processed
	Eventually(func() int {
		return len(manager.admitters)
	}, 1*time.Second).Should(Equal(1), "Expected admitter to be added")

	// Cancel context to stop the manager
	cancel()

	// Wait for Run to complete
	select {
	case <-done:
		// Success - Run completed
	case <-time.After(2 * time.Second):
		t.Fatal("AdmitterManager.Run did not complete within timeout after context cancellation")
	}

	// Verify the admitter map is cleaned up
	Expect(len(manager.admitters)).To(Equal(0), "Expected admitters to be cleaned up on context cancellation")
}

func TestAdmitterManager_Run_ProcessCommands(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the manager
	go manager.Run(ctx)

	// Test SetAdmitter command
	mockAdmitter := newMockAdmitterForAdmitterManager()
	setCmd := SetAdmitter("test-check", mockAdmitter)
	admitterCommands <- setCmd

	// Wait for admitter to be added
	Eventually(func() int {
		return len(manager.admitters)
	}, 1*time.Second).Should(Equal(1), "Expected one admitter to be added")

	Eventually(func() bool {
		return manager.admitters["test-check"] != nil
	}, 1*time.Second).Should(BeTrue(), "Expected test-check admitter to exist")

	Expect(manager.admitters["test-check"].AdmissionCheckName).To(Equal("test-check"), "Expected correct admission check name")

	Eventually(func() bool {
		return mockAdmitter.syncCalled
	}, 1*time.Second).Should(BeTrue(), "Expected Sync to be called on admitter")

	// Test RemoveAdmitter command
	removeCmd := RemoveAdmitter("test-check")
	admitterCommands <- removeCmd

	// Wait for admitter to be removed
	Eventually(func() int {
		return len(manager.admitters)
	}, 1*time.Second).Should(Equal(0), "Expected admitter to be removed")
}

func TestSetAdmitter_NewAdmitter(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockAdmitter := newMockAdmitterForAdmitterManager()

	// Execute SetAdmitter command directly
	setCmd := SetAdmitter("test-check", mockAdmitter)
	setCmd(manager, ctx)

	// Verify admitter was added
	Expect(len(manager.admitters)).To(Equal(1), "Expected one admitter to be added")
	entry := manager.admitters["test-check"]
	Expect(entry).ToNot(BeNil(), "Expected admitter entry to exist")
	Expect(entry.Admitter).To(Equal(mockAdmitter), "Expected correct admitter instance")
	Expect(entry.AdmissionCheckName).To(Equal("test-check"), "Expected correct admission check name")
	Expect(entry.Cancel).ToNot(BeNil(), "Expected cancel function to be set")
	Expect(mockAdmitter.syncCalled).To(BeTrue(), "Expected Sync to be called on admitter")
}

func TestSetAdmitter_ReplaceExistingAdmitter(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Add first admitter
	firstAdmitter := newMockAdmitterThatBlocksUntilCancel()
	setCmd1 := SetAdmitter("test-check", firstAdmitter)
	setCmd1(manager, ctx)

	Expect(len(manager.admitters)).To(Equal(1), "Expected one admitter after first set")
	originalEntry := manager.admitters["test-check"]

	// Add second admitter with same name (should replace)
	secondAdmitter := newMockAdmitterForAdmitterManager()
	setCmd2 := SetAdmitter("test-check", secondAdmitter)
	setCmd2(manager, ctx)

	// Verify replacement
	Expect(len(manager.admitters)).To(Equal(1), "Expected still one admitter after replacement")
	newEntry := manager.admitters["test-check"]
	Expect(newEntry).ToNot(Equal(originalEntry), "Expected different entry after replacement")
	Expect(newEntry.Admitter).To(Equal(secondAdmitter), "Expected new admitter instance")
	Expect(secondAdmitter.syncCalled).To(BeTrue(), "Expected Sync to be called on new admitter")
}

func TestSetAdmitter_SameAdmitterSkipped(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mockAdmitter := newMockAdmitterForAdmitterManager()

	// Add admitter first time
	setCmd1 := SetAdmitter("test-check", mockAdmitter)
	setCmd1(manager, ctx)

	originalCallCount := mockAdmitter.syncCallCount

	// Add same admitter again (should be skipped due to reflect.DeepEqual)
	setCmd2 := SetAdmitter("test-check", mockAdmitter)
	setCmd2(manager, ctx)

	// Verify it was skipped
	Expect(mockAdmitter.syncCallCount).To(Equal(originalCallCount), "Expected Sync call count to remain the same when same admitter is set")
}

func TestSetAdmitter_SyncError_Retry(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start manager to process retry commands
	go manager.Run(ctx)

	mockAdmitter := newMockAdmitterWithError(errors.New("sync failed"))

	// Execute SetAdmitter command
	setCmd := SetAdmitter("test-check", mockAdmitter)
	admitterCommands <- setCmd

	// Wait for admitter to be added despite sync error
	Eventually(func() int {
		return len(manager.admitters)
	}, 1*time.Second).Should(Equal(1), "Expected admitter to be added even with sync error")

	Eventually(func() bool {
		return mockAdmitter.syncCalled
	}, 1*time.Second).Should(BeTrue(), "Expected Sync to be called despite error")

	// Note: The retry mechanism uses a 15-second delay, so we can't easily test the retry in a unit test
	// but we can verify the admitter was added and sync was attempted
}

func TestRemoveAdmitter_ExistingAdmitter(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Add admitter first
	mockAdmitter := newMockAdmitterThatBlocksUntilCancel()
	setCmd := SetAdmitter("test-check", mockAdmitter)
	setCmd(manager, ctx)

	Expect(len(manager.admitters)).To(Equal(1), "Expected one admitter before removal")

	// Remove admitter
	removeCmd := RemoveAdmitter("test-check")
	removeCmd(manager, ctx)

	// Verify removal
	Expect(len(manager.admitters)).To(Equal(0), "Expected no admitters after removal")
}

func TestRemoveAdmitter_NonExistentAdmitter(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Try to remove non-existent admitter
	removeCmd := RemoveAdmitter("non-existent")
	removeCmd(manager, ctx)

	// Should not crash and admitters should remain empty
	Expect(len(manager.admitters)).To(Equal(0), "Expected no admitters")
}

func TestListAdmitters_Empty(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// List admitters when empty
	listCmd, resultChan := ListAdmitters()
	listCmd(manager, ctx)

	result := <-resultChan
	Expect(result).ToNot(BeNil(), "Expected non-nil result")
	Expect(len(result)).To(Equal(0), "Expected empty admitters list")
}

func TestListAdmitters_WithAdmitters(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Add multiple admitters
	mockAdmitter1 := newMockAdmitterForAdmitterManager()
	mockAdmitter2 := newMockAdmitterForAdmitterManager()

	setCmd1 := SetAdmitter("check-1", mockAdmitter1)
	setCmd1(manager, ctx)

	setCmd2 := SetAdmitter("check-2", mockAdmitter2)
	setCmd2(manager, ctx)

	// List admitters
	listCmd, resultChan := ListAdmitters()
	listCmd(manager, ctx)

	result := <-resultChan
	Expect(result).ToNot(BeNil(), "Expected non-nil result")
	Expect(len(result)).To(Equal(2), "Expected two admitters in list")
	Expect(result["check-1"]).To(BeTrue(), "Expected check-1 to be in list")
	Expect(result["check-2"]).To(BeTrue(), "Expected check-2 to be in list")
}

func TestAdmitterManager_ConcurrentOperations(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 100)
	incomingResults := make(chan result.AsyncAdmissionResult, 100)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start manager
	go manager.Run(ctx)

	var wg sync.WaitGroup
	numGoroutines := 10

	// Concurrently add admitters
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			checkName := fmt.Sprintf("check-%d", index)
			mockAdmitter := newMockAdmitterForAdmitterManager()
			setCmd := SetAdmitter(checkName, mockAdmitter)
			admitterCommands <- setCmd
		}(i)
	}

	wg.Wait()

	// Verify all admitters were added
	Eventually(func() int {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		return len(result)
	}, 2*time.Second).Should(Equal(numGoroutines), "Expected all admitters to be added")

	// Concurrently remove admitters
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			checkName := fmt.Sprintf("check-%d", index)
			removeCmd := RemoveAdmitter(checkName)
			admitterCommands <- removeCmd
		}(i)
	}

	wg.Wait()

	// Verify all admitters were removed
	Eventually(func() int {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		return len(result)
	}, 2*time.Second).Should(Equal(0), "Expected all admitters to be removed")
}

func TestAdmitterManager_ContextCancellationCleansUpAdmitters(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan bool)
	go func() {
		manager.Run(ctx)
		done <- true
	}()

	// Add some admitters after starting the manager
	mockAdmitter1 := newMockAdmitterThatBlocksUntilCancel()
	mockAdmitter2 := newMockAdmitterThatBlocksUntilCancel()

	setCmd1 := SetAdmitter("check-1", mockAdmitter1)
	setCmd2 := SetAdmitter("check-2", mockAdmitter2)

	// Send both commands
	admitterCommands <- setCmd1
	admitterCommands <- setCmd2

	// Wait for both admitters to be added
	Eventually(func() int {
		return len(manager.admitters)
	}, 2*time.Second).Should(Equal(2), "Expected two admitters before cancellation")

	// Cancel context
	cancel()

	// Wait for Run to complete
	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("AdmitterManager.Run did not complete within timeout")
	}

	// Verify cleanup
	Expect(len(manager.admitters)).To(Equal(0), "Expected all admitters to be cleaned up")
}
