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

// EnvResolver resolves resources from environment variables using a prefix.
// Variable names are formed as PREFIX_NAME_KIND, PREFIX_NAME_TYPE, and
// PREFIX_NAME_DSN. NAME is normalized by upper-casing, replacing dashes and
// dots with underscores, and appending an underscore if the prefix does not end
// with one.
type EnvResolver struct {
	Prefix string
}

// NewEnvResolver returns a resolver that reads env vars with the given prefix.
func NewEnvResolver(prefix string) *EnvResolver {
	if prefix != "" && !strings.HasSuffix(prefix, "_") {
		prefix += "_"
	}
	return &EnvResolver{Prefix: prefix}
}

// Resolve implements Resolver.
func (r *EnvResolver) Resolve(ctx context.Context, name string) (Config, bool, error) {
	key := r.envKey(name)
	kind := os.Getenv(key + "KIND")
	dsn := os.Getenv(key + "DSN")
	typ := os.Getenv(key + "TYPE")
	if kind == "" && dsn == "" && typ == "" {
		return Config{}, false, nil
	}
	if kind == "" {
		// Default to database when only a DSN is supplied.
		kind = string(KindDatabase)
	}
	return Config{
		Name: name,
		Kind: Kind(kind),
		Type: typ,
		DSN:  dsn,
	}, true, nil
}

func (r *EnvResolver) envKey(name string) string {
	base := strings.ToUpper(name)
	base = strings.ReplaceAll(base, "-", "_")
	base = strings.ReplaceAll(base, ".", "_")
	base = strings.ReplaceAll(base, "/", "_")
	return fmt.Sprintf("%s%s_", r.Prefix, base)
}
