package provider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/sportsfeed/lmb"
)

const (
	lmbSourceType = "lmb"
	lmbLeague     = "lmb"
)

// CalendarClient is the narrow LMB dependency required by LMBSource.
type CalendarClient interface {
	Calendar(ctx context.Context, day time.Time) ([]lmb.Game, error)
}

type lmbSource struct {
	client CalendarClient
	now    func() time.Time
}

// NewLMBSource adapts LMB calendar games into provider-neutral fixtures.
func NewLMBSource(client CalendarClient, now func() time.Time) Source {
	if now == nil {
		now = time.Now
	}
	return &lmbSource{client: client, now: now}
}

// Fixtures retrieves and normalizes LMB games for day. Invalid and
// cancelled/postponed/suspended games are intentionally omitted because no
// void/refund policy is implemented at this boundary.
func (s *lmbSource) Fixtures(ctx context.Context, day time.Time) ([]Fixture, error) {
	if s.client == nil {
		return nil, errors.New("LMB calendar client is not configured")
	}

	games, err := s.client.Calendar(ctx, day)
	if err != nil {
		return nil, fmt.Errorf("fetch LMB calendar: %w", err)
	}

	now := s.now()
	fixtures := make([]Fixture, 0, len(games))
	for _, game := range games {
		fixture, ok := normalizeLMBGame(game, now)
		if !ok {
			continue
		}
		fixtures = append(fixtures, fixture)
	}
	return fixtures, nil
}

func normalizeLMBGame(game lmb.Game, now time.Time) (Fixture, bool) {
	awayName := strings.TrimSpace(game.AwayTeam.Name)
	homeName := strings.TrimSpace(game.LocalTeam.Name)
	if game.GameID == 0 || game.DateTime == 0 || awayName == "" || homeName == "" || isCancelledLMBGame(game) {
		return Fixture{}, false
	}

	scheduledAt := time.Unix(game.DateTime, 0).UTC()
	state := StateActive
	if isFinalLMBGame(game) {
		state = StateClosed
	} else if scheduledAt.After(now) {
		state = StatePending
	}

	return Fixture{
		SourceType:  lmbSourceType,
		SourceID:    strconv.FormatInt(game.GameID, 10),
		League:      lmbLeague,
		ScheduledAt: scheduledAt,
		State:       state,
		Away: Team{
			Name:         awayName,
			Abbreviation: strings.TrimSpace(game.AwayTeam.ShortName),
			Role:         "away",
		},
		Home: Team{
			Name:         homeName,
			Abbreviation: strings.TrimSpace(game.LocalTeam.ShortName),
			Role:         "home",
		},
	}, true
}

func isFinalLMBGame(game lmb.Game) bool {
	return strings.EqualFold(strings.TrimSpace(game.Status), "F") ||
		strings.Contains(strings.ToLower(game.DetailedStatus), "final")
}

func isCancelledLMBGame(game lmb.Game) bool {
	for _, status := range []string{game.Status, game.DetailedStatus, game.CanceledStatus} {
		normalized := strings.ToLower(status)
		if strings.Contains(normalized, "cancel") ||
			strings.Contains(normalized, "postpon") ||
			strings.Contains(normalized, "suspend") {
			return true
		}
	}
	return false
}
