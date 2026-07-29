package currency

import (
	"fmt"
	"math/big"
	"strings"
)

const rateScale = int64(100_000_000)

func crossRates(snapshot rateSnapshot) ([]rateRecord, error) {
	provider := strings.TrimSpace(snapshot.Provider)
	if provider == "" {
		return nil, fmt.Errorf("provider is required")
	}
	if snapshot.Timestamp.IsZero() {
		return nil, fmt.Errorf("timestamp is required")
	}
	rates := make([]rateRecord, 0, len(supportedCurrencies)*len(supportedCurrencies))
	for _, from := range supportedCurrencies {
		fromRate, err := decimalRat(snapshot.Rates[from])
		if err != nil {
			return nil, fmt.Errorf("parse %s base rate: %w", from, err)
		}
		for _, to := range supportedCurrencies {
			toRate, err := decimalRat(snapshot.Rates[to])
			if err != nil {
				return nil, fmt.Errorf("parse %s base rate: %w", to, err)
			}
			value := new(big.Rat).Quo(toRate, fromRate)
			rates = append(rates, rateRecord{
				From:      from,
				To:        to,
				Value:     formatPositiveRat(value, rateScale, 8),
				Provider:  provider,
				Timestamp: snapshot.Timestamp.UTC(),
			})
		}
	}
	return rates, nil
}

func multiplyCents(cents int64, rate string) (int64, error) {
	rateValue, err := decimalRat(rate)
	if err != nil {
		return 0, err
	}
	numerator := new(big.Int).Mul(big.NewInt(cents), rateValue.Num())
	converted := roundPositiveQuotient(numerator, rateValue.Denom())
	if !converted.IsInt64() {
		return 0, fmt.Errorf("converted amount exceeds the supported range")
	}
	return converted.Int64(), nil
}

func decimalRat(value string) (*big.Rat, error) {
	rate, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	if !ok || rate.Sign() <= 0 {
		return nil, fmt.Errorf("invalid positive decimal %q", value)
	}
	return rate, nil
}

func formatPositiveRat(value *big.Rat, scale int64, decimalPlaces int) string {
	scaledNumerator := new(big.Int).Mul(value.Num(), big.NewInt(scale))
	scaled := roundPositiveQuotient(scaledNumerator, value.Denom())
	digits := scaled.String()
	if len(digits) <= decimalPlaces {
		digits = strings.Repeat("0", decimalPlaces+1-len(digits)) + digits
	}
	return digits[:len(digits)-decimalPlaces] + "." + digits[len(digits)-decimalPlaces:]
}

func roundPositiveQuotient(numerator, denominator *big.Int) *big.Int {
	quotient := new(big.Int)
	remainder := new(big.Int)
	quotient.QuoRem(numerator, denominator, remainder)
	if new(big.Int).Mul(remainder, big.NewInt(2)).Cmp(denominator) >= 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	return quotient
}
