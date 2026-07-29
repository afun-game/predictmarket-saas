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

import "context"

// LayeredResolver tries multiple resolvers in order and returns the first
// successful config.
type LayeredResolver struct {
	resolvers []Resolver
}

// NewLayeredResolver returns a resolver that queries resolvers in order.
func NewLayeredResolver(resolvers ...Resolver) *LayeredResolver {
	return &LayeredResolver{resolvers: resolvers}
}

// Resolve implements Resolver.
func (l *LayeredResolver) Resolve(ctx context.Context, name string) (Config, bool, error) {
	for _, r := range l.resolvers {
		if r == nil {
			continue
		}
		cfg, ok, err := r.Resolve(ctx, name)
		if err != nil {
			return Config{}, false, err
		}
		if ok {
			return cfg, true, nil
		}
	}
	return Config{}, false, nil
}
