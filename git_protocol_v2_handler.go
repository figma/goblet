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
	"bytes"
	"context"
	"io"
	"log"
	"strings"
	"time"

	"github.com/google/gitprotocolio"
	git "github.com/libgit2/git2go/v33"
	"go.opencensus.io/stats"
	"go.opencensus.io/tag"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type gitProtocolErrorReporter interface {
	reportError(context.Context, time.Time, error)
}

func handleV2Command(ctx context.Context, reporter gitProtocolErrorReporter, repo *managedRepository, command []*gitprotocolio.ProtocolV2RequestChunk, w io.Writer, ci_source string) bool {
	startTime := time.Now()
	var err error
	ctx, err = tag.New(ctx, tag.Upsert(CommandTypeKey, command[0].Command))
	if err != nil {
		reporter.reportError(ctx, startTime, err)
		return false
	}

	cacheState := "locally-served"
	ctx, err = tag.New(ctx, tag.Upsert(CommandCacheStateKey, cacheState))
	if err != nil {
		reporter.reportError(ctx, startTime, err)
		return false
	}

	logV2Request(command, repo)

	switch command[0].Command {
	case "ls-refs":
		ctx, err = tag.New(ctx, tag.Update(CommandCacheStateKey, "queried-upstream"))
		if err != nil {
			reporter.reportError(ctx, startTime, err)
			return false
		}

		resp, err := repo.lsRefsUpstream(command)
		if err != nil {
			reporter.reportError(ctx, startTime, err)
			return false
		}

		// refs, err := parseLsRefsResponse(resp)
		// if err != nil {
		// 	reporter.reportError(ctx, startTime, err)
		// 	return false
		// }

		// if hasUpdate, err := repo.hasAnyUpdate(refs); err != nil {
		// 	reporter.reportError(ctx, startTime, err)
		// 	return false
		// } else if hasUpdate {
		// 	repo.fetchUpstreamPool.Submit(func() {
		// 		// check again when the task is picked up
		// 		hasUpdate, err := repo.hasAnyUpdate(refs)
		// 		if err == nil {
		// 			if hasUpdate {
		// 				log.Printf("FetchUpstream required since refs are not satisfied (%s)\n", repo.localDiskPath)
		// 				StatsdClient.Incr("goblet.operation.count", []string{"dir:" + repo.localDiskPath, "op:ondemand_fetch", "triggered_by:hasanyupdate"}, 1)
		// 				repo.fetchUpstream(nil)
		// 			} else {
		// 				log.Printf("FetchUpstream skipped since refs are satisfied (%s)\n", repo.localDiskPath)
		// 			}
		// 		}
		// 	})
		// }

		writeResp(w, resp)
		reporter.reportError(ctx, startTime, nil)
		return true

	case "fetch":
		wantHashes, wantRefs, err := parseFetchWants(command)
		if err != nil {
			reporter.reportError(ctx, startTime, err)
			return false
		}

		if hasAllWants, err := repo.hasAllWants(wantHashes, wantRefs); err != nil {
			reporter.reportError(ctx, startTime, err)
			return false
		} else if !hasAllWants {
			ctx, err = tag.New(ctx, tag.Update(CommandCacheStateKey, "queried-upsteam"))
			if err != nil {
				reporter.reportError(ctx, startTime, err)
				return false
			}

			fetchStartTime := time.Now()
			repo.fetchUpstreamPool.SubmitAndWait(func() {
				logElapsed("FetchUpstream queuing", fetchStartTime, time.Minute, repo.localDiskPath)

				// check again when the task is picked up
				hasAllWants, err := repo.hasAllWants(wantHashes, wantRefs)
				if err == nil {
					if !hasAllWants {
						log.Printf("FetchUpstream required since wants are not satisfied (%s)\n", repo.localDiskPath)
						StatsdClient.Incr("goblet.operation.count", []string{"dir:" + repo.localDiskPath, "op:ondemand_fetch", "triggered_by:hasallwants"}, 1)
						repo.fetchUpstream(wantHashes)
					} else {
						log.Printf("FetchUpstream skipped since wants are satisfied (%s)\n", repo.localDiskPath)
					}
				}
			})

			select {
			case <-ctx.Done():
				reporter.reportError(ctx, startTime, ctx.Err())
				log.Printf("ServeFetchLocal cancelled since request was closed (%s)\n", repo.localDiskPath)
				return false
			default:
				if hasAllWants, checkErr := repo.hasAllWants(wantHashes, wantRefs); checkErr != nil {
					log.Printf("ServeFetchLocal cancelled since wants throws error after fetch (%s %v)\n", repo.localDiskPath, checkErr)
					reporter.reportError(ctx, startTime, checkErr)
					return false
				} else if !hasAllWants {
					reporter.reportError(ctx, startTime, err)
					log.Printf("ServeFetchLocal cancelled since wants are not satisfied after fetch (%s)\n", repo.localDiskPath)
					return false
				}
			}
			stats.Record(ctx, UpstreamFetchWaitingTime.M(int64(time.Since(fetchStartTime)/time.Millisecond)))
		}

		errorChan := make(chan error, 1)
		serveStartTime := time.Now()
		repo.serveFetchPool.Submit(func() {
			logElapsed("ServeFetchLocal queuing", serveStartTime, time.Minute, repo.localDiskPath)
			errorChan <- repo.serveFetchLocal(command, w, ci_source)
		})

		err = <-errorChan
		reporter.reportError(ctx, startTime, err)
		return err == nil
	}
	reporter.reportError(ctx, startTime, status.Error(codes.InvalidArgument, "unknown command"))
	return false
}

