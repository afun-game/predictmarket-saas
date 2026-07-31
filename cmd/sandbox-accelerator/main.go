// Command sandbox-accelerator is the V3 sandbox fake settlement accelerator.
// It watches active events through the merchant API and resolves them through
// the admin API once their resolution time passes, so sandbox merchants can
// exercise settlement webhooks, seamless credits, and reconciliation without
// manually resolving events.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type eventItem struct {
	ID             string    `json:"id"`
	Status         string    `json:"status"`
	ResolutionTime time.Time `json:"resolution_time"`
}

type eventListResponse struct {
	Data []eventItem `json:"data"`
	Meta struct {
		Pagination struct {
			Page    int  `json:"page"`
			HasNext bool `json:"has_next"`
		} `json:"pagination"`
	} `json:"meta"`
}

func main() {
	api := flag.String("api", "http://localhost:8080", "platform base URL")
	merchantKey := flag.String("merchant-key", "", "merchant API key for listing events")
	adminKey := flag.String("admin-key", "", "admin API key for resolving events")
	interval := flag.Duration("interval", 10*time.Second, "poll interval")
	outcome := flag.String("outcome", "Yes", "winning outcome applied to resolved events")
	limit := flag.Int("limit", 500, "events page size")
	dryRun := flag.Bool("dry-run", false, "list due events without resolving")
	flag.Parse()

	if strings.TrimSpace(*merchantKey) == "" || strings.TrimSpace(*adminKey) == "" {
		fatal("-merchant-key and -admin-key are required")
	}
	accelerator := &accelerator{
		baseURL:     strings.TrimRight(*api, "/"),
		merchantKey: strings.TrimSpace(*merchantKey),
		adminKey:    strings.TrimSpace(*adminKey),
		outcome:     strings.TrimSpace(*outcome),
		limit:       *limit,
		dryRun:      *dryRun,
		client:      &http.Client{Timeout: 10 * time.Second},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.Info("sandbox settlement accelerator started",
		"api", accelerator.baseURL, "interval", interval.String(), "outcome", accelerator.outcome, "dry_run", accelerator.dryRun)
	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	// Run once immediately so a short-lived CI job still resolves due events.
	accelerator.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			slog.Info("sandbox settlement accelerator stopped")
			return
		case <-ticker.C:
			accelerator.poll(ctx)
		}
	}
}

type accelerator struct {
	baseURL     string
	merchantKey string
	adminKey    string
	outcome     string
	limit       int
	dryRun      bool
	client      *http.Client
}

func (a *accelerator) poll(ctx context.Context) {
	events, err := a.dueEvents(ctx)
	if err != nil {
		slog.Error("list due events failed", "error", err)
		return
	}
	for _, event := range events {
		if a.dryRun {
			slog.Info("event due for resolution (dry run)", "event_id", event.ID)
			continue
		}
		if err := a.resolve(ctx, event.ID); err != nil {
			slog.Error("resolve event failed", "error", err, "event_id", event.ID)
			continue
		}
		slog.Info("event resolved", "event_id", event.ID, "outcome", a.outcome)
	}
}

func (a *accelerator) dueEvents(ctx context.Context) ([]eventItem, error) {
	now := time.Now().UTC()
	var due []eventItem
	for page := 1; ; page++ {
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			fmt.Sprintf("%s/api/v1/events?status=active&limit=%d&page=%d", a.baseURL, a.limit, page),
			nil,
		)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Bearer "+a.merchantKey)
		response, err := a.client.Do(request)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		_ = response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("list events status %d: %s", response.StatusCode, body)
		}
		var payload eventListResponse
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("decode events: %w", err)
		}
		for _, event := range payload.Data {
			if !event.ResolutionTime.IsZero() && !event.ResolutionTime.After(now) {
				due = append(due, event)
			}
		}
		if !payload.Meta.Pagination.HasNext || len(payload.Data) == 0 {
			break
		}
	}
	return due, nil
}

func (a *accelerator) resolve(ctx context.Context, eventID string) error {
	body, err := json.Marshal(map[string]string{"outcome": a.outcome})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		a.baseURL+"/api/v1/events/"+eventID+"/resolve",
		bytes.NewReader(body),
	)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+a.adminKey)
	request.Header.Set("Content-Type", "application/json")
	response, err := a.client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusOK {
		encoded, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return fmt.Errorf("resolve event status %d: %s", response.StatusCode, encoded)
	}
	return nil
}

func fatal(message string) {
	slog.Error(message)
	os.Exit(1)
}
