package adminauth

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PostgresRepository stores admin accounts in PostgreSQL.
type PostgresRepository struct {
	database *sql.DB
}

// NewPostgresRepository returns a Repository over the given database.
func NewPostgresRepository(database *sql.DB) Repository {
	return &PostgresRepository{database: database}
}

func (r *PostgresRepository) GetByUsername(ctx context.Context, username string) (*Account, error) {
	if r == nil || r.database == nil {
		return nil, errors.New("admin repository is not configured")
	}
	const query = `
SELECT id, username, password_hash, role, status, failed_attempts, locked_until, last_login_at, created_at
FROM admin_accounts WHERE username = $1`
	account := Account{}
	row := r.database.QueryRowContext(ctx, query, username)
	err := row.Scan(
		&account.ID,
		&account.Username,
		&account.PasswordHash,
		&account.Role,
		&account.Status,
		&account.FailedAttempts,
		&account.LockedUntil,
		&account.LastLoginAt,
		&account.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &account, nil
}

func (r *PostgresRepository) GetByID(ctx context.Context, id string) (*Account, error) {
	if r == nil || r.database == nil {
		return nil, errors.New("admin repository is not configured")
	}
	const query = `
SELECT id, username, password_hash, role, status, failed_attempts, locked_until, last_login_at, created_at
FROM admin_accounts WHERE id = $1`
	account := Account{}
	row := r.database.QueryRowContext(ctx, query, id)
	err := row.Scan(
		&account.ID,
		&account.Username,
		&account.PasswordHash,
		&account.Role,
		&account.Status,
		&account.FailedAttempts,
		&account.LockedUntil,
		&account.LastLoginAt,
		&account.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &account, nil
}

func (r *PostgresRepository) Create(ctx context.Context, account Account) error {
	if r == nil || r.database == nil {
		return errors.New("admin repository is not configured")
	}
	const query = `
INSERT INTO admin_accounts (username, password_hash, role, status)
VALUES ($1, $2, $3, $4)`
	_, err := r.database.ExecContext(ctx, query, account.Username, account.PasswordHash, account.Role, account.Status)
	return err
}

func (r *PostgresRepository) Count(ctx context.Context) (int, error) {
	if r == nil || r.database == nil {
		return 0, errors.New("admin repository is not configured")
	}
	var count int
	err := r.database.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_accounts`).Scan(&count)
	return count, err
}

func (r *PostgresRepository) TouchLogin(ctx context.Context, id string, success bool, now time.Time) (*time.Time, error) {
	if r == nil || r.database == nil {
		return nil, errors.New("admin repository is not configured")
	}
	// The lockout threshold check runs in SQL so the read-modify-write is atomic.
	const query = `
UPDATE admin_accounts SET
    failed_attempts = CASE WHEN $2 THEN 0 ELSE failed_attempts + 1 END,
    locked_until = CASE
        WHEN $2 THEN NULL
        WHEN NOT $2 AND failed_attempts + 1 >= $3 THEN $4
        ELSE locked_until
    END,
    last_login_at = CASE WHEN $2 THEN $4 ELSE last_login_at END,
    updated_at = NOW()
WHERE id = $1
RETURNING locked_until`
	var lockedUntil *time.Time
	err := r.database.QueryRowContext(ctx, query, id, success, maxFailedAttempts, now).Scan(&lockedUntil)
	return lockedUntil, err
}

// PostgresActionLog persists administrator actions to admin_action_logs.
type PostgresActionLog struct {
	database *sql.DB
}

// NewPostgresActionLog returns an ActionLogStore over the given database.
func NewPostgresActionLog(database *sql.DB) ActionLogStore {
	return &PostgresActionLog{database: database}
}

func (s *PostgresActionLog) RecordAction(ctx context.Context, action Action) error {
	if s == nil || s.database == nil {
		return errors.New("admin action log database is not configured")
	}
	const query = `
INSERT INTO admin_action_logs (admin_id, action, resource, resource_id, before_state, after_state, client_ip)
VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := s.database.ExecContext(
		ctx,
		query,
		action.AdminID,
		action.Action,
		action.Resource,
		action.ResourceID,
		jsonValue(action.Before),
		jsonValue(action.After),
		action.ClientIP,
	)
	return err
}

func jsonValue(value any) any {
	if value == nil {
		return nil
	}
	return value
}
