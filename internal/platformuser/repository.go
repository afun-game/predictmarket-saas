// Package platformuser persists the platform's mapping of merchant users.
package platformuser

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const maxExternalUserIDLength = 255

var ErrInvalidUser = errors.New("invalid platform user")

// ErrUserNotFound is returned when a tenant-scoped user does not exist.
var ErrUserNotFound = errors.New("platform user was not found")

// User is the platform record for a merchant-controlled external user ID.
type User struct {
	MerchantID     string
	ExternalUserID string
	Locale         string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Repository stores tenant-scoped user records.
type Repository interface {
	Upsert(ctx context.Context, user User) error
	// Get returns one tenant-scoped user.
	Get(ctx context.Context, merchantID, externalUserID string) (User, error)
	// UpdateStatus changes the user's status (active or blocked).
	UpdateStatus(ctx context.Context, merchantID, externalUserID, status string) error
}

// PostgresRepository stores platform users in PostgreSQL.
type PostgresRepository struct {
	database *sql.DB
}

// NewPostgresRepository constructs a PostgreSQL-backed user repository.
func NewPostgresRepository(database *sql.DB) *PostgresRepository {
	return &PostgresRepository{database: database}
}

// Get returns one tenant-scoped user.
func (r *PostgresRepository) Get(ctx context.Context, merchantID, externalUserID string) (User, error) {
	if r == nil || r.database == nil {
		return User{}, errors.New("platform user database is not configured")
	}
	const query = `
SELECT merchant_id, external_user_id, locale, status, created_at, updated_at
FROM platform_users WHERE merchant_id = $1 AND external_user_id = $2`
	value := User{}
	err := r.database.QueryRowContext(ctx, query, merchantID, externalUserID).Scan(
		&value.MerchantID,
		&value.ExternalUserID,
		&value.Locale,
		&value.Status,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrUserNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("get platform user: %w", err)
	}
	return value, nil
}

// UpdateStatus changes the user's status (active or blocked).
func (r *PostgresRepository) UpdateStatus(ctx context.Context, merchantID, externalUserID, status string) error {
	if r == nil || r.database == nil {
		return errors.New("platform user database is not configured")
	}
	if status != "active" && status != "blocked" {
		return ErrInvalidUser
	}
	const query = `
UPDATE platform_users SET status = $3, updated_at = NOW()
WHERE merchant_id = $1 AND external_user_id = $2`
	result, err := r.database.ExecContext(ctx, query, merchantID, externalUserID, status)
	if err != nil {
		return fmt.Errorf("update platform user status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// Upsert creates an active user or refreshes its locale and timestamp.
func (r *PostgresRepository) Upsert(ctx context.Context, user User) error {
	if r == nil || r.database == nil {
		return errors.New("platform user database is not configured")
	}
	value, err := normalize(user)
	if err != nil {
		return err
	}
	const query = `
INSERT INTO platform_users (
    merchant_id, external_user_id, locale, status, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (merchant_id, external_user_id)
DO UPDATE SET locale = EXCLUDED.locale, updated_at = EXCLUDED.updated_at`
	_, err = r.database.ExecContext(
		ctx,
		query,
		value.MerchantID,
		value.ExternalUserID,
		value.Locale,
		value.Status,
		value.CreatedAt,
		value.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert platform user: %w", err)
	}
	return nil
}

func normalize(user User) (User, error) {
	user.MerchantID = strings.TrimSpace(user.MerchantID)
	user.ExternalUserID = strings.TrimSpace(user.ExternalUserID)
	user.Locale = strings.TrimSpace(user.Locale)
	if user.MerchantID == "" || user.ExternalUserID == "" || len(user.ExternalUserID) > maxExternalUserIDLength {
		return User{}, ErrInvalidUser
	}
	if user.Locale == "" || len(user.Locale) > 35 {
		return User{}, ErrInvalidUser
	}
	if user.Status == "" {
		user.Status = "active"
	}
	if user.Status != "active" && user.Status != "blocked" {
		return User{}, ErrInvalidUser
	}
	now := time.Now().UTC()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = now
	}
	if user.UpdatedAt.IsZero() {
		user.UpdatedAt = now
	}
	return user, nil
}
