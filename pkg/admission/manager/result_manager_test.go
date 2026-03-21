package manager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/konflux-ci/kueue-external-admission/pkg/admission/result"
)

// testResultManagerSetup creates a new ResultManager with all necessary channels for testing
type testResultManagerSetup struct {
	manager             *ResultManager
	admitterManager     *AdmitterManager
	admitterCommands    chan admitterCmdFunc
	incomingResults     chan result.AsyncAdmissionResult
	resultNotifications chan result.AdmissionResult
	resultCmd           chan resultCmdFunc
	cleanupChan         chan time.Time
}

// newTestResultManagerSetup creates a complete test setup for ResultManager
func newTestResultManagerSetup() *testResultManagerSetup {
	logger := zap.New(zap.UseDevMode(true))
	admitterCommands := make(chan admitterCmdFunc, 1)
	incomingResults := make(chan result.AsyncAdmissionResult, 10)
	resultNotifications := make(chan result.AdmissionResult, 10)
	resultCmd := make(chan resultCmdFunc, 1)
	cleanupChan := make(chan time.Time, 1)

	manager := NewResultManager(logger, admitterCommands, incomingResults, resultNotifications, resultCmd, cleanupChan)
	admitterManager := NewAdmitterManager(logger, admitterCommands, incomingResults)

	return &testResultManagerSetup{
		manager:             manager,
		admitterManager:     admitterManager,
		admitterCommands:    admitterCommands,
		incomingResults:     incomingResults,
		resultNotifications: resultNotifications,
		resultCmd:           resultCmd,
		cleanupChan:         cleanupChan,
	}
}

// runManagerInBackground starts the manager in a goroutine and returns context and cancel function
func runManagerInBackground(setup *testResultManagerSetup) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go setup.admitterManager.Run(ctx)
	go setup.manager.Run(ctx)
	return ctx, cancel
}

// createAsyncResult is a helper to create test results
func createAsyncResult(checkName string, shouldAdmit bool, details string, err error) result.AsyncAdmissionResult {
	builder := result.NewAdmissionResultBuilder(checkName)
	if shouldAdmit {
		builder.SetAdmissionAllowed()
	} else {
		builder.SetAdmissionDenied()
	}
	if details != "" {
		builder.AddDetails(details)
	}

	return result.AsyncAdmissionResult{
		AdmissionResult: builder.Build(),
		Error:           err,
	}
}

func TestNewResultManager(t *testing.T) {
	RegisterTestingT(t)

	setup := newTestResultManagerSetup()

	Expect(setup.manager).ToNot(BeNil(), "Expected non-nil ResultManager")
	Expect(setup.manager.resultsRegistry).ToNot(BeNil(), "Expected resultsRegistry to be initialized")
	Expect(setup.manager.resultsRegistry).To(BeEmpty(), "Expected resultsRegistry to be empty initially")

	// Verify channels are set (we can't compare them directly due to type differences)
	Expect(setup.manager.admitterCommands).ToNot(BeNil(), "Expected admitterCommands channel to be set")
	Expect(setup.manager.incomingResults).ToNot(BeNil(), "Expected incomingResults channel to be set")
	Expect(setup.manager.resultNotifications).ToNot(BeNil(), "Expected resultNotifications channel to be set")
	Expect(setup.manager.resultCmd).ToNot(BeNil(), "Expected resultCmd channel to be set")
}

func TestResultManager_Run_ContextCancellation(t *testing.T) {
	RegisterTestingT(t)

	setup := newTestResultManagerSetup()

	// Start the manager with a context we can cancel
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan bool, 1)
	go func() {
		setup.manager.Run(ctx)
		done <- true
	}()

	// Give the manager a moment to start
	Eventually(func() bool {
		// Send a snapshot command to verify the manager is running
		snapshotCmd, snapshotChan := GetSnapshot()
		setup.resultCmd <- snapshotCmd

		select {
		case <-snapshotChan:
			return true
		case <-time.After(100 * time.Millisecond):
			return false
		}
	}, 1*time.Second).Should(BeTrue(), "Expected manager to be running and responsive")

	// Cancel context to stop the manager
	cancel()

	// Verify the manager stops within a reasonable time
	Eventually(func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 2*time.Second).Should(BeTrue(), "Expected manager to stop after context cancellation")
}

