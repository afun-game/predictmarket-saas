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
}

// PostgresRepository stores platform users in PostgreSQL.
type PostgresRepository struct {
	database *sql.DB
}

// NewPostgresRepository constructs a PostgreSQL-backed user repository.
func NewPostgresRepository(database *sql.DB) *PostgresRepository {
	return &PostgresRepository{database: database}
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
