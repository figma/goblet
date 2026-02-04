package goblet

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoWithRetry_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := DoWithRetry(http.DefaultClient, req)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got: %d", resp.StatusCode)
	}
	defer resp.Body.Close()
}

func TestDoWithRetry_RetryAfterHeader(t *testing.T) {
	var attemptCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attemptCount, 1)
		if count < 3 {
			w.Header().Set("Retry-After", "1") // 1 second
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))
	defer server.Close()

	start := time.Now()
	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := DoWithRetry(http.DefaultClient, req)

	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got: %d", resp.StatusCode)
	}
	if atomic.LoadInt32(&attemptCount) != 3 {
		t.Fatalf("Expected 3 attempts, got: %d", attemptCount)
	}
	// Should have waited at least 2 seconds (2 retries with 1 second each)
	if elapsed < 2*time.Second {
		t.Fatalf("Expected at least 2 seconds wait, got: %v", elapsed)
	}
	defer resp.Body.Close()
}

func TestDoWithRetry_MaxRetries(t *testing.T) {
	var attemptCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attemptCount, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := DoWithRetry(http.DefaultClient, req)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("Expected status 429, got: %d", resp.StatusCode)
	}
	// Should try: initial + 3 retries = 4 total attempts
	if atomic.LoadInt32(&attemptCount) != 4 {
		t.Fatalf("Expected 4 attempts (initial + 3 retries), got: %d", attemptCount)
	}
	defer resp.Body.Close()
}

func TestDoWithRetry_Status403Retry(t *testing.T) {
	var attemptCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attemptCount, 1)
		if count < 2 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := DoWithRetry(http.DefaultClient, req)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got: %d", resp.StatusCode)
	}
	if atomic.LoadInt32(&attemptCount) != 2 {
		t.Fatalf("Expected 2 attempts, got: %d", attemptCount)
	}
	defer resp.Body.Close()
}

func TestDoWithRetry_Status5xxRetry(t *testing.T) {
	var attemptCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attemptCount, 1)
		if count < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := DoWithRetry(http.DefaultClient, req)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got: %d", resp.StatusCode)
	}
	if atomic.LoadInt32(&attemptCount) != 2 {
		t.Fatalf("Expected 2 attempts, got: %d", attemptCount)
	}
	defer resp.Body.Close()
}

func TestDoWithRetry_NoRetryOn501(t *testing.T) {
	var attemptCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attemptCount, 1)
		w.WriteHeader(http.StatusNotImplemented)
	}))
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := DoWithRetry(http.DefaultClient, req)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("Expected status 501, got: %d", resp.StatusCode)
	}
	// Should NOT retry on 501
	if atomic.LoadInt32(&attemptCount) != 1 {
		t.Fatalf("Expected 1 attempt (no retry), got: %d", attemptCount)
	}
	defer resp.Body.Close()
}

func TestDoWithRetry_MaxWaitTime(t *testing.T) {
	var attemptCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attemptCount, 1)
		if count < 2 {
			w.Header().Set("Retry-After", "120") // 120 seconds (should be capped at 60)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	start := time.Now()
	req, _ := http.NewRequest("GET", server.URL, nil)
	resp, err := DoWithRetry(http.DefaultClient, req)

	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got: %d", resp.StatusCode)
	}
	// Should have waited max 60 seconds, not 120
	if elapsed >= 90*time.Second {
		t.Fatalf("Expected wait time capped at ~60 seconds, got: %v", elapsed)
	}
	if elapsed < 60*time.Second {
		t.Fatalf("Expected wait time of ~60 seconds, got: %v", elapsed)
	}
	defer resp.Body.Close()
}

func TestParseRetryAfter_Integer(t *testing.T) {
	duration := parseRetryAfter("5")
	if duration != 5*time.Second {
		t.Fatalf("Expected 5 seconds, got: %v", duration)
	}
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	future := time.Now().Add(10 * time.Second)
	retryAfter := future.Format(time.RFC1123)
	duration := parseRetryAfter(retryAfter)

	// Allow some tolerance for timing
	if duration < 9*time.Second || duration > 11*time.Second {
		t.Fatalf("Expected ~10 seconds, got: %v", duration)
	}
}

func TestParseRetryAfter_Empty(t *testing.T) {
	duration := parseRetryAfter("")
	if duration != 0 {
		t.Fatalf("Expected 0, got: %v", duration)
	}
}

func TestParseRetryAfter_Invalid(t *testing.T) {
	duration := parseRetryAfter("invalid")
	if duration != 0 {
		t.Fatalf("Expected 0, got: %v", duration)
	}
}

func TestShouldRetry(t *testing.T) {
	tests := []struct {
		statusCode int
		expected   bool
	}{
		{200, false},
		{201, false},
		{400, false},
		{401, false},
		{403, true},
		{404, false},
		{429, true},
		{500, true},
		{501, false},
		{502, true},
		{503, true},
		{504, true},
	}

	for _, test := range tests {
		result := shouldRetry(test.statusCode)
		if result != test.expected {
			t.Errorf("shouldRetry(%d) = %v, expected %v", test.statusCode, result, test.expected)
		}
	}
}
