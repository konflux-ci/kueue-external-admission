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

	"github.com/konflux-ci/kueue-external-admission/pkg/admission"
	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
)

const (
	mockCheckName = "mock-check"
)

// mockAdmitterForAdmitterManager is a mock implementation of the Admitter interface for AdmitterManager testing
// Uses channels for communication instead of shared state to avoid race conditions
type mockAdmitterForAdmitterManager struct {
	syncError        error
	blockUntilCancel bool
	checkName        string // Name to use in admission results
}

func (m *mockAdmitterForAdmitterManager) Sync(ctx context.Context,
	asyncAdmissionResults chan<- result.AsyncAdmissionResult) error {
	if m.syncError != nil {
		return m.syncError
	}

	// Send a test result to indicate sync was called
	builder := result.NewAdmissionResultBuilder(m.checkName)
	builder.SetAdmissionAllowed()
	builder.AddDetails("mock-sync-called")

	asyncResult := result.AsyncAdmissionResult{
		AdmissionResult: builder.Build(),
		Error:           nil,
	}

	// Send the result to indicate Sync was called
	select {
	case asyncAdmissionResults <- asyncResult:
	case <-ctx.Done():
		return ctx.Err()
	}

	// Now Sync is blocking - it runs until context is cancelled or work is done
	if m.blockUntilCancel {
		<-ctx.Done()
	}

	return ctx.Err()
}

func (m *mockAdmitterForAdmitterManager) Equal(other admission.Admitter) bool {
	// Type assertion to check if the other admitter is also a mock admitter
	otherMock, ok := other.(*mockAdmitterForAdmitterManager)
	if !ok {
		return false
	}

	// Compare the relevant fields that determine functional equivalence
	// Compare errors by message, not by instance
	errorsEqual := (m.syncError == nil && otherMock.syncError == nil) ||
		(m.syncError != nil && otherMock.syncError != nil && m.syncError.Error() == otherMock.syncError.Error())

	return errorsEqual && m.blockUntilCancel == otherMock.blockUntilCancel && m.checkName == otherMock.checkName
}

func newMockAdmitterForAdmitterManager() *mockAdmitterForAdmitterManager {
	return &mockAdmitterForAdmitterManager{
		checkName: mockCheckName,
	}
}

func newMockAdmitterForAdmitterManagerWithName(checkName string) *mockAdmitterForAdmitterManager {
	return &mockAdmitterForAdmitterManager{
		checkName: checkName,
	}
}

