package audit

import (
	"context"
	"testing"
)

func TestMemoryStoreRecordsInOrder(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	first := Event{MerchantID: "m1", Method: "POST", Path: "/api/v2/orders", StatusCode: 201}
	second := Event{MerchantID: "m1", Method: "DELETE", Path: "/api/v2/orders/1", StatusCode: 200}
	if err := store.Record(ctx, first); err != nil {
		t.Fatalf("Record(first) error = %v", err)
	}
	if err := store.Record(ctx, second); err != nil {
		t.Fatalf("Record(second) error = %v", err)
	}
	events := store.Events()
	if len(events) != 2 || events[0] != first || events[1] != second {
		t.Fatalf("Events() = %#v", events)
	}
}

func TestNilStoreRecordFails(t *testing.T) {
	var store *MemoryStore
	if err := store.Record(context.Background(), Event{}); err == nil {
		t.Fatal("Record on nil store succeeded")
	}
}
