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

	"github.com/nxsky/twill/runtime/config"
)

// LoaderResolver resolves resources from a Twill config.Loader. Resource keys
// are formed as "twill.resources.<name>.kind", "twill.resources.<name>.type",
// and "twill.resources.<name>.dsn".
type LoaderResolver struct {
	Loader config.Loader
}

// NewLoaderResolver returns a resolver backed by loader.
func NewLoaderResolver(loader config.Loader) *LoaderResolver {
	return &LoaderResolver{Loader: loader}
}

// Resolve implements Resolver.
func (r *LoaderResolver) Resolve(ctx context.Context, name string) (Config, bool, error) {
	if r.Loader == nil {
		return Config{}, false, nil
	}
	base := "twill.resources." + name + "."
	kind, _ := r.Loader.Get(ctx, base+"kind")
	dsn, _ := r.Loader.Get(ctx, base+"dsn")
	typ, _ := r.Loader.Get(ctx, base+"type")
	if kind == "" && dsn == "" && typ == "" {
		return Config{}, false, nil
	}
	if kind == "" {
		kind = string(KindDatabase)
	}
	return Config{
		Name: name,
		Kind: Kind(kind),
		Type: typ,
		DSN:  dsn,
	}, true, nil
}

// LoaderWithEnvFallback returns a LoaderResolver that uses loader first, then
// falls back to an env resolver with the given prefix. It is a convenience for
// migrating from env-only resource config to a unified config loader.
func LoaderWithEnvFallback(loader config.Loader, envPrefix string) *LoaderResolver {
	return NewLoaderResolver(config.NewLayered(loader, config.NewEnvLoader(envPrefix)))
}
