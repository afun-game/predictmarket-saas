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
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// FileLoader loads config values from files in a directory. This supports the
// standard Kubernetes pattern where ConfigMaps and Secrets are mounted as
// directories of files, with each file's name as the key and content as the
// value.
//
// For example, a ConfigMap mounted at /etc/config with keys "timeout" and
// "dsn" produces files /etc/config/timeout and /etc/config/dsn. FileLoader
// reads these files and exposes them via the Loader interface.
//
// Dotted keys are supported by using subdirectories: a key
// "twill.middleware.timeout" maps to the file
// "<root>/twill/middleware/timeout".
type FileLoader struct {
	root string

	mu       sync.RWMutex
	cache    map[string]string
	loaded   bool
	disabled bool
}

// NewFileLoader returns a loader that reads config values from files under
// root. If root does not exist or is not a directory, the loader returns
// empty values for all keys (useful for optional ConfigMap mounts).
func NewFileLoader(root string) *FileLoader {
	return &FileLoader{root: root, cache: map[string]string{}}
}

// Get implements Loader. The key is translated to a file path by replacing
// dots with path separators.
func (l *FileLoader) Get(ctx context.Context, key string) (string, bool) {
	if l == nil || l.disabled {
		return "", false
	}
	l.mu.RLock()
	if l.loaded {
		v, ok := l.cache[key]
		l.mu.RUnlock()
		return v, ok
	}
	l.mu.RUnlock()

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loaded {
		v, ok := l.cache[key]
		return v, ok
	}

	l.load()
	if l.disabled {
		return "", false
	}

	v, ok := l.cache[key]
	return v, ok
}

// Unmarshal implements Loader. FileLoader does not support structured
// unmarshaling; it always returns nil without changing dst.
func (l *FileLoader) Unmarshal(ctx context.Context, key string, dst any) error {
	return nil
}

// load reads all files under root into the cache. Must be called with l.mu held.
func (l *FileLoader) load() {
	l.loaded = true

	info, err := os.Stat(l.root)
	if err != nil || !info.IsDir() {
		l.disabled = true
		return
	}

	filepath.Walk(l.root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(l.root, path)
		if err != nil {
			return nil
		}
		key := filepath.ToSlash(rel)
		key = strings.ReplaceAll(key, "/", ".")
		l.cache[key] = strings.TrimSpace(string(data))
		return nil
	})
}

// Reload re-reads the directory, refreshing cached values. This supports
// Kubernetes ConfigMap/Secret updates where the mount path is refreshed.
func (l *FileLoader) Reload() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loaded = false
	l.disabled = false
	l.cache = map[string]string{}
	l.load()
	if l.disabled {
		return fmt.Errorf("config directory %q does not exist", l.root)
	}
	return nil
}
