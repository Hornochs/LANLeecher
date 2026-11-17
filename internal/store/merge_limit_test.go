package store_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/lan-dot-party/lanleecher/internal/store"
)

func TestListMergeSuggestionsLimit(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	ev, err := db.StartEvent(ctx, "lan", "")
	if err != nil {
		t.Fatal(err)
	}
	n := store.MergeSuggestionLimit + 20
	for i := 0; i < n; i++ {
		a, b := fmt.Sprintf("02:00:00:01:%02x:%02x", i>>8, i&0xff), fmt.Sprintf("02:00:00:02:%02x:%02x", i>>8, i&0xff)
		for _, m := range []string{a, b} {
			if err := db.EnsureMAC(ctx, m, store.Now()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.CreateMergeSuggestion(ctx, a, b, "hostname:x", ev.ID); err != nil {
			t.Fatal(err)
		}
	}
	list, truncated, err := db.ListMergeSuggestions(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != store.MergeSuggestionLimit || !truncated {
		t.Fatalf("got %d suggestions, truncated=%v; want %d, true", len(list), truncated, store.MergeSuggestionLimit)
	}
}