func logV2Request(chunks []*gitprotocolio.ProtocolV2RequestChunk, repo *managedRepository) {
	var buffer bytes.Buffer
	for _, c := range chunks {
		buffer.Write(c.EncodeToPktLine())
		buffer.WriteRune(' ')
	}
	log.Printf("Received V2 Request: %s\n", buffer.String())

}

func generateV2RequestMetricTags(chunks []*gitprotocolio.ProtocolV2RequestChunk, repo *managedRepository) []string {
	var command string
	if len(chunks) > 0 {
		command = chunks[0].Command
	} else {
		command = "unknown"
	}
	tags := []string{
		"repo:" + repo.upstreamURL.String(),
		"command:" + strings.ToLower(strings.ReplaceAll(command, " ", "_")),
	}
	return tags
}

func parseLsRefsResponse(chunks []*gitprotocolio.ProtocolV2ResponseChunk) (map[string]git.Oid, error) {
	m := map[string]git.Oid{}
	for _, ch := range chunks {
		if ch.Response == nil {
			continue
		}
		ss := strings.Split(string(ch.Response), " ")
		if len(ss) < 2 {
			return nil, status.Errorf(codes.Internal, "cannot parse the upstream ls-refs response: got %d component, want at least 2", len(ss))
		}
		hash, err := git.NewOid(ss[0])
		if err != nil {
			return nil, status.Errorf(codes.Internal, "cannot parse the upstream ls-refs response: got invalid hash %s", ss[0])
		}
		m[strings.TrimSpace(ss[1])] = *hash
	}
	return m, nil
}

func parseFetchWants(chunks []*gitprotocolio.ProtocolV2RequestChunk) ([]git.Oid, []string, error) {
	hashes := []git.Oid{}
	refs := []string{}
	for _, ch := range chunks {
		if ch.Argument == nil {
			continue
		}
		s := string(ch.Argument)
		if strings.HasPrefix(s, "want ") {
			ss := strings.Split(s, " ")
			if len(ss) < 2 {
				return nil, nil, status.Errorf(codes.InvalidArgument, "cannot parse the fetch request: got %d component, want at least 2", len(ss))
			}
			hash, err := git.NewOid(strings.TrimSpace(ss[1]))
			if err != nil {
				return nil, nil, status.Errorf(codes.InvalidArgument, "cannot parse the upstream ls-refs response: got invalid hash %s", strings.TrimSpace(ss[1]))
			}
			hashes = append(hashes, *hash)
		} else if strings.HasPrefix(s, "want-ref ") {
			ss := strings.Split(s, " ")
			if len(ss) < 2 {
				return nil, nil, status.Errorf(codes.InvalidArgument, "cannot parse the fetch request: got %d component, want at least 2", len(ss))
			}
			refs = append(refs, strings.TrimSpace(ss[1]))
		}
	}
	return hashes, refs, nil
}
