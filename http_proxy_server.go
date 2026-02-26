// Copyright 2019 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package goblet

import (
	"compress/gzip"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/gitprotocolio"
	"go.opencensus.io/tag"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type httpProxyServer struct {
	config *ServerConfig
}

func (s *httpProxyServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// CONNECT tunneling for HTTPS requests (used by Git LFS and other
	// HTTPS clients going through this HTTP proxy). Must be handled before
	// any Git-specific logic since CONNECT requests have no URL path.
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}

	// In AWS, ALBs strips the "scheme", "host" bits from the request URI.
	// Goblet requires this data (especially the "host" bit) to construct
	// the upstream URL. Thus, we re-construct the original URI using the
	// request Host header and assuming that all upstream locations will
	// accept HTTPs.
	if r.URL.Host == "" {
		r.URL.Host = r.Host
		r.URL.Scheme = "https"
	}

	w, logCloser := logHTTPRequest(s.config, w, r)
	defer logCloser()
	reporter := &httpErrorReporter{config: s.config, req: r, w: w}

	ctx, err := tag.New(r.Context(), tag.Insert(CommandTypeKey, "not-a-command"))
	if err != nil {
		reporter.reportError(err)
		return
	}
	r = r.WithContext(ctx)

	// Extract CI-Source
	ci_source := r.Header.Get("CI-Source")
	if len(ci_source) == 0 {
		ci_source = r.UserAgent()
	}

	// Technically, this server is an HTTP proxy, and it should use
	// Proxy-Authorization / Proxy-Authenticate. However, existing
	// authentication mechanism around Git is not compatible with proxy
	// authorization. We use normal authentication mechanism here.
	if err := s.config.RequestAuthorizer(r); err != nil {
		reporter.reportError(err)
		return
	}
	if isLFSRequest(r) {
		s.lfsProxyHandler(w, r)
		return
	}
	if proto := r.Header.Get("Git-Protocol"); proto != "version=2" {
		reporter.reportError(status.Errorf(codes.InvalidArgument, "accepts only Git protocol v2, received %v", proto))
		return
	}

	switch {
	case strings.HasSuffix(r.URL.Path, "/info/refs"):
		s.infoRefsHandler(reporter, w, r)
	case strings.HasSuffix(r.URL.Path, "/git-receive-pack"):
		reporter.reportError(status.Error(codes.Unimplemented, "git-receive-pack not supported"))
	case strings.HasSuffix(r.URL.Path, "/git-upload-pack"):
		s.uploadPackHandler(reporter, w, r, ci_source)
	}
}

var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authorization",
	"Proxy-Connection",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

func isLFSRequest(r *http.Request) bool {
	return strings.Contains(r.URL.Path, "/info/lfs")
}

func (s *httpProxyServer) handleConnect(w http.ResponseWriter, r *http.Request) {
	log.Printf("CONNECT tunnel: requested to %s", r.Host)

	targetConn, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		log.Printf("CONNECT tunnel: failed to dial %s: %v", r.Host, err)
		http.Error(w, "failed to connect to upstream", http.StatusBadGateway)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		targetConn.Close()
		log.Printf("CONNECT tunnel: hijacking not supported")
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}

	// Send 200 to the client before hijacking so the client knows the
	// tunnel is established and can begin the TLS handshake.
	w.WriteHeader(http.StatusOK)

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		targetConn.Close()
		log.Printf("CONNECT tunnel: hijack failed: %v", err)
		return
	}

	log.Printf("CONNECT tunnel: established to %s", r.Host)

	done := make(chan struct{}, 2)
	go func() {
		io.Copy(targetConn, clientConn)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(clientConn, targetConn)
		done <- struct{}{}
	}()

	<-done
	clientConn.Close()
	targetConn.Close()
	<-done

	log.Printf("CONNECT tunnel: closed to %s", r.Host)
}

func (s *httpProxyServer) lfsProxyHandler(w http.ResponseWriter, r *http.Request) {
	upstreamURL := *r.URL
	upstreamURL.Scheme = "https"

	log.Printf("LFS proxy: %s %s -> %s", r.Method, r.URL.String(), upstreamURL.String())

	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL.String(), r.Body)
	if err != nil {
		log.Printf("LFS proxy: failed to create upstream request: %v", err)
		http.Error(w, "failed to create upstream LFS request", http.StatusBadGateway)
		return
	}

	for key, values := range r.Header {
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}
	for _, h := range hopByHopHeaders {
		proxyReq.Header.Del(h)
	}

	startTime := time.Now()
	resp, err := http.DefaultClient.Do(proxyReq)
	if err != nil {
		log.Printf("LFS proxy: upstream request failed after %s: %v", time.Since(startTime), err)
		http.Error(w, "failed to reach upstream for LFS", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	log.Printf("LFS proxy: upstream responded %d in %s (content-length: %s)", resp.StatusCode, time.Since(startTime), resp.Header.Get("Content-Length"))

	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)
	written, err := io.Copy(w, resp.Body)
	if err != nil {
		log.Printf("LFS proxy: error copying response body: %v (wrote %d bytes)", err, written)
		return
	}

	log.Printf("LFS proxy: completed %s %s -> %d (%d bytes, %s)", r.Method, r.URL.Path, resp.StatusCode, written, time.Since(startTime))
}

