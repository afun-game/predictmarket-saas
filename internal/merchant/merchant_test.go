package merchant

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestConfigureIntegrationIssuesCallbackSecret(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	service.encryptSecret = func(secret string) (string, error) {
		return "enc:" + secret, nil
	}
	created, err := service.Register(context.Background(), &RegisterRequest{
		Name:  "Seamless Merchant",
		Email: "seamless@example.com",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	mode := "seamless"
	callbackURL := "https://merchant.example/callback"
	webhookURL := "https://merchant.example/webhook"
	configured, err := service.ConfigureIntegration(context.Background(), created.ID, &IntegrationRequest{
		WalletMode:           &mode,
		CallbackURL:          &callbackURL,
		WebhookURL:           &webhookURL,
		WebhookEvents:        []string{"order.settled", "market.settled"},
		RotateCallbackSecret: true,
	})
	if err != nil {
		t.Fatalf("ConfigureIntegration() error = %v", err)
	}
	if configured.WalletMode != "seamless" || configured.CallbackURL != callbackURL {
		t.Fatalf("configured = %#v", configured)
	}
	if !strings.HasPrefix(configured.CallbackSecret, "cb_live_") {
		t.Fatalf("callback secret = %q", configured.CallbackSecret)
	}
	stored, err := service.repository.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if stored.CallbackSecretEncrypted == "" || stored.CallbackSecretEncrypted == configured.CallbackSecret {
		t.Fatal("callback secret was not encrypted at rest")
	}
}

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

func TestReissueV3SecretKeepsPriorSecretForRotationWindow(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	now := time.Date(2026, time.July, 30, 8, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	service.encryptSecret = func(secret string) (string, error) {
		return "encrypted:" + secret, nil
	}
	created, err := service.Register(context.Background(), &RegisterRequest{
		Name:  "Acme",
		Email: "admin@example.com",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	prior, err := service.repository.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}

	reissued, err := service.ReissueV3Secret(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("ReissueV3Secret() error = %v", err)
	}
	if reissued.APISecret == "" || reissued.APISecret == created.APISecret || reissued.APIKey != "" {
		t.Errorf("reissued merchant = %#v", reissued)
	}
	stored, err := service.repository.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID() after reissue error = %v", err)
	}
	if stored.APISecret != hashSecret(reissued.APISecret) || stored.APISecretEncrypted != "encrypted:"+reissued.APISecret {
		t.Errorf("stored primary V3 secret = %#v", stored)
	}
	if stored.APISecretSecondaryEncrypted != prior.APISecretEncrypted {
		t.Errorf("stored secondary secret = %q, want %q", stored.APISecretSecondaryEncrypted, prior.APISecretEncrypted)
	}
	if stored.APISecretSecondaryExpiresAt == nil || !stored.APISecretSecondaryExpiresAt.Equal(now.Add(v3SecretRotationWindow)) {
		t.Errorf("secondary secret expiry = %v", stored.APISecretSecondaryExpiresAt)
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

func TestConfigureIntegrationInvalidatesCallbackVerificationOnURLChange(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	service.encryptSecret = func(secret string) (string, error) {
		return "enc:" + secret, nil
	}
	created, err := service.Register(context.Background(), &RegisterRequest{
		Name:  "Verify Merchant",
		Email: "verify@example.com",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	mode := "seamless"
	callbackURL := "https://merchant.example/callback"
	configured, err := service.ConfigureIntegration(context.Background(), created.ID, &IntegrationRequest{
		WalletMode:           &mode,
		CallbackURL:          &callbackURL,
		RotateCallbackSecret: true,
	})
	if err != nil {
		t.Fatalf("ConfigureIntegration() error = %v", err)
	}
	verifiedAt := time.Now().UTC()
	configured.CallbackVerifiedAt = &verifiedAt
	if err := service.repository.Update(context.Background(), configured); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	changedURL := "https://merchant.example/callback-v2"
	if _, err := service.ConfigureIntegration(context.Background(), created.ID, &IntegrationRequest{
		CallbackURL: &changedURL,
	}); err != nil {
		t.Fatalf("ConfigureIntegration(changed URL) error = %v", err)
	}
	stored, err := service.repository.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if stored.CallbackVerifiedAt != nil {
		t.Fatal("callback verification survived a callback URL change")
	}
	if stored.CallbackURL != changedURL {
		t.Fatalf("stored callback URL = %q, want %q", stored.CallbackURL, changedURL)
	}
}

func TestNormalizeLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "empty_clears_limit", value: "", want: ""},
		{name: "blank_clears_limit", value: "   ", want: ""},
		{name: "canonicalizes_scale", value: "500", want: "500.00"},
		{name: "keeps_cents", value: "1234.56", want: "1234.56"},
		{name: "rejects_zero", value: "0", wantErr: true},
		{name: "rejects_negative", value: "-10.00", wantErr: true},
		{name: "rejects_non_numeric", value: "abc", wantErr: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeLimit("max_bet_amount", testCase.value)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("normalizeLimit(%q) error = nil, want a validation error", testCase.value)
				}
				var validation *ValidationError
				if !errors.As(err, &validation) {
					t.Fatalf("normalizeLimit(%q) error = %T, want *ValidationError", testCase.value, err)
				}
				if validation.Field != "max_bet_amount" {
					t.Errorf("validation field = %q, want max_bet_amount", validation.Field)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeLimit(%q) error = %v", testCase.value, err)
			}
			if got != testCase.want {
				t.Errorf("normalizeLimit(%q) = %q, want %q", testCase.value, got, testCase.want)
			}
		})
	}
}

func TestUpdateAppliesRiskLimits(t *testing.T) {
	t.Parallel()

	service := newService(newMemoryRepository())
	created, err := service.Register(context.Background(), &RegisterRequest{
		Name:  "Acme",
		Email: "admin@example.com",
	})
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	maxBet := "500"
	maxUser := "2500.5"
	updated, err := service.Update(context.Background(), created.ID, &UpdateRequest{
		MaxBetAmount:    &maxBet,
		MaxUserExposure: &maxUser,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.MaxBetAmount != "500.00" || updated.MaxUserExposure != "2500.50" {
		t.Errorf("limits = %q and %q, want 500.00 and 2500.50",
			updated.MaxBetAmount, updated.MaxUserExposure)
	}
	if updated.MaxMarketExposure != "" {
		t.Errorf("unset limit = %q, want empty", updated.MaxMarketExposure)
	}

	cleared := ""
	updated, err = service.Update(context.Background(), created.ID, &UpdateRequest{
		MaxBetAmount: &cleared,
	})
	if err != nil {
		t.Fatalf("Update() clearing limit error = %v", err)
	}
	if updated.MaxBetAmount != "" {
		t.Errorf("cleared limit = %q, want empty", updated.MaxBetAmount)
	}
	if updated.MaxUserExposure != "2500.50" {
		t.Errorf("untouched limit = %q, want 2500.50", updated.MaxUserExposure)
	}

	invalid := "-1"
	if _, err := service.Update(context.Background(), created.ID, &UpdateRequest{
		MaxMarketExposure: &invalid,
	}); err == nil {
		t.Fatal("Update() accepted a negative limit")
	}
}
