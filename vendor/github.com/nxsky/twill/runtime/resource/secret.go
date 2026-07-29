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

package resource

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Secret is an abstraction for resolving sensitive values like API keys,
// passwords, and tokens. Implementations should never log or expose secret
// values in error messages.
type Secret interface {
	// Get returns the secret value for the named key.
	Get(ctx context.Context, key string) (string, error)
}

// NewEnvSecret returns a Secret backed by environment variables. Keys are
// mapped to env names by upper-casing, replacing dashes and dots with
// underscores, and prepending the prefix.
func NewEnvSecret(prefix string) Secret {
	if prefix != "" && !strings.HasSuffix(prefix, "_") {
		prefix += "_"
	}
	return &envSecret{prefix: prefix}
}

type envSecret struct {
	prefix string
}

func (s *envSecret) Get(ctx context.Context, key string) (string, error) {
	envName := s.envKey(key)
	value, ok := os.LookupEnv(envName)
	if !ok {
		return "", fmt.Errorf("secret %q not found", key)
	}
	return value, nil
}

func (s *envSecret) envKey(key string) string {
	base := strings.ToUpper(key)
	base = strings.ReplaceAll(base, "-", "_")
	base = strings.ReplaceAll(base, ".", "_")
	return s.prefix + base
}
