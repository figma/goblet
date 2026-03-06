package end2end

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	goblettest "github.com/canva/goblet/testing"
)

type lfsBatchRequest struct {
	Operation string          `json:"operation"`
	Objects   []lfsBatchObject `json:"objects"`
}

type lfsBatchObject struct {
	OID  string `json:"oid"`
	Size int64  `json:"size"`
}

type lfsBatchResponse struct {
	Transfer string `json:"transfer"`
	Objects  []struct {
		OID     string `json:"oid"`
		Size    int64  `json:"size"`
		Actions map[string]struct {
			Href string `json:"href"`
		} `json:"actions"`
	} `json:"objects"`
}

func TestLFSBatch_ProxiesRequest(t *testing.T) {
	ts := goblettest.NewTestServer(&goblettest.TestServerConfig{
		RequestAuthorizer: goblettest.TestRequestAuthorizer,
		TokenSource:       goblettest.TestTokenSource,
	})
	defer ts.Close()

	reqBody := lfsBatchRequest{
		Operation: "download",
		Objects: []lfsBatchObject{
			{OID: "abc123", Size: 100},
			{OID: "def456", Size: 200},
		},
	}
	body, _ := json.Marshal(reqBody)

	url := ts.ProxyServerURL + "repo.git/info/lfs/objects/batch"
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	req.Header.Set("Accept", "application/vnd.git-lfs+json")
	req.Header.Set("Authorization", "Bearer "+goblettest.ValidClientAuthToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	var batchResp lfsBatchResponse
	if err := json.NewDecoder(resp.Body).Decode(&batchResp); err != nil {
		t.Fatal(err)
	}

	if len(batchResp.Objects) != 2 {
		t.Fatalf("expected 2 objects, got %d", len(batchResp.Objects))
	}
	for i, obj := range batchResp.Objects {
		if obj.OID != reqBody.Objects[i].OID {
			t.Errorf("object %d: expected OID %s, got %s", i, reqBody.Objects[i].OID, obj.OID)
		}
		dl, ok := obj.Actions["download"]
		if !ok {
			t.Errorf("object %d: missing download action", i)
		} else if dl.Href == "" {
			t.Errorf("object %d: empty download href", i)
		}
	}
}

func TestLFSBatch_RequiresAuth(t *testing.T) {
	ts := goblettest.NewTestServer(&goblettest.TestServerConfig{
		RequestAuthorizer: goblettest.TestRequestAuthorizer,
		TokenSource:       goblettest.TestTokenSource,
	})
	defer ts.Close()

	reqBody := lfsBatchRequest{
		Operation: "download",
		Objects:   []lfsBatchObject{{OID: "abc123", Size: 100}},
	}
	body, _ := json.Marshal(reqBody)

	url := ts.ProxyServerURL + "repo.git/info/lfs/objects/batch"
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/vnd.git-lfs+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestLFSBatch_UnsupportedPathReturns501(t *testing.T) {
	ts := goblettest.NewTestServer(&goblettest.TestServerConfig{
		RequestAuthorizer: goblettest.TestRequestAuthorizer,
		TokenSource:       goblettest.TestTokenSource,
	})
	defer ts.Close()

	url := ts.ProxyServerURL + "repo.git/info/lfs/locks"
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+goblettest.ValidClientAuthToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", resp.StatusCode)
	}
}
