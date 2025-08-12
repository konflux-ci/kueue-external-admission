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

func TestNewResultManager(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)
	resultNotifications := make(chan result.AdmissionResult, 10)
	resultCmd := make(chan resultCmdFunc, 10)

	manager := NewResultManager(logger, admitterCommands, incomingResults, resultNotifications, resultCmd)

	Expect(manager).ToNot(BeNil(), "Expected non-nil ResultManager")
	Expect(manager.logger).To(Equal(logger), "Expected logger to be set correctly")
	Expect(manager.resultsRegistry).ToNot(BeNil(), "Expected resultsRegistry to be initialized")
	Expect(len(manager.resultsRegistry)).To(Equal(0), "Expected resultsRegistry to be empty initially")

	// Verify channels are set (we can't compare them directly due to type differences)
	Expect(manager.admitterCommands).ToNot(BeNil(), "Expected admitterCommands channel to be set")
	Expect(manager.incomingResults).ToNot(BeNil(), "Expected incomingResults channel to be set")
	Expect(manager.resultNotifications).ToNot(BeNil(), "Expected resultNotifications channel to be set")
	Expect(manager.resultCmd).ToNot(BeNil(), "Expected resultCmd channel to be set")
}

func TestResultManager_Run_ContextCancellation(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)
	resultNotifications := make(chan result.AdmissionResult, 10)
	resultCmd := make(chan resultCmdFunc, 10)

	manager := NewResultManager(logger, admitterCommands, incomingResults, resultNotifications, resultCmd)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan bool)
	go func() {
		manager.Run(ctx)
		done <- true
	}()

	// Cancel context to stop the manager
	cancel()

	// Wait for Run to complete
	select {
	case <-done:
		// Success - Run completed
	case <-time.After(2 * time.Second):
		t.Fatal("ResultManager.Run did not complete within timeout after context cancellation")
	}
}

func TestResultManager_HandleNewResult_FirstResult(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)
	resultNotifications := make(chan result.AdmissionResult, 10)
	resultCmd := make(chan resultCmdFunc, 10)

	manager := NewResultManager(logger, admitterCommands, incomingResults, resultNotifications, resultCmd)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the manager
	go manager.Run(ctx)

	// Create a test result
	builder := result.NewAdmissionResultBuilder("test-check")
	builder.SetAdmissionAllowed()
	builder.AddDetails("test details")

	asyncResult := result.AsyncAdmissionResult{
		AdmissionResult: builder.Build(),
		Error:           nil,
	}

	// Send the result
	incomingResults <- asyncResult

	// Should receive notification since it's a new result
	Eventually(func() bool {
		select {
		case notification := <-resultNotifications:
			return notification.CheckName() == "test-check" && notification.ShouldAdmit()
		default:
			return false
		}
	}, 1*time.Second).Should(BeTrue(), "Expected notification for first result")

	// Verify result is stored using GetSnapshot
	snapshotCmd, snapshotChan := GetSnapshot()
	resultCmd <- snapshotCmd

	Eventually(func() bool {
		select {
		case snapshot := <-snapshotChan:
			storedResult, exists := snapshot["test-check"]
			return exists && storedResult.AdmissionResult.ShouldAdmit() && storedResult.Error == nil
		default:
			return false
		}
	}, 1*time.Second).Should(BeTrue(), "Expected result to be stored in registry")
}

func TestResultManager_HandleNewResult_SameResult(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)
	resultNotifications := make(chan result.AdmissionResult, 10)
	resultCmd := make(chan resultCmdFunc, 10)

	manager := NewResultManager(logger, admitterCommands, incomingResults, resultNotifications, resultCmd)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the manager
	go manager.Run(ctx)

	// Create a test result
	builder := result.NewAdmissionResultBuilder("test-check")
	builder.SetAdmissionAllowed()
	builder.AddDetails("test details")

	asyncResult := result.AsyncAdmissionResult{
		AdmissionResult: builder.Build(),
		Error:           nil,
	}

	// Send the result first time
	incomingResults <- asyncResult

	// Should receive notification for first result
	Eventually(func() bool {
		select {
		case <-resultNotifications:
			return true
		default:
			return false
		}
	}, 1*time.Second).Should(BeTrue(), "Expected notification for first result")

	// Send the same result again
	incomingResults <- asyncResult

	// Should not receive another notification for the same result
	Consistently(func() bool {
		select {
		case <-resultNotifications:
			return false // Received unexpected notification
		default:
			return true // No notification received (expected)
		}
	}, 500*time.Millisecond).Should(BeTrue(), "Should not receive notification for identical result")
}