func (s *httpProxyServer) infoRefsHandler(reporter *httpErrorReporter, w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("service") != "git-upload-pack" {
		reporter.reportError(status.Error(codes.InvalidArgument, "accepts only git-fetch"))
		return
	}

	w.Header().Add("Content-Type", "application/x-git-upload-pack-advertisement")
	rs := []*gitprotocolio.InfoRefsResponseChunk{
		{ProtocolVersion: 2},
		{Capabilities: []string{"ls-refs"}},
		// See managed_repositories.go for not having ref-in-want.
		{Capabilities: []string{"fetch=filter shallow"}},
		{Capabilities: []string{"server-option"}},
		{EndOfRequest: true},
	}
	for _, pkt := range rs {
		if err := writePacket(w, pkt); err != nil {
			// Client-side IO error. Treat this as Canceled.
			reporter.reportError(status.Errorf(codes.Canceled, "client IO error"))
			return
		}
	}
}

func (s *httpProxyServer) uploadPackHandler(reporter *httpErrorReporter, w http.ResponseWriter, r *http.Request, ci_source string) {
	// /git-upload-pack doesn't recognize text/plain error. Send an error
	// with ErrorPacket.
	w.Header().Add("Content-Type", "application/x-git-upload-pack-result")
	if r.Header.Get("Content-Encoding") == "gzip" {
		var err error
		if r.Body, err = gzip.NewReader(r.Body); err != nil {
			reporter.reportError(status.Errorf(codes.InvalidArgument, "cannot ungzip: %v", err))
			return
		}
	}

	// HTTP is strictly speaking a request-response protocol, and a server
	// cannot send a non-error response until the entire request is read.
	// We need to compromise and either drain the entire request first or
	// buffer the entire response.
	//
	// Because this server supports only ls-refs and fetch commands, valid
	// protocol V2 requests are relatively small in practice compared to the
	// response. A request with many wants and haves can be large, but
	// practically there's a limit on the number of haves a client would
	// send. Compared to that the fetch response can contain a packfile, and
	// this can easily get large. Read the entire request upfront.
	commands, err := parseAllCommands(r.Body)
	if err != nil {
		reporter.reportError(err)
		return
	}

	repo, err := openManagedRepository(s.config, r.URL)
	if err != nil {
		reporter.reportError(err)
		return
	}

	gitReporter := &gitProtocolHTTPErrorReporter{config: s.config, req: r, w: w}
	for i, command := range commands {
		tags := generateV2RequestMetricTags(command, repo)
		startTime := time.Now()
		if !handleV2Command(r.Context(), gitReporter, repo, command, w, ci_source) {
			log.Printf("Failed to handle V2 Request (command %d/%d) (CI: %s, repo:%s)\n", i+1, len(commands), ci_source, repo.upstreamURL)
			duration := time.Since(startTime)
			tags = append(tags, "success:0")
			StatsdClient.Distribution("goblet.v2request.dist", duration.Seconds(), tags, 1)
			return
		} else {
			duration := time.Since(startTime)
			tags = append(tags, "success:1")
			StatsdClient.Distribution("goblet.v2request.dist", duration.Seconds(), tags, 1)
		}
	}
}

func parseAllCommands(r io.Reader) ([][]*gitprotocolio.ProtocolV2RequestChunk, error) {
	commands := [][]*gitprotocolio.ProtocolV2RequestChunk{}
	v2Req := gitprotocolio.NewProtocolV2Request(r)
	for {
		chunks := []*gitprotocolio.ProtocolV2RequestChunk{}
		for v2Req.Scan() {
			c := copyRequestChunk(v2Req.Chunk())
			if c.EndRequest {
				break
			}
			chunks = append(chunks, c)
		}
		if len(chunks) == 0 || v2Req.Err() != nil {
			break
		}

		switch chunks[0].Command {
		case "ls-refs":
		case "fetch":
			// Do nothing.
		default:
			return nil, status.Errorf(codes.InvalidArgument, "unrecognized command: %v", chunks[0])
		}
		commands = append(commands, chunks)
	}

	if err := v2Req.Err(); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "cannot parse the request: %v", err)
	}
	return commands, nil
}
