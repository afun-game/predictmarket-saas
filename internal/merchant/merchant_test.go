package merchant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestRegisterAndValidateAPIKey(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	service.now = func() time.Time {
		return time.Date(2026, time.July, 28, 8, 0, 0, 0, time.UTC)
	}

	created, err := service.Register(context.Background(), &RegisterRequest{
		Name:  " Acme Predictions ",
		Email: "ADMIN@EXAMPLE.COM",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if created.ID == "" {
		t.Fatal("Register() returned an empty merchant ID")
	}
	if created.Name != "Acme Predictions" {
		t.Errorf("Register() Name = %q, want %q", created.Name, "Acme Predictions")
	}
	if created.Email != "admin@example.com" {
		t.Errorf("Register() Email = %q, want %q", created.Email, "admin@example.com")
	}
	if created.Currency != defaultCurrency || created.Timezone != defaultTimezone {
		t.Errorf(
			"Register() defaults = (%q, %q), want (%q, %q)",
			created.Currency,
			created.Timezone,
			defaultCurrency,
			defaultTimezone,
		)
	}
	if !strings.HasPrefix(created.APIKey, "pk_live_") {
		t.Errorf("Register() APIKey = %q, want pk_live_ prefix", created.APIKey)
	}
	if !strings.HasPrefix(created.APISecret, "sk_live_") {
		t.Errorf("Register() APISecret = %q, want sk_live_ prefix", created.APISecret)
	}

	stored, err := service.repository.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("repository.GetByID() error = %v", err)
	}
	if stored.APISecret == created.APISecret || stored.APISecret != hashSecret(created.APISecret) {
		t.Error("Register() did not hash the API secret before persistence")
	}
	if stored.APIKey == created.APIKey {
		t.Error("Register() stored an API key in plaintext")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored.APIKey), []byte(created.APIKey)); err != nil {
		t.Errorf("stored API key does not validate as bcrypt: %v", err)
	}
	if stored.APIKeyPrefix == "" || !strings.HasPrefix(created.APIKey, stored.APIKeyPrefix) {
		t.Errorf("stored API key prefix = %q, key = %q", stored.APIKeyPrefix, created.APIKey)
	}

	validated, err := service.ValidateAPIKey(context.Background(), created.APIKey)
	if err != nil {
		t.Fatalf("ValidateAPIKey() error = %v", err)
	}
	if validated.ID != created.ID {
		t.Errorf("ValidateAPIKey() merchant ID = %q, want %q", validated.ID, created.ID)
	}
	if validated.APISecret != "" || validated.APIKey != "" {
		t.Error("ValidateAPIKey() exposed persisted credentials")
	}
}

func TestRegisterRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		req   *RegisterRequest
		field string
	}{
		{name: "nil request", field: "request"},
		{
			name:  "empty name",
			req:   &RegisterRequest{Email: "admin@example.com"},
			field: "name",
		},
		{
			name:  "invalid email",
			req:   &RegisterRequest{Name: "Acme", Email: "not-an-email"},
			field: "email",
		},
		{
			name: "unsupported currency",
			req: &RegisterRequest{
				Name:     "Acme",
				Email:    "admin@example.com",
				Currency: "BTC",
			},
			field: "currency",
		},
		{
			name: "invalid timezone",
			req: &RegisterRequest{
				Name:     "Acme",
				Email:    "admin@example.com",
				Timezone: "Mars/Olympus",
			},
			field: "timezone",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			service := newService(newMemoryRepository())
			_, err := service.Register(context.Background(), test.req)
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("Register() error = %v, want ValidationError", err)
			}
			if validationErr.Field != test.field {
				t.Errorf("Register() error field = %q, want %q", validationErr.Field, test.field)
			}
		})
	}
}

func TestUpdateMerchantConfig(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	created, err := service.Register(context.Background(), &RegisterRequest{
		Name:  "Acme",
		Email: "admin@example.com",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	name := "Acme International"
	currency := "eur"
	timezone := "Europe/Paris"
	updated, err := service.Update(context.Background(), created.ID, &UpdateRequest{
		Name:     &name,
		Currency: &currency,
		Timezone: &timezone,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.Name != name || updated.Status != "active" || updated.Currency != "EUR" {
		t.Errorf("Update() result = %#v", updated)
	}
	if updated.Timezone != timezone || updated.FeeRate != 0 {
		t.Errorf("Update() result = %#v", updated)
	}

	if _, err := service.ValidateAPIKey(context.Background(), created.APIKey); err != nil {
		t.Errorf("ValidateAPIKey() error = %v", err)
	}
}

func TestListUsesStablePaginationAndHidesSecrets(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	service.now = func() time.Time {
		return time.Date(2026, time.July, 28, 8, 0, 0, 0, time.UTC)
	}
	for _, name := range []string{"Charlie", "Alpha", "Bravo"} {
		_, err := service.Register(context.Background(), &RegisterRequest{
			Name:  name,
			Email: strings.ToLower(name) + "@example.com",
		})
		if err != nil {
			t.Fatalf("Register(%q) error = %v", name, err)
		}
	}

	firstPage, err := service.List(context.Background(), 1, 2)
	if err != nil {
		t.Fatalf("List(page 1) error = %v", err)
	}
	secondPage, err := service.List(context.Background(), 2, 2)
	if err != nil {
		t.Fatalf("List(page 2) error = %v", err)
	}
	if len(firstPage) != 2 || len(secondPage) != 1 {
		t.Fatalf("List() page sizes = (%d, %d), want (2, 1)", len(firstPage), len(secondPage))
	}
	for _, merchant := range append(firstPage, secondPage...) {
		if merchant.APISecret != "" || merchant.APIKey != "" {
			t.Errorf("List() exposed credentials for merchant %q", merchant.ID)
		}
	}
}

func TestValidateAPIKeyRejectsUnknownKey(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	_, err := service.ValidateAPIKey(context.Background(), "pk_live_unknown")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("ValidateAPIKey() error = %v, want ErrUnauthorized", err)
	}
}