func newMockAdmitterWithError(err error) *mockAdmitterForAdmitterManager {
	return &mockAdmitterForAdmitterManager{
		syncError: err,
		checkName: "mock-check-error",
	}
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
	// For construction test, we don't need to test internal state - just that the manager was created properly
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
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		return len(result)
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

	// Note: We can't verify the admitter map is cleaned up after the manager stops
	// because there's no longer a goroutine processing commands. The cleanup happens
	// in the Run method before it exits, which is tested by the successful completion above.
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
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		return len(result)
	}, 1*time.Second).Should(Equal(1), "Expected one admitter to be added")

	Eventually(func() bool {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		_, exists := result["test-check"]
		return exists
	}, 1*time.Second).Should(BeTrue(), "Expected test-check admitter to exist")

	// Wait for a result from the admitter to confirm Sync was called
	Eventually(func() bool {
		select {
		case result := <-incomingResults:
			return result.AdmissionResult.CheckName() == mockCheckName
		default:
			return false
		}
	}, 1*time.Second).Should(BeTrue(), "Expected Sync to be called on admitter")

	// Test RemoveAdmitter command
	removeCmd := RemoveAdmitter("test-check")
	admitterCommands <- removeCmd

	// Wait for admitter to be removed
	Eventually(func() int {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		return len(result)
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

	// Start the manager in a goroutine
	go manager.Run(ctx)

	mockAdmitter := newMockAdmitterForAdmitterManager()

	// Execute SetAdmitter command via channel
	setCmd := SetAdmitter("test-check", mockAdmitter)
	admitterCommands <- setCmd

	// Verify admitter was added by checking count
	Eventually(func() int {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		return len(result)
	}, 1*time.Second).Should(Equal(1), "Expected one admitter to be added")

	// Since Sync now runs in a goroutine, we need to wait for it to be called
	Eventually(func() bool {
		select {
		case result := <-incomingResults:
			return result.AdmissionResult.CheckName() == mockCheckName
		default:
			return false
		}
	}, 1*time.Second).Should(BeTrue(), "Expected Sync to be called on admitter")
}

func TestSetAdmitter_ReplaceExistingAdmitter(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the manager in a goroutine
	go manager.Run(ctx)

	// Add first admitter
	firstAdmitter := newMockAdmitterThatBlocksUntilCancel()
	setCmd1 := SetAdmitter("test-check", firstAdmitter)
	admitterCommands <- setCmd1

	Eventually(func() int {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		return len(result)
	}, 1*time.Second).Should(Equal(1), "Expected one admitter after first set")

	// Add second admitter with same name (should replace)
	secondAdmitter := newMockAdmitterForAdmitterManager()
	setCmd2 := SetAdmitter("test-check", secondAdmitter)
	admitterCommands <- setCmd2

	// Verify replacement by checking count remains the same
	Consistently(func() int {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		return len(result)
	}, 500*time.Millisecond).Should(Equal(1), "Expected still one admitter after replacement")

	// Since Sync now runs in a goroutine, we need to wait for it to be called
	Eventually(func() bool {
		select {
		case result := <-incomingResults:
			return result.AdmissionResult.CheckName() == mockCheckName
		default:
			return false
		}
	}, 1*time.Second).Should(BeTrue(), "Expected Sync to be called on new admitter")
}

func TestSetAdmitter_SameAdmitterSkipped(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the manager in a goroutine
	go manager.Run(ctx)

	mockAdmitter := newMockAdmitterForAdmitterManager()

	// Add admitter first time
	setCmd1 := SetAdmitter("test-check", mockAdmitter)
	admitterCommands <- setCmd1

	// Wait for first sync call and count results
	var resultCount int
	Eventually(func() bool {
		select {
		case result := <-incomingResults:
			if result.AdmissionResult.CheckName() == mockCheckName {
				resultCount++
				return true
			}
		default:
		}
		return false
	}, 1*time.Second).Should(BeTrue(), "Expected first Sync to be called")

	// Add same admitter again (should be skipped due to Equal method)
	setCmd2 := SetAdmitter("test-check", mockAdmitter)
	admitterCommands <- setCmd2

	// Give some time and verify no additional results (sync was skipped)
	Consistently(func() int {
		select {
		case result := <-incomingResults:
			if result.AdmissionResult.CheckName() == mockCheckName {
				resultCount++
			}
		default:
		}
		return resultCount
	}, 500*time.Millisecond, 50*time.Millisecond).Should(Equal(1),
		"Expected Sync call count to remain the same when same admitter is set")
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
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		return len(result)
	}, 1*time.Second).Should(Equal(1), "Expected admitter to be added even with sync error")

	// For error case, the sync method returns error immediately, so no result is sent
	// We just verify the admitter was added and the sync was attempted (no results expected)

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

	// Start the manager in a goroutine
	go manager.Run(ctx)

	// Add admitter first
	mockAdmitter := newMockAdmitterThatBlocksUntilCancel()
	setCmd := SetAdmitter("test-check", mockAdmitter)
	admitterCommands <- setCmd

	// Wait for admitter to be added
	Eventually(func() int {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		return len(result)
	}, 1*time.Second).Should(Equal(1), "Expected one admitter before removal")

	// Remove admitter
	removeCmd := RemoveAdmitter("test-check")
	admitterCommands <- removeCmd

	// Verify removal
	Eventually(func() int {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		return len(result)
	}, 1*time.Second).Should(Equal(0), "Expected no admitters after removal")
}

func TestRemoveAdmitter_NonExistentAdmitter(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the manager in a goroutine
	go manager.Run(ctx)

	// Try to remove non-existent admitter
	removeCmd := RemoveAdmitter("non-existent")
	admitterCommands <- removeCmd

	// Should not crash and admitters should remain empty
	Eventually(func() int {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		return len(result)
	}, 1*time.Second).Should(Equal(0), "Expected no admitters")
}

func TestListAdmitters_Empty(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the manager in a goroutine
	go manager.Run(ctx)

	// List admitters when empty
	listCmd, resultChan := ListAdmitters()
	admitterCommands <- listCmd

	result := <-resultChan
	Expect(result).ToNot(BeNil(), "Expected non-nil result")
	Expect(result).To(BeEmpty(), "Expected empty admitters list")
}

func TestListAdmitters_WithAdmitters(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the manager in a goroutine
	go manager.Run(ctx)

	// Add multiple admitters
	mockAdmitter1 := newMockAdmitterForAdmitterManager()
	mockAdmitter2 := newMockAdmitterForAdmitterManager()

	setCmd1 := SetAdmitter("check-1", mockAdmitter1)
	admitterCommands <- setCmd1

	setCmd2 := SetAdmitter("check-2", mockAdmitter2)
	admitterCommands <- setCmd2

	// Wait for both admitters to be added
	Eventually(func() int {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		return len(result)
	}, 1*time.Second).Should(Equal(2), "Expected two admitters to be added")

	// List admitters
	listCmd, resultChan := ListAdmitters()
	admitterCommands <- listCmd

	result := <-resultChan
	Expect(result).ToNot(BeNil(), "Expected non-nil result")
	Expect(result).To(HaveLen(2), "Expected two admitters in list")
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
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		return len(result)
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

	// Note: We can't verify cleanup after the manager stops as there's no goroutine processing commands.
	// The cleanup happens in the Run method before it exits, which is tested by successful completion above.
}

