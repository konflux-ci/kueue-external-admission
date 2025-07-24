package watcher

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestCache_GetOrUpdate_FirstCall(t *testing.T) {
	RegisterTestingT(t)
	cache := NewCache[string](time.Minute)
	ctx := context.Background()

	callCount := 0
	fetch := func(ctx context.Context) (string, error) {
		callCount++
		return "test-value", nil
	}

	result, err := cache.GetOrUpdate(ctx, fetch)

	Expect(err).ToNot(HaveOccurred())
	Expect(result).To(Equal("test-value"))
	Expect(callCount).To(Equal(1))
}

func TestCache_GetOrUpdate_CacheHit(t *testing.T) {
	RegisterTestingT(t)
	cache := NewCache[string](time.Minute)
	ctx := context.Background()

	callCount := 0
	fetch := func(ctx context.Context) (string, error) {
		callCount++
		return "test-value", nil
	}

	// First call should fetch
	result1, err1 := cache.GetOrUpdate(ctx, fetch)
	Expect(err1).ToNot(HaveOccurred())

	// Second call should use cache
	result2, err2 := cache.GetOrUpdate(ctx, fetch)
	Expect(err2).ToNot(HaveOccurred())

	Expect(result1).To(Equal(result2))
	Expect(callCount).To(Equal(1))
}

func TestCache_GetOrUpdate_CacheMiss_AfterTTL(t *testing.T) {
	RegisterTestingT(t)
	cache := NewCache[string](10 * time.Millisecond)
	ctx := context.Background()

	callCount := 0
	fetch := func(ctx context.Context) (string, error) {
		callCount++
		return "test-value", nil
	}

	// First call
	_, err1 := cache.GetOrUpdate(ctx, fetch)
	Expect(err1).ToNot(HaveOccurred())

	// Wait for TTL to expire
	time.Sleep(20 * time.Millisecond)

	// Second call should fetch again
	result2, err2 := cache.GetOrUpdate(ctx, fetch)
	Expect(err2).ToNot(HaveOccurred())
	Expect(result2).To(Equal("test-value"))
	Expect(callCount).To(Equal(2))
}

func TestCache_GetOrUpdate_FetchError(t *testing.T) {
	RegisterTestingT(t)
	cache := NewCache[string](time.Minute)
	ctx := context.Background()

	expectedErr := errors.New("fetch failed")
	fetch := func(ctx context.Context) (string, error) {
		return "", expectedErr
	}

	result, err := cache.GetOrUpdate(ctx, fetch)

	Expect(err).To(Equal(expectedErr))
	Expect(result).To(BeEmpty())
}

func TestCache_GetOrUpdate_FetchErrorDoesNotCache(t *testing.T) {
	RegisterTestingT(t)
	cache := NewCache[string](time.Minute)
	ctx := context.Background()

	callCount := 0
	fetch := func(ctx context.Context) (string, error) {
		callCount++
		if callCount == 1 {
			return "", errors.New("first call fails")
		}
		return "success", nil
	}

	// First call should fail
	_, err1 := cache.GetOrUpdate(ctx, fetch)
	Expect(err1).To(HaveOccurred())

	// Second call should succeed (error wasn't cached)
	result2, err2 := cache.GetOrUpdate(ctx, fetch)
	Expect(err2).ToNot(HaveOccurred())
	Expect(result2).To(Equal("success"))
	Expect(callCount).To(Equal(2))
}

func TestCache_GetOrUpdate_ConcurrentAccess(t *testing.T) {
	RegisterTestingT(t)
	cache := NewCache[string](time.Minute)
	ctx := context.Background()

	var callCount int64
	fetch := func(ctx context.Context) (string, error) {
		atomic.AddInt64(&callCount, 1)
		// Add small delay to make race condition more likely
		time.Sleep(10 * time.Millisecond)
		return "test-value", nil
	}

	const numGoroutines = 10
	var wg sync.WaitGroup
	results := make([]string, numGoroutines)
	errors := make([]error, numGoroutines)

	// Start multiple goroutines calling GetOrUpdate simultaneously
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			result, err := cache.GetOrUpdate(ctx, fetch)
			results[index] = result
			errors[index] = err
		}(i)
	}

	wg.Wait()

	// Check that all calls succeeded
	for i, err := range errors {
		Expect(err).ToNot(HaveOccurred(), "Goroutine %d should not have failed", i)
	}

	// Check that all results are the same
	for i, result := range results {
		Expect(result).To(Equal("test-value"), "Goroutine %d should get correct result", i)
	}

	// Check that fetch was called only once (due to double-check locking)
	finalCallCount := atomic.LoadInt64(&callCount)
	Expect(finalCallCount).To(Equal(int64(1)))
}

func TestCache_GetOrUpdate_ContextCancellation(t *testing.T) {
	RegisterTestingT(t)
	cache := NewCache[string](time.Minute)
	ctx, cancel := context.WithCancel(context.Background())

	fetch := func(ctx context.Context) (string, error) {
		// Check if context was cancelled during fetch
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(50 * time.Millisecond):
			return "test-value", nil
		}
	}

	// Cancel context immediately
	cancel()

	result, err := cache.GetOrUpdate(ctx, fetch)

	Expect(err).To(Equal(context.Canceled))
	Expect(result).To(BeEmpty())
}

func TestCache_GetOrUpdate_ZeroTTL(t *testing.T) {
	RegisterTestingT(t)
	cache := NewCache[string](0) // Zero TTL means always expired
	ctx := context.Background()

	callCount := 0
	fetch := func(ctx context.Context) (string, error) {
		callCount++
		return "test-value", nil
	}

	// First call
	_, err1 := cache.GetOrUpdate(ctx, fetch)
	Expect(err1).ToNot(HaveOccurred())

	// Second call should fetch again due to zero TTL
	_, err2 := cache.GetOrUpdate(ctx, fetch)
	Expect(err2).ToNot(HaveOccurred())

	Expect(callCount).To(Equal(2), "Fetch should be called twice with zero TTL")
}

func TestCache_GetOrUpdate_DifferentTypes(t *testing.T) {
	RegisterTestingT(t)

	// Test with int type
	intCache := NewCache[int](time.Minute)
	ctx := context.Background()

	intFetch := func(ctx context.Context) (int, error) {
		return 42, nil
	}

	intResult, err := intCache.GetOrUpdate(ctx, intFetch)
	Expect(err).ToNot(HaveOccurred())
	Expect(intResult).To(Equal(42))

	// Test with struct type
	type TestStruct struct {
		Name  string
		Value int
	}

	structCache := NewCache[TestStruct](time.Minute)
	expected := TestStruct{Name: "test", Value: 123}

	structFetch := func(ctx context.Context) (TestStruct, error) {
		return expected, nil
	}

	structResult, err := structCache.GetOrUpdate(ctx, structFetch)
	Expect(err).ToNot(HaveOccurred())
	Expect(structResult).To(Equal(expected))
}
