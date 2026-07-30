package event

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/afun-game/predictmarket-saas/internal/messaging"
	"github.com/afun-game/predictmarket-saas/pkg/types"
	"github.com/jackc/pgx/v5/pgconn"
)

const eventColumns = `
    id,
    source_type,
    source_id,
    title,
    description,
    category,
    end_time,
    resolution_time,
    status,
    outcome,
    created_at,
    updated_at`

type postgresRepository struct {
	database *sql.DB
}

func newPostgresRepository(database *sql.DB) *postgresRepository {
	return &postgresRepository{database: database}
}

func (r *postgresRepository) Create(ctx context.Context, value *types.Event) error {
	const query = `
INSERT INTO events (
    id,
    source_type,
    source_id,
    title,
    description,
    category,
    end_time,
    resolution_time,
    status,
    outcome,
    created_at,
    updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`

	_, err := r.database.ExecContext(
		ctx,
		query,
		value.ID,
		value.SourceType,
		value.SourceID,
		value.Title,
		value.Description,
		value.Category,
		value.EndTime,
		value.ResolutionTime,
		value.Status,
		value.Outcome,
		value.CreatedAt,
		value.UpdatedAt,
	)
	if isUniqueViolation(err) {
		return ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

func (r *postgresRepository) UpsertSource(ctx context.Context, value *types.Event) (string, error) {
	const query = `
INSERT INTO events (
    id,
    source_type,
    source_id,
    title,
    description,
    category,
    end_time,
    resolution_time,
    status,
    outcome,
    created_at,
    updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (source_type, source_id) DO UPDATE
SET title = EXCLUDED.title,
    description = EXCLUDED.description,
    category = EXCLUDED.category,
    end_time = EXCLUDED.end_time,
    resolution_time = EXCLUDED.resolution_time,
    status = CASE
        WHEN events.status IN ('closed', 'resolved') THEN events.status
        ELSE EXCLUDED.status
    END,
    updated_at = EXCLUDED.updated_at
RETURNING id`

	var eventID string
	err := r.database.QueryRowContext(
		ctx,
		query,
		value.ID,
		value.SourceType,
		value.SourceID,
		value.Title,
		value.Description,
		value.Category,
		value.EndTime,
		value.ResolutionTime,
		value.Status,
		value.Outcome,
		value.CreatedAt,
		value.UpdatedAt,
	).Scan(&eventID)
	if err != nil {
		return "", fmt.Errorf("upsert source event: %w", err)
	}
	return eventID, nil
}

func (r *postgresRepository) GetByID(
	ctx context.Context,
	eventID string,
) (*types.Event, error) {
	query := "SELECT " + eventColumns + " FROM events WHERE id = $1"
	return scanEvent(r.database.QueryRowContext(ctx, query, eventID))
}

func (r *postgresRepository) GetBySource(
	ctx context.Context,
	sourceType string,
	sourceID string,
) (*types.Event, error) {
	query := "SELECT " + eventColumns + " FROM events WHERE source_type = $1 AND source_id = $2"
	return scanEvent(r.database.QueryRowContext(ctx, query, sourceType, sourceID))
}

func (r *postgresRepository) List(
	ctx context.Context,
	filters ListFilters,
) ([]*types.Event, int, error) {
	const countQuery = `
SELECT COUNT(*)
FROM events
WHERE ($1 = '' OR category = $1)
  AND ($2 = '' OR status = $2)`

	var total int
	err := r.database.QueryRowContext(
		ctx,
		countQuery,
		filters.Category,
		filters.Status,
	).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count events: %w", err)
	}

	query := "SELECT " + eventColumns + `
FROM events
WHERE ($1 = '' OR category = $1)
  AND ($2 = '' OR status = $2)
ORDER BY end_time, id
LIMIT $3 OFFSET $4`
	offset := (filters.Page - 1) * filters.Limit
	rows, err := r.database.QueryContext(
		ctx,
		query,
		filters.Category,
		filters.Status,
		filters.Limit,
		offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("query events: %w", err)
	}
	defer func() {
		_ = rows.Close() // Best-effort cleanup on early returns; checked below on the happy path.
	}()

	values := make([]*types.Event, 0, filters.Limit)
	for rows.Next() {
		value, err := scanEvent(rows)
		if err != nil {
			return nil, 0, err
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate events: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, fmt.Errorf("close event rows: %w", err)
	}
	return values, total, nil
}

func (r *postgresRepository) UpdateStatus(
	ctx context.Context,
	eventID string,
	expectedStatus string,
	status string,
	updatedAt time.Time,
) error {
	const query = `
UPDATE events
SET status = $3, updated_at = $4
WHERE id = $1 AND status = $2`

	result, err := r.database.ExecContext(
		ctx,
		query,
		eventID,
		expectedStatus,
		status,
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("update event status row: %w", err)
	}
	return requireUpdatedRow(result)
}

func (r *postgresRepository) Resolve(
	ctx context.Context,
	eventID string,
	expectedStatus string,
	outcome string,
	resolutionSource string,
	updatedAt time.Time,
) error {
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin event resolution: %w", err)
	}
	defer func() { _ = databaseTx.Rollback() }()

	const resolveQuery = `
UPDATE events
SET status = 'resolved', outcome = $3, updated_at = $4
WHERE id = $1 AND status = $2`
	result, err := databaseTx.ExecContext(
		ctx,
		resolveQuery,
		eventID,
		expectedStatus,
		outcome,
		updatedAt,
	)
	if err != nil {
		return fmt.Errorf("resolve event row: %w", err)
	}
	if err := requireUpdatedRow(result); err != nil {
		return err
	}
	const auditQuery = `
INSERT INTO event_resolution_audits (
    event_id, outcome, resolution_source, resolved_at
) VALUES ($1, $2, $3, $4)`
	if _, err := databaseTx.ExecContext(
		ctx,
		auditQuery,
		eventID,
		outcome,
		resolutionSource,
		updatedAt,
	); err != nil {
		return fmt.Errorf("insert event resolution audit: %w", err)
	}
	payload, err := json.Marshal(messaging.NewEventResolved(eventID, outcome, updatedAt))
	if err != nil {
		return fmt.Errorf("marshal resolved event outbox payload: %w", err)
	}
	const outboxQuery = `
INSERT INTO event_outbox (event_id, event_type, topic, payload, created_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (event_id, event_type) DO NOTHING`
	if _, err := databaseTx.ExecContext(
		ctx,
		outboxQuery,
		eventID,
		messaging.EventResolvedType,
		messaging.EventResolvedTopic,
		payload,
		updatedAt,
	); err != nil {
		return fmt.Errorf("insert resolved event outbox message: %w", err)
	}
	if err := databaseTx.Commit(); err != nil {
		return fmt.Errorf("commit event resolution: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEvent(row rowScanner) (*types.Event, error) {
	value := &types.Event{}
	err := row.Scan(
		&value.ID,
		&value.SourceType,
		&value.SourceID,
		&value.Title,
		&value.Description,
		&value.Category,
		&value.EndTime,
		&value.ResolutionTime,
		&value.Status,
		&value.Outcome,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan event: %w", err)
	}
	return value, nil
}

func requireUpdatedRow(result sql.Result) error {
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count updated event rows: %w", err)
	}
	if rowsAffected == 0 {
		return ErrInvalidTransition
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var postgresErr *pgconn.PgError
	return errors.As(err, &postgresErr) && postgresErr.Code == "23505"
}
