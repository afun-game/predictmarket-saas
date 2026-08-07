package market

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/afun-game/predictmarket-saas/pkg/types"
)

const (
	testMerchantID = "11111111-1111-4111-8111-111111111111"
	testEventID    = "22222222-2222-4222-8222-222222222222"
)

func TestCreateAndGet(t *testing.T) {
	service := newService(newMemoryRepository())
	created, err := service.Create(context.Background(), validCreateRequest())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Status != "active" || created.LiquidityPool != 100 {
		t.Fatalf("Create() market = %#v", created)
	}
	if created.MerchantFeeRate != 0 || created.PlatformFeeRate != 0 {
		t.Errorf("Create() fee rates = (%v, %v), want (0, 0)", created.MerchantFeeRate, created.PlatformFeeRate)
	}
	if created.ID == "" || created.CreatedAt.IsZero() {
		t.Errorf("Create() did not initialize ID and creation time: %#v", created)
	}

	created.Options[0] = "changed"
	stored, err := service.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Options[0] != "Yes" {
		t.Errorf("stored options changed through returned value: %#v", stored.Options)
	}
}

func TestCreateValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CreateRequest)
		field  string
	}{
		{name: "merchant ID", mutate: func(req *CreateRequest) { req.MerchantID = "bad" }, field: "merchant_id"},
		{name: "event ID", mutate: func(req *CreateRequest) { req.EventID = "bad" }, field: "event_id"},
		{name: "type", mutate: func(req *CreateRequest) { req.Type = "sportsbook" }, field: "type"},
		{name: "multiple choice is unsupported", mutate: func(req *CreateRequest) {
			req.Type = "multiple_choice"
			req.Options = []string{"A", "B", "C"}
		}, field: "type"},
		{name: "question", mutate: func(req *CreateRequest) { req.Question = " " }, field: "question"},
		{name: "binary option count", mutate: func(req *CreateRequest) { req.Options = []string{"Yes"} }, field: "options"},
		{name: "duplicate options", mutate: func(req *CreateRequest) { req.Options = []string{"Yes", " yes "} }, field: "options"},
		{name: "empty option", mutate: func(req *CreateRequest) { req.Options = []string{"Yes", " "} }, field: "options"},
		{name: "negative liquidity", mutate: func(req *CreateRequest) { req.LiquidityPool = -1 }, field: "liquidity_pool"},
		{name: "fractional cent", mutate: func(req *CreateRequest) { req.LiquidityPool = 1.001 }, field: "liquidity_pool"},
		{name: "non-finite liquidity", mutate: func(req *CreateRequest) { req.LiquidityPool = math.Inf(1) }, field: "liquidity_pool"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := validCreateRequest()
			test.mutate(req)
			_, err := newService(newMemoryRepository()).Create(context.Background(), req)
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) || validationErr.Field != test.field {
				t.Fatalf("Create() error = %v, want ValidationError field %q", err, test.field)
			}
		})
	}
}

func TestCreateRejectsInvalidReferences(t *testing.T) {
	repository := &invalidReferenceRepository{Repository: newMemoryRepository()}
	_, err := newService(repository).Create(context.Background(), validCreateRequest())
	if !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("Create() error = %v, want ErrInvalidReference", err)
	}
}

func TestCreateRejectsExpiredEvent(t *testing.T) {
	t.Parallel()

	repository := &expiredEventRepository{Repository: newMemoryRepository()}
	_, err := newService(repository).Create(context.Background(), validCreateRequest())
	if !errors.Is(err, ErrEventExpired) {
		t.Fatalf("Create() error = %v, want ErrEventExpired", err)
	}
}

func TestCreateInheritsEventCategory(t *testing.T) {
	t.Parallel()

	service := newService(&categorizedEventRepository{Repository: newMemoryRepository()})
	created, err := service.Create(context.Background(), validCreateRequest())
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Category != "sports" {
		t.Fatalf("Category = %q, want inherited sports", created.Category)
	}
}

