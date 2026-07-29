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

package middleware

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/nxsky/twill/runtime"
	configsource "github.com/nxsky/twill/runtime/config"
)

// ConfigKey and ShortConfigKey identify the middleware section in TOML config.
const (
	ConfigKey      = "twill.middleware"
	ShortConfigKey = "middleware"
)

// MiddlewareListener is implemented by listeners that carry a pre-resolved
// middleware chain. Servers and adapters can apply the chain automatically.
type MiddlewareListener interface {
	net.Listener
	MiddlewareChain() []Middleware
}

// Config describes global and per-listener middleware settings.
type Config struct {
	ListenerConfig
	Listeners map[string]ListenerConfig `toml:"listeners"`
}

// ListenerConfig describes middleware settings for a single listener.
type ListenerConfig struct {
	RequestID                      bool     `toml:"request_id"`
	Timeout                        string   `toml:"timeout"`
	RateLimit                      int      `toml:"rate_limit"`
	RateLimitWindow                string   `toml:"rate_limit_window"`
	CircuitBreakerFailureThreshold int      `toml:"circuit_breaker_failure_threshold"`
	CircuitBreakerOpenDuration     string   `toml:"circuit_breaker_open_duration"`
	RequireIdempotency             []string `toml:"require_idempotency"`
	AuthHook                       string   `toml:"auth_hook"`
}

// Load parses the middleware section for the given listener and returns the
// middleware chain. If the listener is not configured, the global defaults are
// used.
func Load(sections map[string]string, listener string) ([]Middleware, error) {
	var cfg Config
	if err := runtime.ParseConfigSection(ConfigKey, ShortConfigKey, sections, &cfg); err != nil {
		return nil, err
	}
	return cfg.build(listener)
}

// LoadFromLoader parses the middleware section for the given listener using a
// unified config loader. It also applies listener-specific overrides loaded from
// "twill.middleware.listeners.<listener>" if the loader supports it.
func LoadFromLoader(ctx context.Context, loader configsource.Loader, listener string) ([]Middleware, error) {
	if loader == nil {
		return nil, nil
	}
	var cfg Config
	key := ConfigKey
	if _, ok := loader.Get(ctx, ShortConfigKey); ok {
		if _, ok := loader.Get(ctx, ConfigKey); !ok {
			key = ShortConfigKey
		}
	}
	if err := loader.Unmarshal(ctx, key, &cfg); err != nil {
		return nil, err
	}
	return cfg.build(listener)
}

func (cfg Config) build(listener string) ([]Middleware, error) {
	lis := cfg.ListenerConfig
	if override, ok := cfg.Listeners[listener]; ok {
		lis = override
	}
	return lis.build()
}

func (cfg ListenerConfig) build() ([]Middleware, error) {
	var middlewares []Middleware
	if cfg.RequestID {
		middlewares = append(middlewares, RequestID())
	}
	if cfg.Timeout != "" {
		d, err := time.ParseDuration(cfg.Timeout)
		if err != nil {
			return nil, fmt.Errorf("middleware timeout %q: %w", cfg.Timeout, err)
		}
		middlewares = append(middlewares, Timeout(d))
	}
	if cfg.RateLimit > 0 {
		window := time.Minute
		if cfg.RateLimitWindow != "" {
			d, err := time.ParseDuration(cfg.RateLimitWindow)
			if err != nil {
				return nil, fmt.Errorf("middleware rate_limit_window %q: %w", cfg.RateLimitWindow, err)
			}
			window = d
		}
		middlewares = append(middlewares, RateLimit(cfg.RateLimit, window))
	}
	if cfg.CircuitBreakerFailureThreshold > 0 {
		duration := time.Second
		if cfg.CircuitBreakerOpenDuration != "" {
			d, err := time.ParseDuration(cfg.CircuitBreakerOpenDuration)
			if err != nil {
				return nil, fmt.Errorf("middleware circuit_breaker_open_duration %q: %w", cfg.CircuitBreakerOpenDuration, err)
			}
			duration = d
		}
		middlewares = append(middlewares, CircuitBreaker(CircuitBreakerOptions{
			FailureThreshold: cfg.CircuitBreakerFailureThreshold,
			OpenDuration:     duration,
		}))
	}
	if len(cfg.RequireIdempotency) > 0 {
		middlewares = append(middlewares, RequireIdempotencyKey(cfg.RequireIdempotency...))
	}
	if cfg.AuthHook != "" {
		auth, err := LookupAuth(cfg.AuthHook)
		if err != nil {
			return nil, fmt.Errorf("middleware auth_hook %q: %w", cfg.AuthHook, err)
		}
		middlewares = append(middlewares, AuthHook(auth))
	}
	return middlewares, nil
}
