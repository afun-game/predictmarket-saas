package sports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

type postgresRepository struct{ database *sql.DB }

func newPostgresRepository(database *sql.DB) *postgresRepository {
	return &postgresRepository{database: database}
}

// NewPostgresRepository creates a Repository backed by PostgreSQL.
func NewPostgresRepository(database *sql.DB) Repository {
	return newPostgresRepository(database)
}

func (r *postgresRepository) UpsertSource(ctx context.Context, sourceType, sourceID string, value *SportsEvent, syncedAt time.Time) (string, error) {
	tx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin sports metadata upsert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	const upsert = `
INSERT INTO sports_events (event_id, league, game_id, start_time, synced_at)
SELECT id, $3, NULLIF($4, ''), $5, $6
FROM events
WHERE source_type = $1 AND source_id = $2
ON CONFLICT (event_id) DO UPDATE
SET league = EXCLUDED.league,
    game_id = EXCLUDED.game_id,
    start_time = EXCLUDED.start_time,
    synced_at = EXCLUDED.synced_at
RETURNING event_id`
	var eventID string
	err = tx.QueryRowContext(ctx, upsert, sourceType, sourceID, value.League, value.GameID, value.StartTime, syncedAt).Scan(&eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: source type %q, source ID %q", ErrNotFound, sourceType, sourceID)
	}
	if err != nil {
		return "", fmt.Errorf("upsert sports event: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sports_event_teams WHERE event_id = $1`, eventID); err != nil {
		return "", fmt.Errorf("replace sports teams: %w", err)
	}
	const insertTeam = `
INSERT INTO sports_event_teams (event_id, name, abbreviation, role)
VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''))
ON CONFLICT (event_id, name) DO UPDATE
SET abbreviation = EXCLUDED.abbreviation, role = EXCLUDED.role`
	for _, team := range value.Teams {
		if _, err := tx.ExecContext(ctx, insertTeam, eventID, team.Name, team.Abbreviation, team.Role); err != nil {
			return "", fmt.Errorf("insert sports team %q: %w", team.Name, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit sports metadata upsert: %w", err)
	}
	return eventID, nil
}

func (r *postgresRepository) GetByID(ctx context.Context, eventID string) (*SportsEvent, error) {
	const query = `
SELECT e.id, e.source_type, e.source_id, e.title, e.description, e.category,
       e.end_time, e.resolution_time, e.status, e.outcome, e.created_at, e.updated_at,
       se.league, se.game_id, se.start_time
FROM sports_events se
JOIN events e ON e.id = se.event_id
WHERE e.id = $1`
	value, err := scanSportsEvent(r.database.QueryRowContext(ctx, query, eventID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := r.loadTeams(ctx, value); err != nil {
		return nil, err
	}
	return value, nil
}

func (r *postgresRepository) List(ctx context.Context, filters EventFilters) ([]*SportsEvent, int, error) {
	const predicate = `
FROM sports_events se
JOIN events e ON e.id = se.event_id
WHERE ($1 = '' OR se.league = $1)
  AND ($2 = '' OR e.status = $2)
  AND ($3 = '' OR EXISTS (
      SELECT 1 FROM sports_event_teams t
      WHERE t.event_id = se.event_id
        AND (LOWER(t.name) LIKE '%' || LOWER($3) || '%'
             OR LOWER(COALESCE(t.abbreviation, '')) LIKE '%' || LOWER($3) || '%')
  ))`
	var total int
	if err := r.database.QueryRowContext(ctx, "SELECT COUNT(*) "+predicate, filters.League, filters.Status, filters.Team).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count sports events: %w", err)
	}
	query := `
SELECT e.id, e.source_type, e.source_id, e.title, e.description, e.category,
       e.end_time, e.resolution_time, e.status, e.outcome, e.created_at, e.updated_at,
       se.league, se.game_id, se.start_time ` + predicate + `
ORDER BY se.start_time NULLS LAST, e.id
LIMIT $4 OFFSET $5`
	rows, err := r.database.QueryContext(ctx, query, filters.League, filters.Status, filters.Team, filters.Limit, (filters.Page-1)*filters.Limit)
	if err != nil {
		return nil, 0, fmt.Errorf("query sports events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	values := make([]*SportsEvent, 0, filters.Limit)
	for rows.Next() {
		value, err := scanSportsEvent(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate sports events: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, fmt.Errorf("close sports event rows: %w", err)
	}
	for _, value := range values {
		if err := r.loadTeams(ctx, value); err != nil {
			return nil, 0, err
		}
	}
	return values, total, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanSportsEvent(row rowScanner) (*SportsEvent, error) {
	common := &types.Event{}
	value := &SportsEvent{Event: common, Teams: []Team{}}
	var outcome, gameID sql.NullString
	var startTime sql.NullTime
	if err := row.Scan(
		&common.ID, &common.SourceType, &common.SourceID, &common.Title, &common.Description,
		&common.Category, &common.EndTime, &common.ResolutionTime, &common.Status, &outcome,
		&common.CreatedAt, &common.UpdatedAt, &value.League, &gameID, &startTime,
	); err != nil {
		return nil, err
	}
	if outcome.Valid {
		common.Outcome = &outcome.String
	}
	if gameID.Valid {
		value.GameID = gameID.String
	}
	if startTime.Valid {
		value.StartTime = &startTime.Time
	}
	return value, nil
}

func (r *postgresRepository) loadTeams(ctx context.Context, value *SportsEvent) error {
	rows, err := r.database.QueryContext(ctx, `
SELECT name, COALESCE(abbreviation, ''), COALESCE(role, '')
FROM sports_event_teams WHERE event_id = $1
ORDER BY CASE role WHEN 'away' THEN 0 WHEN 'home' THEN 1 ELSE 2 END, name`, value.Event.ID)
	if err != nil {
		return fmt.Errorf("query sports teams: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var team Team
		if err := rows.Scan(&team.Name, &team.Abbreviation, &team.Role); err != nil {
			return fmt.Errorf("scan sports team: %w", err)
		}
		value.Teams = append(value.Teams, team)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sports teams: %w", err)
	}
	return nil
}