func TestCreateCategoryExplicitOverridesInheritance(t *testing.T) {
	t.Parallel()

	service := newService(&categorizedEventRepository{Repository: newMemoryRepository()})
	request := validCreateRequest()
	request.Category = "eSports"
	created, err := service.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Category != "esports" {
		t.Fatalf("Category = %q, want lowercased esports", created.Category)
	}
}

func TestCreateRejectsOverlongCategory(t *testing.T) {
	t.Parallel()

	request := validCreateRequest()
	request.Category = string(make([]byte, maxCategoryLength+1))
	if _, err := validateCreateRequest(request); err == nil {
		t.Fatal("validateCreateRequest() accepted an overlong category")
	}
}

func TestListFiltersAndPagination(t *testing.T) {
	service := newService(newMemoryRepository())
	first := createMarket(t, service, validCreateRequest())

	secondRequest := validCreateRequest()
	secondRequest.EventID = "33333333-3333-4333-8333-333333333333"
	second := createMarket(t, service, secondRequest)
	if err := service.UpdateStatus(context.Background(), second.ID, "suspended"); err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}

	values, total, err := service.List(context.Background(), &ListFilters{
		EventID: secondRequest.EventID,
		Status:  "suspended",
	})
	if err != nil {
		t.Fatalf("List(filtered) error = %v", err)
	}
	if total != 1 || len(values) != 1 || values[0].ID != second.ID {
		t.Fatalf("List(filtered) values = %#v, total = %d", values, total)
	}

	values, total, err = service.List(context.Background(), &ListFilters{Page: 2, Limit: 1})
	if err != nil {
		t.Fatalf("List(paged) error = %v", err)
	}
	if total != 2 || len(values) != 1 {
		t.Fatalf("List(paged) len = %d, total = %d", len(values), total)
	}
	if values[0].ID != first.ID && values[0].ID != second.ID {
		t.Errorf("List(paged) returned unknown market %q", values[0].ID)
	}
}

func TestListSortsLatestAndPopular(t *testing.T) {
	repository := newMemoryRepository()
	service := newService(repository)
	now := time.Date(2026, time.August, 4, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }

	older := createMarket(t, service, validCreateRequest())
	now = now.Add(time.Minute)
	newerRequest := validCreateRequest()
	newerRequest.EventID = "33333333-3333-4333-8333-333333333333"
	newer := createMarket(t, service, newerRequest)

	repository.mu.Lock()
	repository.byID[older.ID].TotalVolume = 100
	repository.byID[newer.ID].TotalVolume = 10
	repository.mu.Unlock()

	latest, _, err := service.List(context.Background(), &ListFilters{Sort: "latest"})
	if err != nil {
		t.Fatalf("List(latest) error = %v", err)
	}
	if latest[0].ID != newer.ID {
		t.Errorf("List(latest) first ID = %q, want %q", latest[0].ID, newer.ID)
	}

	popular, _, err := service.List(context.Background(), &ListFilters{Sort: "popular"})
	if err != nil {
		t.Fatalf("List(popular) error = %v", err)
	}
	if popular[0].ID != older.ID {
		t.Errorf("List(popular) first ID = %q, want %q", popular[0].ID, older.ID)
	}

	_, _, err = service.List(context.Background(), &ListFilters{Sort: "oldest"})
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || validationErr.Field != "sort" {
		t.Fatalf("List(invalid sort) error = %v, want ValidationError for sort", err)
	}
}