func TestResultManager_HandleNewResult(t *testing.T) {
	const testCheckName = "test-check"

	testCases := []struct {
		name                  string
		setupResults          []result.AsyncAdmissionResult
		expectedNotifications int
		validateFirstResult   func(notification result.AdmissionResult) bool
		validateSecondResult  func(notification result.AdmissionResult) bool
		expectStoredError     bool
	}{
		{
			name: "FirstResult",
			setupResults: []result.AsyncAdmissionResult{
				createAsyncResult(testCheckName, true, "test details", nil),
			},
			expectedNotifications: 1,
			validateFirstResult: func(notification result.AdmissionResult) bool {
				return notification.CheckName() == "test-check" && notification.ShouldAdmit()
			},
			expectStoredError: false,
		},
		{
			name: "SameResult",
			setupResults: []result.AsyncAdmissionResult{
				createAsyncResult(testCheckName, true, "test details", nil),
				createAsyncResult(testCheckName, true, "test details", nil), // Same result
			},
			expectedNotifications: 1, // Only first should notify
			validateFirstResult: func(notification result.AdmissionResult) bool {
				return notification.CheckName() == testCheckName && notification.ShouldAdmit()
			},
			expectStoredError: false,
		},
		{
			name: "ChangedResult",
			setupResults: []result.AsyncAdmissionResult{
				createAsyncResult(testCheckName, true, "first", nil),
				createAsyncResult(testCheckName, false, "second", nil), // Different result
			},
			expectedNotifications: 2,
			validateFirstResult: func(notification result.AdmissionResult) bool {
				return notification.CheckName() == testCheckName && notification.ShouldAdmit()
			},
			validateSecondResult: func(notification result.AdmissionResult) bool {
				return notification.CheckName() == testCheckName && !notification.ShouldAdmit()
			},
			expectStoredError: false,
		},
		{
			name: "WithError",
			setupResults: []result.AsyncAdmissionResult{
				createAsyncResult(testCheckName, false, "error case", errors.New("test error")),
			},
			expectedNotifications: 0, // No notifications for errors
			expectStoredError:     true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			RegisterTestingT(t)

			setup := newTestResultManagerSetup()
			_, cancel := runManagerInBackground(setup)
			defer cancel()

			// Send all results
			for _, asyncResult := range tc.setupResults {
				setup.incomingResults <- asyncResult
			}

			// Collect notifications
			var notifications []result.AdmissionResult
			if tc.expectedNotifications > 0 {
				for i := 0; i < tc.expectedNotifications; i++ {
					Eventually(func() bool {
						select {
						case notification := <-setup.resultNotifications:
							notifications = append(notifications, notification)
							return true
						default:
							return false
						}
					}, 1*time.Second).Should(BeTrue(), fmt.Sprintf("Expected notification %d", i+1))
				}

				// Validate notifications
				if tc.validateFirstResult != nil {
					Expect(tc.validateFirstResult(notifications[0])).To(BeTrue(), "First notification validation failed")
				}
				if tc.validateSecondResult != nil && len(notifications) > 1 {
					Expect(tc.validateSecondResult(notifications[1])).To(BeTrue(), "Second notification validation failed")
				}
			} else {
				// Should not receive any notifications
				Consistently(func() bool {
					select {
					case <-setup.resultNotifications:
						return false
					default:
						return true
					}
				}, 500*time.Millisecond).Should(BeTrue(), "Should not receive notifications")
			}

			// Verify result storage
			snapshotCmd, snapshotChan := GetSnapshot()
			setup.resultCmd <- snapshotCmd

			Eventually(func() bool {
				select {
				case snapshot := <-snapshotChan:
					storedResult, exists := snapshot["test-check"]
					if !exists {
						return false
					}
					if tc.expectStoredError {
						return storedResult.Error != nil
					}
					return storedResult.Error == nil
				default:
					return false
				}
			}, 1*time.Second).Should(BeTrue(), "Expected result to be stored correctly")
		})
	}
}

func TestResultManager_GetSnapshot(t *testing.T) {
	testCases := []struct {
		name           string
		setupResults   []result.AsyncAdmissionResult
		expectedCount  int
		validateResult func(snapshot map[string]result.AsyncAdmissionResult) bool
	}{
		{
			name:          "Empty",
			setupResults:  []result.AsyncAdmissionResult{},
			expectedCount: 0,
			validateResult: func(snapshot map[string]result.AsyncAdmissionResult) bool {
				return len(snapshot) == 0
			},
		},
		{
			name: "WithResults",
			setupResults: []result.AsyncAdmissionResult{
				createAsyncResult("check-0", true, "details-0", nil),
				createAsyncResult("check-1", true, "details-1", nil),
				createAsyncResult("check-2", true, "details-2", nil),
			},
			expectedCount: 3,
			validateResult: func(snapshot map[string]result.AsyncAdmissionResult) bool {
				if len(snapshot) != 3 {
					return false
				}
				for i := 0; i < 3; i++ {
					checkName := fmt.Sprintf("check-%d", i)
					storedResult, exists := snapshot[checkName]
					if !exists || storedResult.Error != nil {
						return false
					}
				}
				return true
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			RegisterTestingT(t)

			setup := newTestResultManagerSetup()
			_, cancel := runManagerInBackground(setup)
			defer cancel()

			// Send all results
			for _, asyncResult := range tc.setupResults {
				setup.incomingResults <- asyncResult
			}

			// Wait for results to be processed if any
			if tc.expectedCount > 0 {
				var notificationCount int
				Eventually(func() int {
					for {
						select {
						case <-setup.resultNotifications:
							notificationCount++
						default:
							return notificationCount
						}
					}
				}, 1*time.Second).Should(Equal(tc.expectedCount), "Expected notifications")
			}

			// Get snapshot
			snapshotCmd, snapshotChan := GetSnapshot()
			setup.resultCmd <- snapshotCmd

			Eventually(func() bool {
				select {
				case snapshot := <-snapshotChan:
					return tc.validateResult(snapshot)
				default:
					return false
				}
			}, 1*time.Second).Should(BeTrue(), "Snapshot validation failed")
		})
	}
}

