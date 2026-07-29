// Package health provides separate liveness and dependency-aware readiness probes.
package health

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const probeTimeout = 2 * time.Second

// Checker owns the clients used by readiness probes.
type Checker struct {
	database *sql.DB
	redis    *redis.Client
	nats     *nats.Conn
}

// New opens lightweight probe clients for the application's three required
// dependencies. The first readiness request verifies their actual reachability.
func New(databaseURL, redisURL, natsURL string) (*Checker, error) {
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL readiness client: %w", err)
	}
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("parse Redis readiness URL: %w", err)
	}
	redisClient := redis.NewClient(options)
	connection, err := nats.Connect(natsURL, nats.Name("predictmarket-readiness"))
	if err != nil {
		_ = redisClient.Close()
		_ = database.Close()
		return nil, fmt.Errorf("connect NATS readiness client: %w", err)
	}
	return &Checker{database: database, redis: redisClient, nats: connection}, nil
}

// Close releases readiness-probe resources during graceful shutdown.
func (c *Checker) Close() error {
	if c == nil {
		return nil
	}
	if c.nats != nil {
		c.nats.Close()
	}
	if c.redis != nil {
		if err := c.redis.Close(); err != nil {
			return err
		}
	}
	if c.database != nil {
		return c.database.Close()
	}
	return nil
}

// Liveness reports that the process can serve HTTP. It intentionally does not
// depend on external services so Kubernetes can distinguish a restart-worthy
// process failure from a temporary dependency outage.
func (c *Checker) Liveness(w http.ResponseWriter, _ *http.Request) {
	writeStatus(w, http.StatusOK, "ok")
}

// Readiness reports whether PostgreSQL, Redis, and NATS are all reachable.
func (c *Checker) Readiness(w http.ResponseWriter, r *http.Request) {
	if err := c.Ready(r.Context()); err != nil {
		writeStatus(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeStatus(w, http.StatusOK, "ok")
}

// Ready probes every required backing service using one bounded context.
func (c *Checker) Ready(ctx context.Context) error {
	if c == nil || c.database == nil || c.redis == nil || c.nats == nil {
		return fmt.Errorf("readiness checker is not configured")
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	if err := c.database.PingContext(probeCtx); err != nil {
		return fmt.Errorf("PostgreSQL: %w", err)
	}
	if err := c.redis.Ping(probeCtx).Err(); err != nil {
		return fmt.Errorf("Redis: %w", err)
	}
	if err := c.nats.FlushWithContext(probeCtx); err != nil {
		return fmt.Errorf("NATS: %w", err)
	}
	return nil
}

func writeStatus(w http.ResponseWriter, status int, value string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": value})
}
