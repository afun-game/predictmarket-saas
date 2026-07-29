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

// Package config provides a layered config loader for Twill runtime values.
// It supports loading from TOML sections and environment variables, with
// explicit precedence so environment can override file config.
package config

import (
	"context"
	"fmt"
)

// Loader resolves configuration values by key. Keys use a dotted notation,
// for example "twill.middleware.public.timeout" or
// "twill.resources.reverse-db.dsn".
type Loader interface {
	// Get returns the string value for key, or false if the key is not present.
	Get(ctx context.Context, key string) (string, bool)

	// Unmarshal parses the value at key into dst. It is intended for
	// structured config sections encoded in TOML. If the key is not present,
	// Unmarshal returns nil and leaves dst unchanged.
	Unmarshal(ctx context.Context, key string, dst any) error
}

// Layered combines multiple loaders with precedence: the first loader is tried
// first, and if it does not have the key, the next loader is tried.
type Layered struct {
	loaders []Loader
}

// NewLayered returns a loader that queries sources in order.
func NewLayered(loaders ...Loader) *Layered {
	return &Layered{loaders: loaders}
}

// Get implements Loader.
func (l *Layered) Get(ctx context.Context, key string) (string, bool) {
	for _, loader := range l.loaders {
		if loader == nil {
			continue
		}
		if value, ok := loader.Get(ctx, key); ok {
			return value, true
		}
	}
	return "", false
}

// Unmarshal implements Loader.
func (l *Layered) Unmarshal(ctx context.Context, key string, dst any) error {
	for _, loader := range l.loaders {
		if loader == nil {
			continue
		}
		if err := loader.Unmarshal(ctx, key, dst); err != nil {
			return err
		}
		// If the loader populated dst, we assume it found the key. Because
		// Unmarshal cannot always distinguish "not found" from "found with zero
		// values", loaders should return an error for malformed values and leave
		// dst unchanged when the key is absent.
	}
	return nil
}

// EnvKey converts a config key into an environment variable name using the
// given prefix. Dots and dashes become underscores, and the key is upper-cased.
func EnvKey(prefix, key string) string {
	out := []rune(prefix)
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z':
			out = append(out, r-'a'+'A')
		case r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// Require returns an error if the required value is missing.
func Require(name, value string) error {
	if value == "" {
		return fmt.Errorf("required config value %q is missing", name)
	}
	return nil
}
