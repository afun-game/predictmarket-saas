// Package reconciliation recovers wallet collateral that has no open order.
package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/nxsky/twill"
)

const (
	defaultSchedule = "@every 10m"
	jobName         = "wallet-lock-reconciliation"
)

// Service finds and releases stranded wallet collateral.
type Service interface {
	Reconcile(ctx context.Context) (*Result, error)
}

// Result summarizes one reconciliation run.
type Result struct {
	twill.AutoMarshal

	WalletsRecovered int     `json:"wallets_recovered"`
	AmountRecovered  float64 `json:"amount_recovered"`
}

type Repository interface {
	Reconcile(ctx context.Context) (*Result, error)
}

type implementation struct {
	twill.Implements[Service]

	database   twill.Database `twill:"primary-db"`
	cron       twill.Cron     `twill:"wallet-lock-reconciliation"`
	repository Repository
	schedule   string
}

// NewService creates the scheduled reconciliation component.
func NewService() Service {
	return &implementation{}
}

func newService(repository Repository) *implementation {
	return &implementation{repository: repository}
}

func (s *implementation) Init(ctx context.Context) error {
	if s.repository == nil {
		database := s.database.Get()
		if database == nil || database.StdDB() == nil {
			return errors.New("primary database is not configured")
		}
		s.repository = newPostgresRepository(database.StdDB())
	}
	if s.schedule == "" {
		s.schedule = strings.TrimSpace(os.Getenv("RECONCILIATION_INTERVAL"))
	}
	if s.schedule == "" {
		s.schedule = defaultSchedule
	}

	scheduler := s.cron.Get()
	if scheduler == nil {
		return errors.New("wallet reconciliation cron is not configured")
	}
	if err := scheduler.Add(ctx, jobName, s.schedule, func(jobCtx context.Context) {
		result, err := s.Reconcile(jobCtx)
		if err != nil {
			slog.ErrorContext(jobCtx, "wallet lock reconciliation failed", "error", err)
			return
		}
		slog.InfoContext(
			jobCtx,
			"wallet lock reconciliation completed",
			"wallets_recovered", result.WalletsRecovered,
			"amount_recovered", result.AmountRecovered,
		)
	}); err != nil {
		return fmt.Errorf("register wallet reconciliation job: %w", err)
	}
	return nil
}

func (s *implementation) Reconcile(ctx context.Context) (*Result, error) {
	if s.repository == nil {
		return nil, errors.New("wallet reconciliation repository is not configured")
	}
	result, err := s.repository.Reconcile(ctx)
	if err != nil {
		return nil, fmt.Errorf("reconcile stranded wallet collateral: %w", err)
	}
	return result, nil
}
