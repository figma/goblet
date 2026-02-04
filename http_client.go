package goblet

import (
	"net/http"
	"strconv"
	"time"
)

const (
	maxRetries      = 3
	maxRetryAfter   = 60 * time.Second
)

// DoWithRetry executes an HTTP request with retry logic that respects GitHub's Retry-After header.
// It retries on 403, 429, and 5xx errors (except 501).
func DoWithRetry(client *http.Client, req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		// Clone the request for each attempt to ensure headers and body are fresh
		reqClone := req.Clone(req.Context())

		resp, err = client.Do(reqClone)

		// If no error and status is successful, return immediately
		if err == nil && !shouldRetry(resp.StatusCode) {
			return resp, nil
		}

		// Don't retry if we've exhausted our attempts
		if attempt >= maxRetries {
			return resp, err
		}

		// Calculate wait time from Retry-After header
		var waitDuration time.Duration
		if resp != nil {
			waitDuration = parseRetryAfter(resp.Header.Get("Retry-After"))
			// Close the response body before retrying
			resp.Body.Close()
		}

		// If no Retry-After header or error occurred, use exponential backoff
		if waitDuration == 0 {
			waitDuration = time.Duration(1<<uint(attempt)) * time.Second
		}

		// Cap the wait duration at maxRetryAfter
		if waitDuration > maxRetryAfter {
			waitDuration = maxRetryAfter
		}

		time.Sleep(waitDuration)
	}

	return resp, err
}

// shouldRetry determines if a status code should trigger a retry
func shouldRetry(statusCode int) bool {
	// Retry on 403 (sometimes used for rate limiting)
	if statusCode == http.StatusForbidden {
		return true
	}

	// Retry on 429 (Too Many Requests)
	if statusCode == http.StatusTooManyRequests {
		return true
	}

	// Retry on 5xx errors except 501 (Not Implemented)
	if statusCode >= 500 && statusCode < 600 && statusCode != http.StatusNotImplemented {
		return true
	}

	return false
}

// parseRetryAfter parses the Retry-After header value.
// It supports both integer seconds and HTTP date formats.
func parseRetryAfter(retryAfter string) time.Duration {
	if retryAfter == "" {
		return 0
	}

	// Try parsing as integer (seconds)
	if seconds, err := strconv.Atoi(retryAfter); err == nil {
		return time.Duration(seconds) * time.Second
	}

	// Try parsing as HTTP date (RFC 1123)
	if t, err := time.Parse(time.RFC1123, retryAfter); err == nil {
		duration := time.Until(t)
		if duration > 0 {
			return duration
		}
	}

	return 0
}