func TestResultManager_RemoveStaleResults(t *testing.T) {
	RegisterTestingT(t)

	setup := newTestResultManagerSetup()
	_, cancel := runManagerInBackground(setup)
	defer cancel()

	mockAdmitter := newMockAdmitterForAdmitterManager()
	setup.admitterCommands <- SetAdmitter(mockCheckName, mockAdmitter)
	setup.incomingResults <- createAsyncResult("mock-check", true, "", nil)

	numOfResults := 50

	// Add some results and wait for each to be processed
	for i := range numOfResults {
		asyncResult := createAsyncResult(fmt.Sprintf("check-%d", i), true, "", nil)
		setup.incomingResults <- asyncResult

		// Wait for this specific result to be processed
		Eventually(func() int {
			snapshotCmd, snapshotChan := GetSnapshot()
			setup.resultCmd <- snapshotCmd
			select {
			case snapshot := <-snapshotChan:
				return len(snapshot)
			case <-time.After(100 * time.Millisecond):
				return -1 // timeout
			}
		}, 1*time.Second).Should(Equal(i+2), fmt.Sprintf("Expected %d results to be stored after adding result %d", i+1, i))
	}

	// Trigger cleanup
	setup.cleanupChan <- time.Now()

	Eventually(func() int {
		// Verify that the results are properly stored and accessible
		snapshotCmd, snapshotChan := GetSnapshot()
		setup.resultCmd <- snapshotCmd

		select {
		case snapshot := <-snapshotChan:
			return len(snapshot)
		case <-time.After(100 * time.Millisecond):
			return -1
		}
	}, 3*time.Second).Should(Equal(1), "Expected only 1 result to remain after cleanup")
}

func TestResultManager_ConcurrentOperations(t *testing.T) {
	RegisterTestingT(t)

	setup := newTestResultManagerSetup()
	_, cancel := runManagerInBackground(setup)
	defer cancel()

	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrently send results
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			asyncResult := createAsyncResult(
				fmt.Sprintf("concurrent-check-%d", index),
				true,
				fmt.Sprintf("concurrent details %d", index),
				nil,
			)

			setup.incomingResults <- asyncResult
			// Create duplicate results
			if index%2 == 0 {
				setup.incomingResults <- asyncResult
			}
		}(i)
	}

	// Concurrently request snapshots
	snapshotResults := make(chan map[string]result.AsyncAdmissionResult, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// drain results channel
			<-setup.resultNotifications
			snapshotCmd, snapshotChan := GetSnapshot()
			setup.resultCmd <- snapshotCmd
			snapshotResults <- <-snapshotChan
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
		Expect(len(snapshot)).To(BeNumerically(">=", 0), "Expected positive result count")
		Expect(len(snapshot)).To(BeNumerically("<=", numGoroutines), "Expected reasonable result count")
	}

	Expect(snapshotCount).To(Equal(numGoroutines), "Expected all snapshots to be received")

	// Final verification - get one more snapshot to see final state
	finalSnapshotCmd, finalSnapshotChan := GetSnapshot()
	setup.resultCmd <- finalSnapshotCmd

	Eventually(func() int {
		select {
		case snapshot := <-finalSnapshotChan:
			return len(snapshot)
		default:
			return -1
		}
	}, 10*time.Second).Should(Equal(numGoroutines), "Expected all concurrent results to be stored")
}

func TestResultManager_NotificationFlow(t *testing.T) {
	RegisterTestingT(t)

	setup := newTestResultManagerSetup()
	_, cancel := runManagerInBackground(setup)
	defer cancel()

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

	// Send test cases
	for _, tc := range testCases {
		t.Logf("Testing case: %s", tc.name)

		asyncResult := createAsyncResult(checkName, tc.shouldAdmit, tc.details, nil)
		if tc.hasError {
			asyncResult.Error = errors.New("test error")
		}

		setup.incomingResults <- asyncResult
	}

	// Collect the expected 4 notifications (excluding error case)
	var notifications []result.AdmissionResult
	for i := 0; i < 4; i++ {
		Eventually(func() bool {
			select {
			case notification := <-setup.resultNotifications:
				notifications = append(notifications, notification)
				return true
			default:
				return false
			}
		}, 2*time.Second).Should(BeTrue(), fmt.Sprintf("Expected notification %d", i+1))
	}

	// Verify notification contents
	Expect(notifications).To(HaveLen(4), "Expected 4 notifications")
	Expect(notifications[0].ShouldAdmit()).To(BeTrue(), "First notification should be allowed")
	Expect(notifications[1].ShouldAdmit()).To(BeFalse(), "Second notification should be denied")
	Expect(notifications[2].ShouldAdmit()).To(BeTrue(), "Third notification should be allowed")
	Expect(notifications[3].ShouldAdmit()).To(BeTrue(), "Fourth notification should be allowed (error resolved)")
}
