package fixed

import "testing"

func TestCentsFromString(t *testing.T) {
	tests := []struct {
		input string
		want  int64
		valid bool
	}{
		{input: "12", want: 1200, valid: true},
		{input: "12.3", want: 1230, valid: true},
		{input: "12.30", want: 1230, valid: true},
		{input: " 0.01 ", want: 1, valid: true},
		{input: "0", valid: false},
		{input: "1.001", valid: false},
		{input: "-1", valid: false},
		{input: "+1", valid: false},
		{input: "1e2", valid: false},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := CentsFromString(test.input)
			if test.valid && err != nil {
				t.Fatalf("CentsFromString(%q) error = %v", test.input, err)
			}
			if !test.valid && err == nil {
				t.Fatalf("CentsFromString(%q) succeeded", test.input)
			}
			if test.valid && got != test.want {
				t.Errorf("CentsFromString(%q) = %d, want %d", test.input, got, test.want)
			}
		})
	}
}

func TestFixedPointConversions(t *testing.T) {
	shares, err := SharesFromFloat(12.345678)
	if err != nil {
		t.Fatalf("SharesFromFloat() error = %v", err)
	}
	if got := FormatShares(shares); got != "12.345678" {
		t.Errorf("FormatShares() = %q, want 12.345678", got)
	}
	if _, err := SharesFromFloat(0.0000001); err == nil {
		t.Fatal("SharesFromFloat() accepted more than six decimals")
	}
	if _, err := CentsFromFloat(90_071_992_547_410); err == nil {
		t.Fatal("CentsFromFloat() accepted an out-of-range amount")
	}
}

func TestBinaryCollateralConservesPayout(t *testing.T) {
	shares, err := SharesFromFloat(10)
	if err != nil {
		t.Fatalf("SharesFromFloat() error = %v", err)
	}
	price, err := PriceFromFloat(0.333333)
	if err != nil {
		t.Fatalf("PriceFromFloat() error = %v", err)
	}
	buy, err := CollateralCents("buy", shares, price)
	if err != nil {
		t.Fatalf("buyer collateral error = %v", err)
	}
	sell, err := CollateralCents("sell", shares, price)
	if err != nil {
		t.Fatalf("seller collateral error = %v", err)
	}
	payout, err := PayoutCents(shares)
	if err != nil {
		t.Fatalf("PayoutCents() error = %v", err)
	}
	if buy+sell != payout {
		t.Errorf("binary collateral = %d + %d, payout = %d", buy, sell, payout)
	}
}
