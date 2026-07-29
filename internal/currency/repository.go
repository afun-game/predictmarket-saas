package currency

import (
	"context"
	"time"
)

type rateRecord struct {
	From      string
	To        string
	Value     string
	Provider  string
	Timestamp time.Time
}

type rateRepository interface {
	GetLatest(ctx context.Context, from, to string) (rateRecord, error)
	Save(ctx context.Context, rates []rateRecord) error
}