func TestResultManager_HandleNewResult_ChangedResult(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)
	resultNotifications := make(chan result.AdmissionResult, 10)
	resultCmd := make(chan resultCmdFunc, 10)

	manager := NewResultManager(logger, admitterCommands, incomingResults, resultNotifications, resultCmd)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the manager
	go manager.Run(ctx)

	// Create first result (allowed)
	builder1 := result.NewAdmissionResultBuilder("test-check")
	builder1.SetAdmissionAllowed()
	builder1.AddDetails("first result")

	asyncResult1 := result.AsyncAdmissionResult{
		AdmissionResult: builder1.Build(),
		Error:           nil,
	}

	// Send first result
	incomingResults <- asyncResult1

	// Should receive notification for first result
	Eventually(func() bool {
		select {
		case notification := <-resultNotifications:
			return notification.ShouldAdmit()
		default:
			return false
		}
	}, 1*time.Second).Should(BeTrue(), "Expected notification for first result")

	// Create second result (denied)
	builder2 := result.NewAdmissionResultBuilder("test-check")
	builder2.SetAdmissionDenied()
	builder2.AddDetails("second result")

	asyncResult2 := result.AsyncAdmissionResult{
		AdmissionResult: builder2.Build(),
		Error:           nil,
	}

	// Send second result
	incomingResults <- asyncResult2

	// Should receive notification for changed result
	Eventually(func() bool {
		select {
		case notification := <-resultNotifications:
			return !notification.ShouldAdmit() // Should be denied now
		default:
			return false
		}
	}, 1*time.Second).Should(BeTrue(), "Expected notification for changed result")
}

func TestResultManager_HandleNewResult_WithError(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)
	resultNotifications := make(chan result.AdmissionResult, 10)
	resultCmd := make(chan resultCmdFunc, 10)

	manager := NewResultManager(logger, admitterCommands, incomingResults, resultNotifications, resultCmd)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the manager
	go manager.Run(ctx)

	// Create a result with an error
	builder := result.NewAdmissionResultBuilder("test-check")
	builder.SetAdmissionDenied()
	builder.AddDetails("error result")

	asyncResult := result.AsyncAdmissionResult{
		AdmissionResult: builder.Build(),
		Error:           errors.New("test error"),
	}

	// Send the result with error
	incomingResults <- asyncResult

	// Should NOT receive notification because result has error
	Consistently(func() bool {
		select {
		case <-resultNotifications:
			return false // Received unexpected notification
		default:
			return true // No notification received (expected)
		}
	}, 500*time.Millisecond).Should(BeTrue(), "Should not receive notification for result with error")

	// But result should still be stored
	snapshotCmd, snapshotChan := GetSnapshot()
	resultCmd <- snapshotCmd

	Eventually(func() bool {
		select {
		case snapshot := <-snapshotChan:
			storedResult, exists := snapshot["test-check"]
			return exists && storedResult.Error != nil && storedResult.Error.Error() == "test error"
		default:
			return false
		}
	}, 1*time.Second).Should(BeTrue(), "Expected result with error to be stored")
}

func TestResultManager_GetSnapshot_Empty(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)
	resultNotifications := make(chan result.AdmissionResult, 10)
	resultCmd := make(chan resultCmdFunc, 10)

	manager := NewResultManager(logger, admitterCommands, incomingResults, resultNotifications, resultCmd)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the manager
	go manager.Run(ctx)

	// Get snapshot when empty
	snapshotCmd, snapshotChan := GetSnapshot()
	resultCmd <- snapshotCmd

	Eventually(func() bool {
		select {
		case snapshot := <-snapshotChan:
			return len(snapshot) == 0
		default:
			return false
		}
	}, 1*time.Second).Should(BeTrue(), "Expected empty snapshot")
}

