package goblet

import (
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func isLFSRequest(r *http.Request) bool {
	return strings.Contains(r.URL.Path, "/info/lfs/")
}

func (s *httpProxyServer) lfsHandler(reporter *httpErrorReporter, w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/info/lfs/objects/batch") {
		reporter.reportError(status.Error(codes.Unimplemented, "only LFS batch API is supported"))
		return
	}

	if r.Method != http.MethodPost {
		reporter.reportError(status.Error(codes.InvalidArgument, "LFS batch API requires POST"))
		return
	}

	// Use the canonicalizer to resolve the upstream host/scheme, but
	// preserve the original request path since GitHub's LFS API is
	// case-sensitive (the GitHub canonicalizer lowercases the path).
	originalPath := r.URL.Path
	canonicalURL, err := s.config.URLCanonicalizer(r.URL)
	if err != nil {
		reporter.reportError(status.Errorf(codes.Internal, "cannot canonicalize URL: %v", err))
		return
	}
	upstreamURL := *canonicalURL
	upstreamURL.Path = originalPath

	t, err := s.config.TokenSource.Token()
	if err != nil {
		reporter.reportError(status.Errorf(codes.Internal, "cannot obtain OAuth2 token: %v", err))
		return
	}

	upstreamReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL.String(), r.Body)
	if err != nil {
		reporter.reportError(status.Errorf(codes.Internal, "cannot create upstream request: %v", err))
		return
	}

	upstreamReq.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	upstreamReq.Header.Set("Accept", "application/vnd.git-lfs+json")
	t.SetAuthHeader(upstreamReq)

	startTime := time.Now()
	resp, err := http.DefaultClient.Do(upstreamReq)
	if err != nil {
		log.Printf("LFS batch request failed (url:%s, err:%v)\n", upstreamURL.String(), err)
		reporter.reportError(status.Errorf(codes.Internal, "upstream LFS request failed: %v", err))
		return
	}
	defer resp.Body.Close()

	log.Printf("LFS batch response (url:%s, status:%d, duration:%s)\n", upstreamURL.String(), resp.StatusCode, time.Since(startTime))

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}
