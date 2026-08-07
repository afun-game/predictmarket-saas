package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/sportsfeed/lmb"
)

func TestNormalizeLMBGameFinalFixture(t *testing.T) {
	now := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	game := lmb.Game{
		GameID:         846703,
		Status:         "F",
		DetailedStatus: "Final",
		DateTime:       1786053600,
		AwayTeam:       lmb.Team{Name: "Saraperos", ShortName: "SLW"},
		LocalTeam:      lmb.Team{Name: "Algodoneros", ShortName: "LAG"},
	}

	fixture, ok := normalizeLMBGame(game, now)
	if !ok {
		t.Fatal("normalizeLMBGame() rejected valid final game")
	}

	wantScheduledAt := time.Unix(1786053600, 0).UTC()
	if fixture.SourceType != "lmb" || fixture.SourceID != "846703" || fixture.League != "lmb" {
		t.Errorf("fixture identity = %#v", fixture)
	}
	if !fixture.ScheduledAt.Equal(wantScheduledAt) || fixture.ScheduledAt.Location() != time.UTC {
		t.Errorf("ScheduledAt = %v (%v), want %v (UTC)", fixture.ScheduledAt, fixture.ScheduledAt.Location(), wantScheduledAt)
	}
	if fixture.State != StateClosed {
		t.Errorf("State = %q, want %q", fixture.State, StateClosed)
	}
	if fixture.Away != (Team{Name: "Saraperos", Abbreviation: "SLW", Role: "away"}) {
		t.Errorf("Away = %#v", fixture.Away)
	}
	if fixture.Home != (Team{Name: "Algodoneros", Abbreviation: "LAG", Role: "home"}) {
		t.Errorf("Home = %#v", fixture.Home)
	}
}

func TestNormalizeLMBGameDetailedFinalClosesFixture(t *testing.T) {
	game := validLMBGame()
	game.Status = "in progress"
	game.DetailedStatus = "FINAL after 10 innings"
	game.DateTime = time.Date(2026, time.August, 7, 20, 0, 0, 0, time.UTC).Unix()

	fixture, ok := normalizeLMBGame(game, time.Date(2026, time.August, 7, 19, 0, 0, 0, time.UTC))
	if !ok {
		t.Fatal("normalizeLMBGame() rejected detailed final game")
	}
	if fixture.State != StateClosed {
		t.Errorf("State = %q, want %q", fixture.State, StateClosed)
	}
}

func TestNormalizeLMBGameMapsNonFinalGameByStartTime(t *testing.T) {
	now := time.Date(2026, time.August, 7, 19, 0, 0, 0, time.UTC)

	for name, test := range map[string]struct {
		gameTime time.Time
		want     State
	}{
		"future game is pending": {gameTime: now.Add(time.Minute), want: StatePending},
		"past game is active":    {gameTime: now.Add(-time.Minute), want: StateActive},
		"current game is active": {gameTime: now, want: StateActive},
	} {
		t.Run(name, func(t *testing.T) {
			game := validLMBGame()
			game.DateTime = test.gameTime.Unix()

			fixture, ok := normalizeLMBGame(game, now)
			if !ok {
				t.Fatal("normalizeLMBGame() rejected valid game")
			}
			if fixture.State != test.want {
				t.Errorf("State = %q, want %q", fixture.State, test.want)
			}
		})
	}
}

func TestNormalizeLMBGameSkipsCancelledStatuses(t *testing.T) {
	for name, game := range map[string]lmb.Game{
		"cancelled main status": func() lmb.Game {
			game := validLMBGame()
			game.Status = "CANCELLED"
			return game
		}(),
		"postponed detailed status": func() lmb.Game {
			game := validLMBGame()
			game.DetailedStatus = "Postponed due to rain"
			return game
		}(),
		"suspended cancellation status": func() lmb.Game {
			game := validLMBGame()
			game.CanceledStatus = "Suspended"
			return game
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := normalizeLMBGame(game, time.Now()); ok {
				t.Fatal("normalizeLMBGame() accepted cancelled game")
			}
		})
	}
}

func TestNormalizeLMBGameSkipsInvalidGameData(t *testing.T) {
	for name, game := range map[string]lmb.Game{
		"zero game ID": func() lmb.Game {
			game := validLMBGame()
			game.GameID = 0
			return game
		}(),
		"zero date time": func() lmb.Game {
			game := validLMBGame()
			game.DateTime = 0
			return game
		}(),
		"blank away name": func() lmb.Game {
			game := validLMBGame()
			game.AwayTeam.Name = " \t "
			return game
		}(),
		"blank home name": func() lmb.Game {
			game := validLMBGame()
			game.LocalTeam.Name = "\n"
			return game
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := normalizeLMBGame(game, time.Now()); ok {
				t.Fatal("normalizeLMBGame() accepted invalid game")
			}
		})
	}
}

func TestLMBSourceFetchesAndNormalizesFixtures(t *testing.T) {
	day := time.Date(2026, time.August, 7, 0, 0, 0, 0, time.FixedZone("America/Mexico_City", -6*60*60))
	now := time.Date(2026, time.August, 7, 19, 0, 0, 0, time.UTC)
	future := validLMBGame()
	future.GameID = 846704
	future.DateTime = now.Add(time.Hour).Unix()
	cancelled := validLMBGame()
	cancelled.GameID = 846705
	cancelled.Status = "cancelled"
	client := &fakeCalendarClient{games: []lmb.Game{validLMBGame(), future, cancelled}}

	source := NewLMBSource(client, func() time.Time { return now })
	fixtures, err := source.Fixtures(context.Background(), day)
	if err != nil {
		t.Fatalf("Fixtures() error = %v", err)
	}
	if !client.day.Equal(day) {
		t.Errorf("Calendar day = %v, want %v", client.day, day)
	}
	if len(fixtures) != 2 {
		t.Fatalf("fixtures = %#v, want two fixtures", fixtures)
	}
	if fixtures[0].SourceID != "846703" || fixtures[0].State != StateActive {
		t.Errorf("first fixture = %#v", fixtures[0])
	}
	if fixtures[1].SourceID != "846704" || fixtures[1].State != StatePending {
		t.Errorf("second fixture = %#v", fixtures[1])
	}
}

func TestLMBSourceWrapsCalendarErrors(t *testing.T) {
	want := errors.New("upstream unavailable")
	source := NewLMBSource(&fakeCalendarClient{err: want}, time.Now)

	_, err := source.Fixtures(context.Background(), time.Now())
	if !errors.Is(err, want) {
		t.Fatalf("Fixtures() error = %v, want wrapped upstream error", err)
	}
	if !strings.Contains(err.Error(), "LMB calendar") {
		t.Errorf("Fixtures() error = %q, want LMB calendar context", err)
	}
}

func validLMBGame() lmb.Game {
	return lmb.Game{
		GameID:    846703,
		Status:    "scheduled",
		DateTime:  time.Date(2026, time.August, 7, 18, 0, 0, 0, time.UTC).Unix(),
		AwayTeam:  lmb.Team{Name: "Saraperos", ShortName: "SLW"},
		LocalTeam: lmb.Team{Name: "Algodoneros", ShortName: "LAG"},
	}
}

type fakeCalendarClient struct {
	games []lmb.Game
	err   error
	day   time.Time
}

func (c *fakeCalendarClient) Calendar(_ context.Context, day time.Time) ([]lmb.Game, error) {
	c.day = day
	return c.games, c.err
}