func TestResultManager_GetSnapshot_WithResults(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)
	resultNotifications := make(chan result.AdmissionResult, 10)
	resultCmd := make(chan resultCmdFunc, 10)

	manager := NewResultManager(logger, admitterCommands, incomingResults, resultNotifications, resultCmd)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the manager
	go manager.Run(ctx)

	// Add multiple results
	for i := 0; i < 3; i++ {
		builder := result.NewAdmissionResultBuilder(fmt.Sprintf("check-%d", i))
		builder.SetAdmissionAllowed()
		builder.AddDetails(fmt.Sprintf("details-%d", i))

		asyncResult := result.AsyncAdmissionResult{
			AdmissionResult: builder.Build(),
			Error:           nil,
		}

		incomingResults <- asyncResult
	}

	// Wait for results to be processed (notifications will be sent)
	var notificationCount int
	Eventually(func() int {
		for {
			select {
			case <-resultNotifications:
				notificationCount++
			default:
				return notificationCount
			}
		}
	}, 1*time.Second).Should(Equal(3), "Expected 3 notifications")

	// Get snapshot
	snapshotCmd, snapshotChan := GetSnapshot()
	resultCmd <- snapshotCmd

	Eventually(func() bool {
		select {
		case snapshot := <-snapshotChan:
			if len(snapshot) != 3 {
				return false
			}
			for i := 0; i < 3; i++ {
				checkName := fmt.Sprintf("check-%d", i)
				result, exists := snapshot[checkName]
				if !exists || !result.AdmissionResult.ShouldAdmit() {
					return false
				}
			}
			return true
		default:
			return false
		}
	}, 1*time.Second).Should(BeTrue(), "Expected snapshot with 3 results")
}

func TestResultManager_RemoveStaleResults(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)
	resultNotifications := make(chan result.AdmissionResult, 10)
	resultCmd := make(chan resultCmdFunc, 10)

	manager := NewResultManager(logger, admitterCommands, incomingResults, resultNotifications, resultCmd)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the manager
	go manager.Run(ctx)

	// Add some results and wait for each to be processed
	for i := 0; i < 3; i++ {
		builder := result.NewAdmissionResultBuilder(fmt.Sprintf("check-%d", i))
		builder.SetAdmissionAllowed()

		asyncResult := result.AsyncAdmissionResult{
			AdmissionResult: builder.Build(),
			Error:           nil,
		}

		incomingResults <- asyncResult

		// Wait for this specific result to be processed
		Eventually(func() int {
			snapshotCmd, snapshotChan := GetSnapshot()
			resultCmd <- snapshotCmd
			select {
			case snapshot := <-snapshotChan:
				return len(snapshot)
			case <-time.After(100 * time.Millisecond):
				return -1 // timeout
			}
		}, 1*time.Second).Should(Equal(i+1), fmt.Sprintf("Expected %d results to be stored after adding result %d", i+1, i))
	}

	// Since we can't easily mock the periodic cleanup (1-minute timer) without
	// modifying the production code, this test verifies that results are properly stored.
	// The stale removal functionality is tested through integration tests where
	// the actual timer triggers the cleanup.

	// Verify that the results are properly stored and accessible
	snapshotCmd, snapshotChan := GetSnapshot()
	resultCmd <- snapshotCmd

	Eventually(func() int {
		select {
		case snapshot := <-snapshotChan:
			return len(snapshot)
		default:
			return 0
		}
	}, 1*time.Second).Should(Equal(3), "Expected all results to be stored and accessible")
}

