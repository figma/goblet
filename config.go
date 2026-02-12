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

package goblet

import (
	"encoding/json"
	"io/ioutil"
)

// ConfigFile holds the configuration for Goblet server instances.
type ConfigFile struct {
	Port                    int      `json:"port"`
	CacheRoot               string   `json:"cache_root"`
	PeerLoadBalancer        string   `json:"peer_load_balancer,omitempty"`
	TokenExpiryDeltaSeconds int      `json:"token_expiry_delta_seconds"`
	EnableMetrics           bool     `json:"enable_metrics,omitempty"`
	PackObjectsHook         string   `json:"pack_objects_hook,omitempty"`
	PackObjectsCache        string   `json:"pack_objects_cache,omitempty"`
	Repositories            []string `json:"repositories,omitempty"`
}

// LoadConfigFile reads a Goblet configuration file.
func LoadConfigFile(path string) (ConfigFile, error) {
	file := ConfigFile{}
	bytes, err := ioutil.ReadFile(path)
	if err != nil {
		return file, err
	}
	err = json.Unmarshal(bytes, &file)
	return file, err
}