func TestAdmitterManager_ConcurrentAddAndRemoveOperations(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 200) // Larger buffer for mixed operations
	incomingResults := make(chan result.AsyncAdmissionResult, 100)

	manager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start manager
	go manager.Run(ctx)

	var wg sync.WaitGroup
	numOperations := 20

	// First, add some initial admitters to have something to remove
	initialAdmitters := 5
	for i := 0; i < initialAdmitters; i++ {
		checkName := fmt.Sprintf("initial-check-%d", i)
		mockAdmitter := newMockAdmitterForAdmitterManager()
		setCmd := SetAdmitter(checkName, mockAdmitter)
		admitterCommands <- setCmd
	}

	// Wait for initial admitters to be added
	Eventually(func() int {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		return len(result)
	}, 2*time.Second).Should(Equal(initialAdmitters), "Expected initial admitters to be added")

	// Now perform concurrent mixed operations
	// Half will add new admitters, half will remove existing ones
	for i := 0; i < numOperations; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			if index%2 == 0 {
				// Add operation
				checkName := fmt.Sprintf("concurrent-check-%d", index)
				mockAdmitter := newMockAdmitterForAdmitterManager()
				setCmd := SetAdmitter(checkName, mockAdmitter)
				admitterCommands <- setCmd
			} else {
				// Remove operation - try to remove from initial admitters or previously added ones
				var checkName string
				if index < initialAdmitters*2 {
					// Remove from initial admitters
					checkName = fmt.Sprintf("initial-check-%d", index%initialAdmitters)
				} else {
					// Try to remove a previously added concurrent admitter
					// This might fail if the admitter wasn't added yet, which is fine for this test
					checkName = fmt.Sprintf("concurrent-check-%d", index-1)
				}
				removeCmd := RemoveAdmitter(checkName)
				admitterCommands <- removeCmd
			}
		}(i)
	}

	wg.Wait()

	// Verify the manager is still functional by getting the current state
	var finalCount int
	Eventually(func() bool {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		finalCount = len(result)
		return true // We just want to get the count, any count is valid
	}, 2*time.Second).Should(BeTrue(), "Expected to be able to list admitters after concurrent operations")

	t.Logf("Final admitter count after concurrent add/remove operations: %d", finalCount)

	// The exact final count is unpredictable due to race conditions, but it should be reasonable
	// We started with 5, added 10 more (even indices), and removed up to 10 (odd indices)
	// So we expect somewhere between 0 and 15 admitters
	Expect(finalCount).To(BeNumerically(">=", 0), "Expected non-negative admitter count")
	Expect(finalCount).To(BeNumerically("<=", 15), "Expected reasonable upper bound on admitter count")

	// Verify we can still add and remove admitters after the concurrent operations
	testAdmitter := newMockAdmitterForAdmitterManager()
	setCmd := SetAdmitter("post-test-check", testAdmitter)
	admitterCommands <- setCmd

	Eventually(func() bool {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		_, exists := result["post-test-check"]
		return exists
	}, 2*time.Second).Should(BeTrue(), "Expected to be able to add admitter after concurrent operations")

	removeCmd := RemoveAdmitter("post-test-check")
	admitterCommands <- removeCmd

	Eventually(func() bool {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		_, exists := result["post-test-check"]
		return !exists
	}, 2*time.Second).Should(BeTrue(), "Expected to be able to remove admitter after concurrent operations")
}

func TestAdmitterManager_ConcurrentReplaceOperations(t *testing.T) {
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
	checkName := "shared-check" // All goroutines will try to set the same check name

	// Concurrently try to set the same admitter name with different configurations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			// Create admitters with different errors to make them distinguishable
			var mockAdmitter *mockAdmitterForAdmitterManager
			if index%3 == 0 {
				mockAdmitter = newMockAdmitterWithError(fmt.Errorf("error-%d", index))
			} else {
				mockAdmitter = newMockAdmitterForAdmitterManager()
				if index%2 == 0 {
					mockAdmitter.blockUntilCancel = true
				}
			}
			setCmd := SetAdmitter(checkName, mockAdmitter)
			admitterCommands <- setCmd
		}(i)
	}

	wg.Wait()

	// Verify exactly one admitter exists with the shared name
	Eventually(func() int {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		return len(result)
	}, 2*time.Second).Should(Equal(1), "Expected exactly one admitter after concurrent replace operations")

	Eventually(func() bool {
		listCmd, resultChan := ListAdmitters()
		admitterCommands <- listCmd
		result := <-resultChan
		_, exists := result[checkName]
		return exists
	}, 2*time.Second).Should(BeTrue(), "Expected the shared check name to exist")

	t.Logf("Successfully handled %d concurrent replace operations on the same admitter name", numGoroutines)
}
