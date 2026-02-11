package github

import (
	"log"
	"net/http"
)

// logGitHubRateLimitHeaders logs GitHub rate limit information from response headers
func logGitHubRateLimitHeaders(operation, url string, res *http.Response) {
	limit := res.Header.Get("X-RateLimit-Limit")
	remaining := res.Header.Get("X-RateLimit-Remaining")
	used := res.Header.Get("X-RateLimit-Used")
	reset := res.Header.Get("X-RateLimit-Reset")
	resource := res.Header.Get("X-RateLimit-Resource")

	if limit != "" || remaining != "" {
		log.Printf("[GitHub Rate Limit] operation=%s, url=%s, status=%d, limit=%s, remaining=%s, used=%s, reset=%s, resource=%s\n",
			operation, url, res.StatusCode, limit, remaining, used, reset, resource)
	} else {
		// Some endpoints might not return rate limit headers
		log.Printf("[GitHub Response] operation=%s, url=%s, status=%d (no rate limit headers)\n",
			operation, url, res.StatusCode)
	}
}

// truncateString truncates a string to maxLen characters
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
