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

package codegen

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// HTTPAdapter describes one existing net/http route adapted into a Twill
// listener.
type HTTPAdapter struct {
	Listener string
	Method   string
	Path     string
}

// ComponentHTTPAdapters represents HTTP adapter routes for one component.
type ComponentHTTPAdapters struct {
	Component string
	Adapters  []HTTPAdapter
}

// MakeHTTPAdaptersString returns a string that should be emitted into generated
// code to represent the set of net/http adapter routes owned by a component.
func MakeHTTPAdaptersString(component string, adapters []HTTPAdapter) string {
	encoded := encodeHTTPAdapters(adapters)
	return fmt.Sprintf(
		"⟦%s:wEaVeRhTTPAdApTeRs:%s→%s⟧\n",
		checksumHTTPAdapters(component, encoded),
		component,
		encoded,
	)
}

// ExtractHTTPAdapters returns the HTTP adapters encoded using
// MakeHTTPAdaptersString in data.
func ExtractHTTPAdapters(data []byte) []ComponentHTTPAdapters {
	results := []ComponentHTTPAdapters{}
	re := regexp.MustCompile(`⟦([0-9a-fA-F]+):wEaVeRhTTPAdApTeRs:([a-zA-Z0-9\-.~_/]*?)→([^⟧]*)⟧`)
	for _, match := range re.FindAllSubmatch(data, -1) {
		if len(match) != 4 {
			continue
		}
		sum := string(match[1])
		component := string(match[2])
		encoded := string(match[3])
		if sum != checksumHTTPAdapters(component, encoded) {
			continue
		}
		adapters := decodeHTTPAdapters(encoded)
		if len(adapters) == 0 {
			continue
		}
		results = append(results, ComponentHTTPAdapters{
			Component: component,
			Adapters:  adapters,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Component < results[j].Component
	})
	return results
}

func encodeHTTPAdapters(adapters []HTTPAdapter) string {
	items := append([]HTTPAdapter{}, adapters...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].Listener != items[j].Listener {
			return items[i].Listener < items[j].Listener
		}
		if items[i].Method != items[j].Method {
			return items[i].Method < items[j].Method
		}
		return items[i].Path < items[j].Path
	})
	encoded := make([]string, 0, len(items))
	for _, adapter := range items {
		encoded = append(encoded, strings.Join([]string{
			url.QueryEscape(adapter.Listener),
			url.QueryEscape(adapter.Method),
			url.QueryEscape(adapter.Path),
		}, "|"))
	}
	return strings.Join(encoded, ";")
}

func decodeHTTPAdapters(encoded string) []HTTPAdapter {
	if encoded == "" {
		return []HTTPAdapter{}
	}
	parts := strings.Split(encoded, ";")
	adapters := make([]HTTPAdapter, 0, len(parts))
	for _, part := range parts {
		fields := strings.Split(part, "|")
		if len(fields) != 3 {
			continue
		}
		listener, listenerErr := url.QueryUnescape(fields[0])
		method, methodErr := url.QueryUnescape(fields[1])
		path, pathErr := url.QueryUnescape(fields[2])
		if listenerErr != nil || methodErr != nil || pathErr != nil {
			continue
		}
		adapters = append(adapters, HTTPAdapter{
			Listener: listener,
			Method:   method,
			Path:     path,
		})
	}
	return adapters
}

func checksumHTTPAdapters(component, encoded string) string {
	value := fmt.Sprintf("wEaVeRhTTPAdApTeRs:%s→%s", component, encoded)
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%0x", sum)[:8]
}
