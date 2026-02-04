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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"gopkg.in/square/go-jose.v2"
	"gopkg.in/square/go-jose.v2/jwt"
)

// GenerateOAuthTokenFromApp generates a GitHub OAuth access token from a set of valid GitHub App credentials. The
// returned token can be used to interact with both GitHub's REST and GraphQL APIs.
func GenerateOAuthTokenFromApp(appID, installationID string, privateKey *rsa.PrivateKey) (oauth2.Token, error) {
	appJWT, err := generateAppJWT(appID, time.Now(), privateKey)
	if err != nil {
		return oauth2.Token{}, err
	}

	token, err := getInstallationAccessToken(appJWT, installationID)
	if err != nil {
		return oauth2.Token{}, err
	}

	return token, nil
}

func getInstallationAccessToken(jwt string, installationID string) (oauth2.Token, error) {
	url := fmt.Sprintf("https://api.github.com/app/installations/%s/access_tokens", installationID)

	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return oauth2.Token{}, err
	}

	req.Header.Add("Accept", "application/vnd.github.v3+json")
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", jwt))

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("GitHub App token request failed (url:%s, err:%v)\n", url, err)
		return oauth2.Token{}, err
	}
	defer func() { _ = res.Body.Close() }()

	resBytes, err := ioutil.ReadAll(res.Body)
	if err != nil {
		log.Printf("GitHub App token response read failed (url:%s, err:%v)\n", url, err)
		return oauth2.Token{}, err
	}

	// Log response headers
	log.Printf("GitHub App token response (url:%s, status:%d, headers:%v)\n", url, res.StatusCode, res.Header)

	if res.StatusCode != http.StatusCreated {
		log.Printf("GitHub App token creation failed with non-OK response (url:%s, status:%d, content-type:%s, error:%s)\n", 
			url, res.StatusCode, res.Header.Get("Content-Type"), string(resBytes))
		return oauth2.Token{}, fmt.Errorf("failed to create OAuth token from GitHub App: %s", string(resBytes))
	}

	resData := struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}{}

	err = json.Unmarshal(resBytes, &resData)
	if err != nil {
		return oauth2.Token{}, err
	}

	exp, err := time.Parse(time.RFC3339, resData.ExpiresAt)
	if err != nil {
		return oauth2.Token{}, nil
	}

	return oauth2.Token{
		AccessToken: base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("x-access-token:%s", resData.Token))),
		TokenType:   "Basic",
		Expiry:      exp,
	}, nil
}

func generateAppJWT(appID string, now time.Time, privateKey *rsa.PrivateKey) (string, error) {
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: privateKey},
		(&jose.SignerOptions{}).WithType("JWT"),
	)

	if err != nil {
		return "", err
	}

	claims := &jwt.Claims{
		Issuer: appID,
		// Using now - 60s to accommodate any client/server clock drift.
		IssuedAt: jwt.NewNumericDate(now.Add(time.Duration(-60) * time.Second)),
		// The JWT's lifetime can be short as it is only used immediately
		// after to retrieve the installation's access  token.
		Expiry: jwt.NewNumericDate(now.Add(time.Duration(5) * time.Minute)),
	}

	token, err := jwt.Signed(signer).Claims(claims).CompactSerialize()
	if err != nil {
		return "", err
	}

	return token, nil
}
