package currency

import (
	"context"
	"sync"
)

type memoryRepository struct {
	mu    sync.RWMutex
	rates map[string]rateRecord
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{rates: map[string]rateRecord{}}
}

func (r *memoryRepository) GetLatest(
	_ context.Context,
	from string,
	to string,
) (rateRecord, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rate, ok := r.rates[rateKey(from, to)]
	if !ok {
		return rateRecord{}, ErrRateNotFound
	}
	return rate, nil
}

func (r *memoryRepository) Save(_ context.Context, rates []rateRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rate := range rates {
		key := rateKey(rate.From, rate.To)
		stored, ok := r.rates[key]
		if !ok || rate.Timestamp.After(stored.Timestamp) {
			r.rates[key] = rate
		}
	}
	return nil
}

func rateKey(from, to string) string {
	return from + ":" + to
}
