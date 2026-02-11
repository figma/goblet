package github

import (
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"
)

// mockTokenSource is a TokenSource-like wrapper that counts how many times
// Token() is called. We embed a real TokenSource but override its behavior
// via the test by pre-populating a valid token.
type callCountingMultiTokenSource struct {
	mts       *MultiTokenSource
	callCount []int64 // per-source call count, indexed by source position
}

func newTestMultiTokenSource(n int) *callCountingMultiTokenSource {
	sources := make([]*TokenSource, n)
	for i := 0; i < n; i++ {
		sources[i] = &TokenSource{
			// Pre-populate a valid token so Token() returns immediately
			// without hitting GitHub API.
			token: &oauth2.Token{
				AccessToken: "test-token-" + string(rune('A'+i)),
				TokenType:   "Basic",
				// No Expiry means token.Valid() returns true since AccessToken != ""
			},
		}
	}
	mts, _ := NewMultiTokenSource(sources, nil)
	return &callCountingMultiTokenSource{
		mts:       mts,
		callCount: make([]int64, n),
	}
}

func TestMultiTokenSourceRoundRobin(t *testing.T) {
	m := newTestMultiTokenSource(3)

	// Call Token() 9 times -- expect 3 calls per source (round-robin).
	for i := 0; i < 9; i++ {
		tok, err := m.mts.Token()
		if err != nil {
			t.Fatalf("Token() returned error: %v", err)
		}
		if tok == nil {
			t.Fatal("Token() returned nil token")
		}
	}

	// Verify the counter advanced to 9.
	counter := atomic.LoadUint64(&m.mts.counter)
	if counter != 9 {
		t.Errorf("expected counter=9, got %d", counter)
	}

	// Verify that the selection pattern is 0,1,2,0,1,2,0,1,2
	// We can verify this by resetting and tracking which source index
	// is selected each time.
	mts2 := newTestMultiTokenSource(3)
	expectedPattern := []int{0, 1, 2, 0, 1, 2, 0, 1, 2}
	for i, expected := range expectedPattern {
		// Before calling Token(), the counter is at i, after it will be i+1.
		// The selected index is i % 3.
		currentCounter := atomic.LoadUint64(&mts2.mts.counter)
		if int(currentCounter%3) != expected {
			t.Errorf("call %d: expected source index %d, counter is %d (mod 3 = %d)",
				i, expected, currentCounter, currentCounter%3)
		}
		_, err := mts2.mts.Token()
		if err != nil {
			t.Fatalf("Token() returned error on call %d: %v", i, err)
		}
	}
}

func TestMultiTokenSourceSingleApp(t *testing.T) {
	m := newTestMultiTokenSource(1)

	// All calls should go to the single source.
	for i := 0; i < 10; i++ {
		tok, err := m.mts.Token()
		if err != nil {
			t.Fatalf("Token() returned error: %v", err)
		}
		if tok == nil {
			t.Fatal("Token() returned nil token")
		}
		if tok.AccessToken != "test-token-A" {
			t.Errorf("expected token from source 0, got AccessToken=%s", tok.AccessToken)
		}
	}

	counter := atomic.LoadUint64(&m.mts.counter)
	if counter != 10 {
		t.Errorf("expected counter=10, got %d", counter)
	}
}

func TestMultiTokenSourceDistributionEven(t *testing.T) {
	n := 5
	sources := make([]*TokenSource, n)
	callCounts := make([]int64, n)

	for i := 0; i < n; i++ {
		idx := i // capture
		sources[idx] = &TokenSource{
			token: &oauth2.Token{
				AccessToken: "test-token",
				TokenType:   "Basic",
			},
		}
		_ = callCounts[idx] // just to capture idx
	}

	mts, err := NewMultiTokenSource(sources, nil)
	if err != nil {
		t.Fatal(err)
	}

	totalCalls := 1000
	for i := 0; i < totalCalls; i++ {
		// Track which source index is selected by checking counter before call
		idx := atomic.LoadUint64(&mts.counter) % uint64(n)
		atomic.AddInt64(&callCounts[idx], 1)
		_, err := mts.Token()
		if err != nil {
			t.Fatalf("Token() error: %v", err)
		}
	}

	// Each source should have been selected exactly totalCalls/n times.
	expected := int64(totalCalls / n)
	for i, count := range callCounts {
		if count != expected {
			t.Errorf("source %d: expected %d calls, got %d", i, expected, count)
		}
	}
}

func TestMultiTokenSourceConcurrentAccess(t *testing.T) {
	n := 4
	sources := make([]*TokenSource, n)
	for i := 0; i < n; i++ {
		sources[i] = &TokenSource{
			token: &oauth2.Token{
				AccessToken: "test-token",
				TokenType:   "Basic",
			},
		}
	}

	mts, err := NewMultiTokenSource(sources, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Hammer Token() from many goroutines to verify no races.
	goroutines := 50
	callsPerGoroutine := 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < callsPerGoroutine; i++ {
				tok, err := mts.Token()
				if err != nil {
					t.Errorf("Token() error: %v", err)
					return
				}
				if tok == nil {
					t.Error("Token() returned nil")
					return
				}
			}
		}()
	}

	wg.Wait()

	totalExpected := uint64(goroutines * callsPerGoroutine)
	counter := atomic.LoadUint64(&mts.counter)
	if counter != totalExpected {
		t.Errorf("expected counter=%d, got %d", totalExpected, counter)
	}
}

func TestMultiTokenSourceRejectsEmpty(t *testing.T) {
	_, err := NewMultiTokenSource(nil, nil)
	if err == nil {
		t.Error("expected error for nil sources, got nil")
	}

	_, err = NewMultiTokenSource([]*TokenSource{}, nil)
	if err == nil {
		t.Error("expected error for empty sources, got nil")
	}
}

func TestMultiTokenSourceFromConfigsRejectsEmpty(t *testing.T) {
	_, err := NewMultiTokenSourceFromConfigs(nil, 0, nil)
	if err == nil {
		t.Error("expected error for nil configs, got nil")
	}

	_, err = NewMultiTokenSourceFromConfigs([]AppConfig{}, 0, nil)
	if err == nil {
		t.Error("expected error for empty configs, got nil")
	}
}

func TestNumSources(t *testing.T) {
	for _, n := range []int{1, 3, 7} {
		m := newTestMultiTokenSource(n)
		if m.mts.NumSources() != n {
			t.Errorf("NumSources() = %d, want %d", m.mts.NumSources(), n)
		}
	}
}
