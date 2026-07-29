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

// GRPCAdapter describes one existing gRPC unary method adapted into a Twill
// listener.
type GRPCAdapter struct {
	Listener string
	Service  string
	Method   string
}

// ComponentGRPCAdapters represents gRPC adapter methods for one component.
type ComponentGRPCAdapters struct {
	Component string
	Adapters  []GRPCAdapter
}

// MakeGRPCAdaptersString returns a string that should be emitted into generated
// code to represent the set of gRPC adapter methods owned by a component.
func MakeGRPCAdaptersString(component string, adapters []GRPCAdapter) string {
	encoded := encodeGRPCAdapters(adapters)
	return fmt.Sprintf(
		"⟦%s:wEaVeRgRPCAdApTeRs:%s→%s⟧\n",
		checksumGRPCAdapters(component, encoded),
		component,
		encoded,
	)
}

// ExtractGRPCAdapters returns the gRPC adapters encoded using
// MakeGRPCAdaptersString in data.
func ExtractGRPCAdapters(data []byte) []ComponentGRPCAdapters {
	results := []ComponentGRPCAdapters{}
	re := regexp.MustCompile(`⟦([0-9a-fA-F]+):wEaVeRgRPCAdApTeRs:([a-zA-Z0-9\-.~_/]*?)→([^⟧]*)⟧`)
	for _, match := range re.FindAllSubmatch(data, -1) {
		if len(match) != 4 {
			continue
		}
		sum := string(match[1])
		component := string(match[2])
		encoded := string(match[3])
		if sum != checksumGRPCAdapters(component, encoded) {
			continue
		}
		adapters := decodeGRPCAdapters(encoded)
		if len(adapters) == 0 {
			continue
		}
		results = append(results, ComponentGRPCAdapters{
			Component: component,
			Adapters:  adapters,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Component < results[j].Component
	})
	return results
}

func encodeGRPCAdapters(adapters []GRPCAdapter) string {
	items := append([]GRPCAdapter{}, adapters...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].Listener != items[j].Listener {
			return items[i].Listener < items[j].Listener
		}
		if items[i].Service != items[j].Service {
			return items[i].Service < items[j].Service
		}
		return items[i].Method < items[j].Method
	})
	encoded := make([]string, 0, len(items))
	for _, adapter := range items {
		encoded = append(encoded, strings.Join([]string{
			url.QueryEscape(adapter.Listener),
			url.QueryEscape(adapter.Service),
			url.QueryEscape(adapter.Method),
		}, "|"))
	}
	return strings.Join(encoded, ";")
}

func decodeGRPCAdapters(encoded string) []GRPCAdapter {
	if encoded == "" {
		return []GRPCAdapter{}
	}
	parts := strings.Split(encoded, ";")
	adapters := make([]GRPCAdapter, 0, len(parts))
	for _, part := range parts {
		fields := strings.Split(part, "|")
		if len(fields) != 3 {
			continue
		}
		listener, listenerErr := url.QueryUnescape(fields[0])
		service, serviceErr := url.QueryUnescape(fields[1])
		method, methodErr := url.QueryUnescape(fields[2])
		if listenerErr != nil || serviceErr != nil || methodErr != nil {
			continue
		}
		adapters = append(adapters, GRPCAdapter{
			Listener: listener,
			Service:  service,
			Method:   method,
		})
	}
	return adapters
}

func checksumGRPCAdapters(component, encoded string) string {
	value := fmt.Sprintf("wEaVeRgRPCAdApTeRs:%s→%s", component, encoded)
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%0x", sum)[:8]
}
