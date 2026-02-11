// Copyright 2021 Canva Inc
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

package github

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/DataDog/datadog-go/statsd"
	"golang.org/x/oauth2"
)

// AppConfig holds credentials for a single GitHub App.
type AppConfig struct {
	AppID          string `json:"app_id"`
	InstallationID string `json:"installation_id"`
	PrivateKey     string `json:"private_key"`
}

// MultiTokenSource wraps N TokenSource instances and randomly selects one
// for each Token() call. Random selection ensures even distribution across
// multiple GitHub Apps without requiring coordination between ECS instances.
//
// MultiTokenSource implements oauth2.TokenSource.
type MultiTokenSource struct {
	sources      []*TokenSource
	rng          *rand.Rand
	mu           sync.Mutex
	statsdClient *statsd.Client
}

// Token returns a token from a randomly selected TokenSource.
func (m *MultiTokenSource) Token() (*oauth2.Token, error) {
	n := len(m.sources)
	var selected int
	if n == 1 {
		selected = 0
	} else {
		m.mu.Lock()
		selected = m.rng.Intn(n)
		m.mu.Unlock()
	}
	source := m.sources[selected]

	if m.statsdClient != nil {
		m.statsdClient.Incr("goblet.token.app_selected", []string{fmt.Sprintf("app_idx:%d", selected)}, 1)
	}

	return source.Token()
}

// NumSources returns the number of underlying TokenSource instances.
func (m *MultiTokenSource) NumSources() int {
	return len(m.sources)
}

// NewMultiTokenSource creates a MultiTokenSource from one or more TokenSource
// instances. The optional statsdClient is used to emit per-app selection metrics.
func NewMultiTokenSource(sources []*TokenSource, statsdClient *statsd.Client) (*MultiTokenSource, error) {
	if len(sources) == 0 {
		return nil, fmt.Errorf("at least one token source must be provided")
	}
	return &MultiTokenSource{
		sources:      sources,
		rng:          rand.New(rand.NewSource(time.Now().UnixNano())),
		statsdClient: statsdClient,
	}, nil
}

// NewMultiTokenSourceFromConfigs creates a MultiTokenSource from a slice of
// AppConfig. Each config produces one TokenSource. This is the primary
// constructor when configuring multiple GitHub Apps.
func NewMultiTokenSourceFromConfigs(configs []AppConfig, tokenExpiryDelta time.Duration, statsdClient *statsd.Client) (*MultiTokenSource, error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("at least one app config must be provided")
	}

	sources := make([]*TokenSource, 0, len(configs))
	for i, cfg := range configs {
		ts, err := NewTokenSource(cfg.AppID, cfg.InstallationID, cfg.PrivateKey, tokenExpiryDelta)
		if err != nil {
			return nil, fmt.Errorf("failed to create token source for app index %d (app_id=%s): %w", i, cfg.AppID, err)
		}
		sources = append(sources, ts)
	}

	return NewMultiTokenSource(sources, statsdClient)
}

type TokenSource struct {
	AppID          string
	InstallationID string
	PrivateKey     *rsa.PrivateKey

	tokenExpiryDelta time.Duration

	token *oauth2.Token
	mu    sync.Mutex
}

func (ts *TokenSource) Token() (*oauth2.Token, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.token.Valid() {
		currentTime := time.Now()
		if !ts.token.Expiry.IsZero() && ts.token.Expiry.Round(0).Add(-ts.tokenExpiryDelta).Before(currentTime) {
			log.Printf("Current OAuth token will expired in %s. Will regenerate.\n", ts.token.Expiry.Sub(currentTime))
		} else {
			return ts.token, nil
		}
	} else {
		log.Printf("Current OAuth token is not valid. Will regenerate.\n")
	}

	ts.token = nil

	newTok, err := GenerateOAuthTokenFromApp(ts.AppID, ts.InstallationID, ts.PrivateKey)
	if err == nil {
		ts.token = &newTok
		log.Printf("New OAuth token generated. Will expired at %s\n", ts.token.Expiry)
	} else {
		log.Printf("New OAuth token generation failed. %v\n", err)
	}

	return ts.token, nil
}

func NewTokenSource(appID string, installationID string, privateKey string, tokenExpiryDelta time.Duration) (*TokenSource, error) {
	if appID == "" {
		return nil, fmt.Errorf("github app id must be provided")
	}
	if installationID == "" {
		return nil, fmt.Errorf("github app installation id must be provided")
	}

	if privateKey == "" {
		return nil, fmt.Errorf("github app private key must be provided")
	}

	block, _ := pem.Decode([]byte(privateKey))
	pk, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}

	log.Printf("OAuth token will be discarded %s before its expiry\n", tokenExpiryDelta)

	return &TokenSource{
		AppID:            appID,
		InstallationID:   installationID,
		PrivateKey:       pk,
		tokenExpiryDelta: tokenExpiryDelta,
	}, nil
}