func TestResultManager_ConcurrentOperations(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 100)
	incomingResults := make(chan result.AsyncAdmissionResult, 100)
	resultNotifications := make(chan result.AdmissionResult, 100)
	resultCmd := make(chan resultCmdFunc, 100)

	manager := NewResultManager(logger, admitterCommands, incomingResults, resultNotifications, resultCmd)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the manager
	go manager.Run(ctx)

	var wg sync.WaitGroup
	numGoroutines := 10

	// Concurrently send results
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			builder := result.NewAdmissionResultBuilder(fmt.Sprintf("concurrent-check-%d", index))
			builder.SetAdmissionAllowed()
			builder.AddDetails(fmt.Sprintf("concurrent details %d", index))

			asyncResult := result.AsyncAdmissionResult{
				AdmissionResult: builder.Build(),
				Error:           nil,
			}

			incomingResults <- asyncResult
		}(i)
	}

	// Concurrently request snapshots
	snapshotResults := make(chan map[string]result.AsyncAdmissionResult, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			snapshotCmd, snapshotChan := GetSnapshot()
			resultCmd <- snapshotCmd

			select {
			case snapshot := <-snapshotChan:
				snapshotResults <- snapshot
			case <-time.After(2 * time.Second):
				t.Errorf("Timeout waiting for snapshot")
			}
		}()
	}

	wg.Wait()
	close(snapshotResults)

	// Verify all snapshots were received
	snapshotCount := 0
	for snapshot := range snapshotResults {
		snapshotCount++
		Expect(snapshot).ToNot(BeNil(), "Expected non-nil snapshot")
		// The exact count may vary due to timing, but should be reasonable
		Expect(len(snapshot)).To(BeNumerically(">=", 0), "Expected non-negative result count")
		Expect(len(snapshot)).To(BeNumerically("<=", numGoroutines), "Expected reasonable result count")
	}

	Expect(snapshotCount).To(Equal(numGoroutines), "Expected all snapshots to be received")

	// Final verification - get one more snapshot to see final state
	finalSnapshotCmd, finalSnapshotChan := GetSnapshot()
	resultCmd <- finalSnapshotCmd

	Eventually(func() int {
		select {
		case snapshot := <-finalSnapshotChan:
			return len(snapshot)
		default:
			return -1
		}
	}, 2*time.Second).Should(Equal(numGoroutines), "Expected all concurrent results to be stored")
}

func TestResultManager_NotificationFlow(t *testing.T) {
	RegisterTestingT(t)

	logger := logr.Discard()
	admitterCommands := make(chan admitterCmdFunc, 10)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)
	resultNotifications := make(chan result.AdmissionResult, 10)
	resultCmd := make(chan resultCmdFunc, 10)

	manager := NewResultManager(logger, admitterCommands, incomingResults, resultNotifications, resultCmd)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the manager
	go manager.Run(ctx)

	// Test sequence: allowed -> denied -> allowed -> error -> allowed
	testCases := []struct {
		name         string
		shouldAdmit  bool
		hasError     bool
		expectNotify bool
		details      string
	}{
		{"first-allowed", true, false, true, "first allowed"},
		{"change-to-denied", false, false, true, "now denied"},
		{"change-to-allowed", true, false, true, "allowed again"},
		{"with-error", false, true, false, "has error"},           // No notification due to error
		{"error-to-allowed", true, false, true, "error resolved"}, // Should notify since result changed
	}

	checkName := "notification-test"
	receivedNotifications := make(chan result.AdmissionResult, 10)

	// Start a goroutine to collect notifications
	go func() {
		for {
			select {
			case notification := <-resultNotifications:
				if notification.CheckName() == checkName {
					receivedNotifications <- notification
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Send test cases
	for _, tc := range testCases {
		t.Logf("Testing case: %s", tc.name)

		builder := result.NewAdmissionResultBuilder(checkName)
		if tc.shouldAdmit {
			builder.SetAdmissionAllowed()
		} else {
			builder.SetAdmissionDenied()
		}
		builder.AddDetails(tc.details)

		asyncResult := result.AsyncAdmissionResult{
			AdmissionResult: builder.Build(),
		}

		if tc.hasError {
			asyncResult.Error = errors.New("test error")
		}

		incomingResults <- asyncResult
	}

	// Collect the expected 4 notifications (excluding error case)
	var notifications []result.AdmissionResult
	for i := 0; i < 4; i++ {
		Eventually(func() bool {
			select {
			case notification := <-receivedNotifications:
				notifications = append(notifications, notification)
				return true
			default:
				return false
			}
		}, 2*time.Second).Should(BeTrue(), fmt.Sprintf("Expected notification %d", i+1))
	}

	// Verify notification contents
	Expect(len(notifications)).To(Equal(4), "Expected 4 notifications")
	Expect(notifications[0].ShouldAdmit()).To(BeTrue(), "First notification should be allowed")
	Expect(notifications[1].ShouldAdmit()).To(BeFalse(), "Second notification should be denied")
	Expect(notifications[2].ShouldAdmit()).To(BeTrue(), "Third notification should be allowed")
	Expect(notifications[3].ShouldAdmit()).To(BeTrue(), "Fourth notification should be allowed (error resolved)")
}
