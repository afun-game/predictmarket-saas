package settlement

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"
)

func TestCalculatePayouts(t *testing.T) {
	tests := []struct {
		name          string
		winningOption string
		orders        []*settlementOrder
		wantPayouts   []int64
		wantRefunds   []int64
		wantLocked    []int64
	}{
		{
			name:          "buy beats sell on winning option",
			winningOption: "Yes",
			orders: []*settlementOrder{
				newTestOrder("a", "buy", "Yes", "filled", 1000, 1000, "USD"),
				newTestOrder("b", "sell", "Yes", "filled", 1000, 1000, "USD"),
			},
			wantPayouts: []int64{1000, 0},
			wantRefunds: []int64{0, 0},
			wantLocked:  []int64{0, 0},
		},
		{
			name:          "sell wins on a different option",
			winningOption: "Yes",
			orders: []*settlementOrder{
				newTestOrder("a", "sell", "No", "partial", 1000, 500, "USD"),
				newTestOrder("b", "buy", "No", "filled", 500, 500, "USD"),
			},
			wantPayouts: []int64{500, 0},
			wantRefunds: []int64{250, 0},
			wantLocked:  []int64{250, 0},
		},
		{
			name:          "proportional winners receive deterministic remainder",
			winningOption: "Yes",
			orders: []*settlementOrder{
				newTestOrder("a", "buy", "Yes", "filled", 333, 333, "USD"),
				newTestOrder("b", "buy", "Yes", "filled", 334, 334, "USD"),
				newTestOrder("c", "sell", "Yes", "filled", 334, 334, "USD"),
			},
			wantPayouts: []int64{333, 334, 0},
			wantRefunds: []int64{0, 0, 0},
			wantLocked:  []int64{0, 0, 0},
		},
		{
			name:          "no winner refunds filled stake",
			winningOption: "Yes",
			orders: []*settlementOrder{
				newTestOrder("a", "buy", "No", "filled", 250, 250, "USD"),
			},
			wantPayouts: []int64{0},
			wantRefunds: []int64{0},
			wantLocked:  []int64{0},
		},
		{
			name:          "currencies use independent pools",
			winningOption: "Yes",
			orders: []*settlementOrder{
				newTestOrder("a", "buy", "Yes", "filled", 100, 100, "USD"),
				newTestOrder("b", "sell", "Yes", "filled", 100, 100, "USD"),
				newTestOrder("c", "buy", "Yes", "filled", 300, 300, "EUR"),
				newTestOrder("d", "sell", "Yes", "filled", 300, 300, "EUR"),
			},
			wantPayouts: []int64{100, 0, 300, 0},
			wantRefunds: []int64{0, 0, 0, 0},
			wantLocked:  []int64{0, 0, 0, 0},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calculatePayouts(test.orders, test.winningOption)
			for index, order := range test.orders {
				assertBigInt(t, "payout", order.payout, test.wantPayouts[index])
				assertBigInt(t, "refund", order.refund, test.wantRefunds[index])
				assertBigInt(t, "locked use", order.lockedUse, test.wantLocked[index])
			}
		})
	}
}

func TestParseAndFormatCents(t *testing.T) {
	tests := map[string]string{
		"0": "0.00", "0.1": "0.10", "1.23": "1.23", "999999999999999999.99": "999999999999999999.99",
	}
	for input, want := range tests {
		value, err := parseCents(input)
		if err != nil {
			t.Fatalf("parseCents(%q) error = %v", input, err)
		}
		if got := formatCents(value); got != want {
			t.Errorf("formatCents(parseCents(%q)) = %q, want %q", input, got, want)
		}
	}
}

func TestSettleEventValidationAndErrors(t *testing.T) {
	repository := &stubRepository{err: ErrEventUnresolved}
	service := NewServiceWithRepository(repository)
	if err := service.SettleEvent(context.Background(), "not-a-uuid"); err == nil {
		t.Fatal("SettleEvent(invalid UUID) error = nil")
	}
	err := service.SettleEvent(context.Background(), "00000000-0000-4000-8000-000000000001")
	if !errors.Is(err, ErrEventUnresolved) {
		t.Fatalf("SettleEvent() error = %v, want ErrEventUnresolved", err)
	}
}

type stubRepository struct{ err error }

func (r *stubRepository) SettleEvent(context.Context, string, time.Time) error { return r.err }

func (r *stubRepository) SettleMarket(context.Context, string, string, time.Time) error { return r.err }

func (r *stubRepository) VoidMarket(context.Context, string, time.Time) error { return r.err }

func newTestOrder(
	id string,
	side string,
	option string,
	status string,
	amount int64,
	filled int64,
	currency string,
) *settlementOrder {
	return &settlementOrder{
		id: id, side: side, option: option, status: status, currency: currency,
		amount: big.NewInt(amount * 10_000), filled: big.NewInt(filled * 10_000),
		price: big.NewInt(500_000), stake: new(big.Int),
		merchantFeeRate: big.NewInt(0), platformFeeRate: big.NewInt(0),
	}
}

func assertBigInt(t *testing.T, field string, got *big.Int, want int64) {
	t.Helper()
	if got.Cmp(big.NewInt(want)) != 0 {
		t.Errorf("%s = %s, want %d", field, got, want)
	}
}
