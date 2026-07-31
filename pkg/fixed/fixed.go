// Package fixed provides exact fixed-point primitives for share, price, and
// currency calculations at service boundaries.
package fixed

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

const (
	// ShareScale stores one share as one million integer units.
	ShareScale int64 = 1_000_000
	// PriceScale stores a probability price with six decimal places.
	PriceScale int64 = 1_000_000
	// CentsScale stores one currency unit as one hundred integer cents.
	CentsScale int64 = 100

	maxShareUnits int64 = 8_000_000_000_000_000
	maxCents      int64 = 8_000_000_000_000_000
)

var ErrOutOfRange = errors.New("fixed-point value is out of range")

// SharesFromFloat accepts a positive share value with at most six decimals.
func SharesFromFloat(value float64) (int64, error) {
	return scaledFromFloat(value, ShareScale, maxShareUnits, "shares")
}

// PriceFromFloat accepts a probability price in the inclusive [0, 1] range.
func PriceFromFloat(value float64) (int64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return 0, fmt.Errorf("price: %w", ErrOutOfRange)
	}
	return scaledFromFloat(value, PriceScale, PriceScale, "price")
}

// CentsFromFloat accepts a positive currency value with at most two decimals.
func CentsFromFloat(value float64) (int64, error) {
	return scaledFromFloat(value, CentsScale, maxCents, "amount")
}

// CentsFromString accepts a positive currency string with at most two decimal
// places. It avoids a float conversion at HTTP boundaries.
func CentsFromString(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return 0, fmt.Errorf("amount: %w", ErrOutOfRange)
	}
	whole, fraction, hasFraction := strings.Cut(value, ".")
	if whole == "" || !allDigits(whole) || (hasFraction && (!allDigits(fraction) || len(fraction) > 2)) {
		return 0, fmt.Errorf("amount: %w", ErrOutOfRange)
	}
	if !hasFraction {
		fraction = ""
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	wholeAmount, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || wholeAmount > maxCents/CentsScale {
		return 0, fmt.Errorf("amount: %w", ErrOutOfRange)
	}
	fractionAmount, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("amount: %w", ErrOutOfRange)
	}
	amount := wholeAmount*CentsScale + fractionAmount
	if amount <= 0 || amount > maxCents {
		return 0, fmt.Errorf("amount: %w", ErrOutOfRange)
	}
	return amount, nil
}

// SharesToFloat converts stored share units to an API-facing number.
func SharesToFloat(value int64) float64 {
	return float64(value) / float64(ShareScale)
}

// PriceToFloat converts stored price units to an API-facing number.
func PriceToFloat(value int64) float64 {
	return float64(value) / float64(PriceScale)
}

// CentsToFloat converts stored cents to an API-facing currency number.
func CentsToFloat(value int64) float64 {
	return float64(value) / float64(CentsScale)
}

// FormatShares returns a PostgreSQL numeric literal with six decimal places.
func FormatShares(value int64) string {
	return formatScaled(value, ShareScale, 6)
}

// FormatPrice returns a PostgreSQL numeric literal with six decimal places.
func FormatPrice(value int64) string {
	return formatScaled(value, PriceScale, 6)
}

// FormatCents returns a PostgreSQL numeric literal with two decimal places.
func FormatCents(value int64) string {
	return formatScaled(value, CentsScale, 2)
}

// CollateralCents returns the rounded currency collateral for a binary order.
// Sell orders cover the complementary result of the selected option.
func CollateralCents(side string, shares, price int64) (int64, error) {
	exposurePrice := price
	if side == "sell" {
		exposurePrice = PriceScale - price
	}
	return productCents(shares, exposurePrice)
}

// PriceImprovementCents returns the rounded collateral released to a taker
// whose limit price improves at execution.
func PriceImprovementCents(side string, shares, limitPrice, matchedPrice int64) (int64, error) {
	difference := limitPrice - matchedPrice
	if side == "sell" {
		difference = matchedPrice - limitPrice
	}
	if difference <= 0 {
		return 0, nil
	}
	return productCents(shares, difference)
}

// PayoutCents returns the at-par settlement value of shares.
func PayoutCents(shares int64) (int64, error) {
	return divideRounded(new(big.Int).Mul(big.NewInt(shares), big.NewInt(CentsScale)), big.NewInt(ShareScale))
}

func scaledFromFloat(value float64, scale, maximum int64, field string) (int64, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return 0, fmt.Errorf("%s: %w", field, ErrOutOfRange)
	}
	scaled := value * float64(scale)
	if scaled > float64(maximum) {
		return 0, fmt.Errorf("%s: %w", field, ErrOutOfRange)
	}
	rounded := math.Round(scaled)
	if math.Abs(scaled-rounded) > 1e-6 {
		return 0, fmt.Errorf("%s must have at most %d decimal places", field, decimalPlaces(scale))
	}
	return int64(rounded), nil
}

func productCents(shares, price int64) (int64, error) {
	product := new(big.Int).Mul(big.NewInt(shares), big.NewInt(price))
	product.Mul(product, big.NewInt(CentsScale))
	denominator := new(big.Int).Mul(big.NewInt(ShareScale), big.NewInt(PriceScale))
	return divideRounded(product, denominator)
}

func divideRounded(value, denominator *big.Int) (int64, error) {
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(value, denominator, remainder)
	if remainder.Lsh(remainder, 1).Cmp(denominator) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return 0, ErrOutOfRange
	}
	return quotient.Int64(), nil
}

func formatScaled(value, scale int64, decimals int) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	whole := value / scale
	fraction := value % scale
	return sign + strconv.FormatInt(whole, 10) + "." + strings.Repeat("0", decimals-len(strconv.FormatInt(fraction, 10))) + strconv.FormatInt(fraction, 10)
}

func decimalPlaces(scale int64) int {
	return len(strconv.FormatInt(scale, 10)) - 1
}

func allDigits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