func TestStatusTransitions(t *testing.T) {
	service := newService(newMemoryRepository())
	created := createMarket(t, service, validCreateRequest())
	ctx := context.Background()

	if err := service.UpdateStatus(ctx, created.ID, "suspended"); err != nil {
		t.Fatalf("UpdateStatus(suspended) error = %v", err)
	}
	if err := service.UpdateStatus(ctx, created.ID, "active"); err != nil {
		t.Fatalf("UpdateStatus(active) error = %v", err)
	}
	if err := service.UpdateStatus(ctx, created.ID, "closed"); err != nil {
		t.Fatalf("UpdateStatus(closed) error = %v", err)
	}
	if err := service.UpdateStatus(ctx, created.ID, "active"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("UpdateStatus(reopen) error = %v, want ErrInvalidTransition", err)
	}
	if err := service.UpdateStatus(ctx, created.ID, "settled"); err == nil {
		t.Fatal("UpdateStatus(settled) error = nil")
	}
}

func TestAddLiquidity(t *testing.T) {
	service := newService(newMemoryRepository())
	created := createMarket(t, service, validCreateRequest())
	ctx := context.Background()

	const additions = 20
	var waitGroup sync.WaitGroup
	for range additions {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if err := service.AddLiquidity(ctx, created.ID, 1.25); err != nil {
				t.Errorf("AddLiquidity() error = %v", err)
			}
		}()
	}
	waitGroup.Wait()
	updated, err := service.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	wantLiquidity := created.LiquidityPool + additions*1.25
	if updated.LiquidityPool != wantLiquidity {
		t.Errorf("LiquidityPool = %v, want %v", updated.LiquidityPool, wantLiquidity)
	}

	if err := service.AddLiquidity(ctx, created.ID, 0); err == nil {
		t.Fatal("AddLiquidity(0) error = nil")
	}
	if err := service.UpdateStatus(ctx, created.ID, "closed"); err != nil {
		t.Fatalf("UpdateStatus(closed) error = %v", err)
	}
	if err := service.AddLiquidity(ctx, created.ID, 1); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AddLiquidity(closed) error = %v, want ErrInvalidTransition", err)
	}
}

func TestGetOrderBookReturnsInitializedSlices(t *testing.T) {
	service := newService(newMemoryRepository())
	created := createMarket(t, service, validCreateRequest())
	book, err := service.GetOrderBook(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetOrderBook() error = %v", err)
	}
	if book.Bids == nil || book.Asks == nil || len(book.Bids) != 0 || len(book.Asks) != 0 {
		t.Errorf("GetOrderBook() = %#v", book)
	}
}

type invalidReferenceRepository struct {
	Repository
}

func (r *invalidReferenceRepository) ValidateReferences(context.Context, string, string) (string, error) {
	return "", ErrInvalidReference
}

type expiredEventRepository struct {
	Repository
}

func (r *expiredEventRepository) ValidateReferences(context.Context, string, string) (string, error) {
	return "", ErrEventExpired
}

type categorizedEventRepository struct {
	Repository
}

func (r *categorizedEventRepository) ValidateReferences(context.Context, string, string) (string, error) {
	return "sports", nil
}

func validCreateRequest() *CreateRequest {
	return &CreateRequest{
		MerchantID:    testMerchantID,
		EventID:       testEventID,
		Type:          "binary",
		Question:      "Will the MVP launch?",
		Options:       []string{"Yes", "No"},
		LiquidityPool: 100,
	}
}

func createMarket(t *testing.T, service *implementation, req *CreateRequest) *types.Market {
	t.Helper()
	value, err := service.Create(context.Background(), req)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return value
}

func TestListFiltersAcceptAllStoredStatuses(t *testing.T) {
	t.Parallel()

	// voided is written by market settlement; the list filter must accept
	// every status that can exist in storage.
	for _, status := range []string{"active", "suspended", "closed", "settled", "voided"} {
		normalized, err := normalizeFilters(&ListFilters{Status: status})
		if err != nil {
			t.Fatalf("normalizeFilters(status=%q) error = %v", status, err)
		}
		if normalized.Status != status {
			t.Errorf("normalizeFilters(status=%q) = %q", status, normalized.Status)
		}
	}
	if _, err := normalizeFilters(&ListFilters{Status: "unknown"}); err == nil {
		t.Fatal("normalizeFilters accepted an unknown status")
	}
}
