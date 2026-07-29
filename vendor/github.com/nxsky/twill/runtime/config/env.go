// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package config

import (
	"context"
	"os"
)

// EnvLoader loads config values from environment variables. Keys are mapped to
// env names with EnvKey using the configured prefix.
type EnvLoader struct {
	Prefix string
}

// NewEnvLoader returns an env loader with the given prefix.
func NewEnvLoader(prefix string) *EnvLoader {
	return &EnvLoader{Prefix: prefix}
}

// Get implements Loader.
func (l *EnvLoader) Get(ctx context.Context, key string) (string, bool) {
	if l == nil {
		return "", false
	}
	return os.LookupEnv(EnvKey(l.Prefix, key))
}

// Unmarshal implements Loader. Env loaders do not support structured
// unmarshaling; it always returns nil without changing dst.
func (l *EnvLoader) Unmarshal(ctx context.Context, key string, dst any) error {
	return nil
}
