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
	"path/filepath"
	"strings"
)

// SecretProvider creates a production Secret handle (for example a directory of
// Kubernetes Secret mounts or an external secrets manager). Users register a
// provider via RegisterSecretProvider to replace the default env-var secret
// resolver.
type SecretProvider interface {
	// Open creates a Secret handle for the given resource config.
	Open(cfg Config) (Secret, error)
}

var secretProvider SecretProvider

// RegisterSecretProvider registers a production secret provider. Call once at
// program startup, before twill.Run. If no provider is registered, secrets are
// resolved from environment variables (and optionally TWILL_SECRET_DIR).
func RegisterSecretProvider(p SecretProvider) {
	secretProvider = p
}

// DirSecret resolves secrets from files in a directory. This matches the
// Kubernetes pattern of mounting a Secret as a volume of key files.
//
// Keys are mapped to file names by replacing dashes and dots with underscores.
// If a file is missing, Get falls back to the optional Env Secret when set.
type DirSecret struct {
	root string
	env  Secret
}

// NewDirSecret returns a Secret that reads files under root. If root is empty,
// only the env fallback is used. env may be nil.
func NewDirSecret(root string, env Secret) *DirSecret {
	return &DirSecret{root: root, env: env}
}

// Get implements Secret. Secret values are never included in error messages.
func (s *DirSecret) Get(ctx context.Context, key string) (string, error) {
	if s != nil && s.root != "" {
		path := filepath.Join(s.root, secretFileName(key))
		data, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimRight(string(data), "\r\n"), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("secret %q: read error", key)
		}
	}
	if s != nil && s.env != nil {
		return s.env.Get(ctx, key)
	}
	return "", fmt.Errorf("secret %q not found", key)
}

func secretFileName(key string) string {
	base := strings.ToLower(key)
	base = strings.ReplaceAll(base, "-", "_")
	base = strings.ReplaceAll(base, ".", "_")
	return base
}

// DefaultSecret returns the process-default Secret resolver: files under
// TWILL_SECRET_DIR (if set), then environment variables with the TWILL_SECRET_
// prefix. Suitable for Kubernetes Secret volume mounts without a custom
// provider.
func DefaultSecret() Secret {
	root := strings.TrimSpace(os.Getenv("TWILL_SECRET_DIR"))
	return NewDirSecret(root, NewEnvSecret("TWILL_SECRET_"))
}
