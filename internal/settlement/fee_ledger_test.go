package settlement

import (
	"math/big"
	"testing"
)

// TestFormatRate covers the 6-decimal fixed-point rendering used for
// fee_ledger.rate, including values below 1.0 that need zero padding.
func TestFormatRate(t *testing.T) {
	tests := []struct {
		name string
		rate int64
		want string
	}{
		{name: "zero", rate: 0, want: "0.000000"},
		{name: "one_percent", rate: 10_000, want: "0.010000"},
		{name: "two_and_a_half_percent", rate: 25_000, want: "0.025000"},
		{name: "whole", rate: 1_000_000, want: "1.000000"},
		{name: "smallest_unit", rate: 1, want: "0.000001"},
		{name: "above_one", rate: 1_500_000, want: "1.500000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatRate(big.NewInt(tt.rate)); got != tt.want {
				t.Errorf("formatRate(%d) = %q, want %q", tt.rate, got, tt.want)
			}
		})
	}
}
