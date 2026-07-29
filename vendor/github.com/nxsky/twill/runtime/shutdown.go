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

package runtime

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultShutdownTimeout is the maximum time allowed for graceful drain when
// TWILL_SHUTDOWN_TIMEOUT is unset.
const DefaultShutdownTimeout = 25 * time.Second

// ShutdownTimeout returns the configured graceful shutdown budget. Override
// with TWILL_SHUTDOWN_TIMEOUT (Go duration, for example "20s"). Defaults to
// DefaultShutdownTimeout so it stays under the common 30s Kubernetes
// terminationGracePeriodSeconds.
func ShutdownTimeout() time.Duration {
	v := os.Getenv("TWILL_SHUTDOWN_TIMEOUT")
	if v == "" {
		return DefaultShutdownTimeout
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return DefaultShutdownTimeout
	}
	return d
}

// DrainGate tracks readiness for multi-replica rollouts. When marked not ready
// (typically on SIGTERM), readiness probes fail so the Service endpoints
// controller removes the pod before connections are closed.
type DrainGate struct {
	ready atomic.Bool
}

// NewDrainGate returns a gate that starts ready.
func NewDrainGate() *DrainGate {
	g := &DrainGate{}
	g.ready.Store(true)
	return g
}

// Ready reports whether the process should accept new traffic.
func (g *DrainGate) Ready() bool {
	if g == nil {
		return true
	}
	return g.ready.Load()
}

// MarkNotReady flips readiness off so probes fail during drain.
func (g *DrainGate) MarkNotReady() {
	if g == nil {
		return
	}
	g.ready.Store(false)
}

// MarkReady restores readiness (primarily for tests).
func (g *DrainGate) MarkReady() {
	if g == nil {
		return
	}
	g.ready.Store(true)
}

// ReadinessHandler returns an HTTP handler that serves 200 when the gate is
// ready and 503 when draining. Mount this on the same path used by Kubernetes
// readinessProbe (DefaultHealthPath is /debug/twill/healthz).
func (g *DrainGate) ReadinessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g != nil && !g.Ready() {
			http.Error(w, "draining", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
}

// LivenessHandler returns an HTTP handler that always serves 200 while the
// process is alive. Liveness should not fail during drain; readiness does.
func LivenessHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
}

// GracefulHTTPServer wraps http.Server with drain-aware shutdown. On
// SIGTERM/SIGINT the readiness gate is flipped, an optional pre-stop delay
// runs (so kube-proxy can update endpoints), then Shutdown is called with
// ShutdownTimeout.
type GracefulHTTPServer struct {
	Server       *http.Server
	Gate         *DrainGate
	PreStopDelay time.Duration
}

// PreStopDelayFromEnv returns TWILL_PRESTOP_DELAY or 5s. This should be less
// than terminationGracePeriodSeconds and aligned with the Deployment preStop
// sleep when one is configured.
func PreStopDelayFromEnv() time.Duration {
	v := os.Getenv("TWILL_PRESTOP_DELAY")
	if v == "" {
		return 5 * time.Second
	}
	// Accept either a Go duration or an integer number of seconds.
	if d, err := time.ParseDuration(v); err == nil && d >= 0 {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	return 5 * time.Second
}

// ListenAndServe starts the server and blocks until it exits. It registers a
// signal handler that performs drain + Shutdown. Returns http.ErrServerClosed
// on graceful stop.
func (g *GracefulHTTPServer) ListenAndServe() error {
	if g == nil || g.Server == nil {
		return http.ErrServerClosed
	}
	if g.Gate == nil {
		g.Gate = NewDrainGate()
	}
	delay := g.PreStopDelay
	if delay == 0 {
		delay = PreStopDelayFromEnv()
	}
	var once sync.Once
	OnExitSignal(func() {
		once.Do(func() {
			g.Gate.MarkNotReady()
			if delay > 0 {
				time.Sleep(delay)
			}
			ctx, cancel := context.WithTimeout(context.Background(), ShutdownTimeout())
			defer cancel()
			_ = g.Server.Shutdown(ctx)
		})
	})
	return g.Server.ListenAndServe()
}

// Shutdown drains readiness and shuts the server down. Safe to call without
// using ListenAndServe (for tests and custom signal wiring).
func (g *GracefulHTTPServer) Shutdown(ctx context.Context) error {
	if g == nil || g.Server == nil {
		return nil
	}
	if g.Gate != nil {
		g.Gate.MarkNotReady()
	}
	return g.Server.Shutdown(ctx)
}
