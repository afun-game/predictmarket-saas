package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	boxrecSourceType = "boxrec"
	boxrecLeague     = "boxing"
)

// BoxRec types mirror the schedule JSON produced by the browser extractor
// (docs/providers/boxrec/probes/extract-schedule.js). Only the fields needed
// to project prediction events are decoded.

// BoxRecBout is a single scheduled bout on a BoxRec event card.
type BoxRecBout struct {
	UID   string       `json:"uid"`
	Cells []string     `json:"cells"`
	Links []BoxRecLink `json:"links"`
}

// BoxRecLink is a hyperlink extracted from a bout row.
type BoxRecLink struct {
	Text string `json:"text"`
	Href string `json:"href"`
}

// BoxRecEvent is one BoxRec event (a fight card) with its bouts.
type BoxRecEvent struct {
	HeaderText string       `json:"headerText"`
	Date       string       `json:"date"`
	DateID     string       `json:"dateId"`
	EventID    string       `json:"eventId"`
	EventName  string       `json:"eventName"`
	Location   string       `json:"location"`
	Bouts      []BoxRecBout `json:"bouts"`
}

// BoxRecSchedule is the top-level captured schedule document.
type BoxRecSchedule struct {
	Provider  string        `json:"provider"`
	FetchedAt string        `json:"fetchedAt"`
	Events    []BoxRecEvent `json:"events"`
}

var fighterLinkRe = regexp.MustCompile(`/en/box-(pro|am)/(\d+)`)

// ParseBoxRecSchedule converts a captured BoxRec schedule document into
// provider-neutral fixtures. Boxing is a two-participant contest: the first
// fighter maps to Away and the second to Home. Bouts without two resolved
// fighters (TBA opponents have no link) are skipped because a prediction
// event needs both participants. BoxRec publishes dates only (no start
// time), so ScheduledAt is midnight UTC of the event date.
func ParseBoxRecSchedule(data []byte) ([]Fixture, error) {
	var schedule BoxRecSchedule
	if err := json.Unmarshal(data, &schedule); err != nil {
		return nil, fmt.Errorf("parse boxrec schedule JSON: %w", err)
	}

	fixtures := make([]Fixture, 0)
	for _, event := range schedule.Events {
		eventID := strings.TrimSpace(event.EventID)
		if eventID == "" {
			continue
		}
		scheduledAt, ok := parseBoxRecDate(event.DateID)
		if !ok {
			continue
		}
		for _, bout := range event.Bouts {
			fixture, ok := normalizeBoxRecBout(event.EventID, bout, scheduledAt)
			if !ok {
				continue
			}
			fixtures = append(fixtures, fixture)
		}
	}
	return fixtures, nil
}

// parseBoxRecDate converts a dateId ("2026-08-07") to midnight UTC. Events
// without a stable date id are skipped.
func parseBoxRecDate(dateID string) (time.Time, bool) {
	dateID = strings.TrimSpace(dateID)
	if dateID == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse("2006-01-02", dateID)
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func normalizeBoxRecBout(eventID string, bout BoxRecBout, scheduledAt time.Time) (Fixture, bool) {
	if bout.UID == "" {
		return Fixture{}, false
	}
	// resolve fighter links; TBA opponents have no /en/box-pro|am/ link
	var first, second *BoxRecLink
	for i := range bout.Links {
		link := &bout.Links[i]
		if !fighterLinkRe.MatchString(link.Href) {
			continue
		}
		if first == nil {
			first = link
		} else if second == nil {
			second = link
		}
	}
	if first == nil || second == nil {
		return Fixture{}, false // need both participants
	}

	awayName := strings.TrimSpace(first.Text)
	homeName := strings.TrimSpace(second.Text)
	if awayName == "" || homeName == "" {
		return Fixture{}, false
	}

	return Fixture{
		SourceType:  boxrecSourceType,
		SourceID:    eventID + ":" + bout.UID,
		League:      boxrecLeague,
		ScheduledAt: scheduledAt,
		State:       StatePending,
		Away: Team{
			Name: awayName,
			Role: "away",
		},
		Home: Team{
			Name: homeName,
			Role: "home",
		},
	}, true
}

// StaticBoxRecSource is a Source over a previously captured BoxRec schedule.
// It satisfies the sports ingestion Source contract so the shared sync
// pipeline can project bouts into prediction events. The captured schedule
// already covers the provider's lookahead window, so every day returns the
// same parsed fixtures.
type StaticBoxRecSource struct {
	fixtures []Fixture
}

// NewBoxRecSource builds a Source from captured schedule JSON bytes.
func NewBoxRecSource(data []byte) (*StaticBoxRecSource, error) {
	fixtures, err := ParseBoxRecSchedule(data)
	if err != nil {
		return nil, err
	}
	return &StaticBoxRecSource{fixtures: fixtures}, nil
}

// Fixtures returns the parsed bouts for any day.
func (s *StaticBoxRecSource) Fixtures(_ context.Context, _ time.Time) ([]Fixture, error) {
	return s.fixtures, nil
}
