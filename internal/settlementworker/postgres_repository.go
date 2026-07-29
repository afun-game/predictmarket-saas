package settlementworker

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/nxsky/twill/runtime/resource"
)

type postgresRepository struct {
	database *sql.DB
}

func newPostgresRepository(database *sql.DB) *postgresRepository {
	return &postgresRepository{database: database}
}

func (r *postgresRepository) Dispatch(
	ctx context.Context,
	limit int,
	publisher resource.PubSub,
) (int, error) {
	databaseTx, err := r.database.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin outbox dispatch: %w", err)
	}
	defer func() { _ = databaseTx.Rollback() }()

	messages, err := lockPendingMessages(ctx, databaseTx, limit)
	if err != nil {
		return 0, err
	}
	for _, message := range messages {
		if err := publisher.Publish(ctx, message.topic, message.payload); err != nil {
			return 0, fmt.Errorf("publish outbox message %s: %w", message.id, err)
		}
		if _, err := databaseTx.ExecContext(
			ctx,
			"UPDATE event_outbox SET published_at = $2 WHERE id = $1",
			message.id,
			time.Now().UTC(),
		); err != nil {
			return 0, fmt.Errorf("mark outbox message %s published: %w", message.id, err)
		}
	}
	if err := databaseTx.Commit(); err != nil {
		return 0, fmt.Errorf("commit outbox dispatch: %w", err)
	}
	return len(messages), nil
}

type outboxMessage struct {
	id      string
	topic   string
	payload []byte
}

func lockPendingMessages(
	ctx context.Context,
	databaseTx *sql.Tx,
	limit int,
) ([]outboxMessage, error) {
	const query = `
SELECT id, topic, payload
FROM event_outbox
WHERE published_at IS NULL
ORDER BY created_at, id
LIMIT $1
FOR UPDATE SKIP LOCKED`
	rows, err := databaseTx.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("lock pending outbox messages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	messages := make([]outboxMessage, 0, limit)
	for rows.Next() {
		var message outboxMessage
		if err := rows.Scan(&message.id, &message.topic, &message.payload); err != nil {
			return nil, fmt.Errorf("scan outbox message: %w", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox messages: %w", err)
	}
	return messages, nil
}
