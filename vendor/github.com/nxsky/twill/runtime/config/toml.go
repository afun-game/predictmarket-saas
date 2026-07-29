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
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

// TomlLoader loads config values from a TOML section map. The section map is
// the same shape produced by runtime.ParseConfig.
type TomlLoader struct {
	sections map[string]string
}

// NewTomlLoader returns a loader backed by the provided TOML sections.
func NewTomlLoader(sections map[string]string) *TomlLoader {
	return &TomlLoader{sections: sections}
}

// Get implements Loader. It looks for an exact section key or a section plus
// nested key using the first dot as a separator.
func (l *TomlLoader) Get(ctx context.Context, key string) (string, bool) {
	if l == nil {
		return "", false
	}
	// Try exact section match first.
	if section, ok := l.sections[key]; ok {
		return section, true
	}
	// Split into section and nested key. The section key is everything before the
	// last dot so that sections like "twill.middleware" can expose fields like
	// "timeout" via the key "twill.middleware.timeout".
	i := strings.LastIndex(key, ".")
	if i < 0 {
		return "", false
	}
	sectionKey, field := key[:i], key[i+1:]
	section, ok := l.sections[sectionKey]
	if !ok {
		return "", false
	}
	return getTomlField(section, field)
}

// Unmarshal implements Loader by parsing the section named key into dst.
func (l *TomlLoader) Unmarshal(ctx context.Context, key string, dst any) error {
	if l == nil {
		return nil
	}
	section, ok := l.sections[key]
	if !ok {
		return nil
	}
	if unknown, err := toml.Decode(section, dst); err != nil {
		return fmt.Errorf("decode section %q: %w", key, err)
	} else if len(unknown.Undecoded()) > 0 {
		return fmt.Errorf("section %q has unknown keys %v", key, unknown.Undecoded())
	}
	return nil
}

func getTomlField(section, field string) (string, bool) {
	var data map[string]any
	if _, err := toml.Decode(section, &data); err != nil {
		return "", false
	}
	value, ok := data[field]
	if !ok {
		return "", false
	}
	switch v := value.(type) {
	case string:
		return v, true
	case bool:
		return fmt.Sprintf("%t", v), true
	case int64:
		return fmt.Sprintf("%d", v), true
	default:
		return "", false
	}
}
