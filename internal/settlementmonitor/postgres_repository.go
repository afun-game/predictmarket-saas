package settlementmonitor

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type postgresRepository struct {
	database *sql.DB
}

func newPostgresRepository(database *sql.DB) *postgresRepository {
	return &postgresRepository{database: database}
}

func (r *postgresRepository) OverdueEvents(ctx context.Context, cutoff time.Time) ([]OverdueEvent, error) {
	const query = `
SELECT e.id, e.source_id, e.status, e.resolution_time
FROM events AS e
WHERE e.status <> 'resolved'
  AND e.resolution_time < $1
  AND EXISTS (
      SELECT 1
      FROM markets AS m
      LEFT JOIN market_settlements AS settlement ON settlement.market_id = m.id
      WHERE m.event_id = e.id AND settlement.market_id IS NULL
  )
ORDER BY e.resolution_time, e.id`
	rows, err := r.database.QueryContext(ctx, query, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query overdue settlement events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	events := []OverdueEvent{}
	for rows.Next() {
		var event OverdueEvent
		if err := rows.Scan(&event.ID, &event.SourceID, &event.Status, &event.ResolutionTime); err != nil {
			return nil, fmt.Errorf("scan overdue settlement event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate overdue settlement events: %w", err)
	}
	return events, nil
}
