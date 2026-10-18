//go:build integration

package postgres

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"xrayl/internal/storage"
)

func TestStoreSourcesAndContexts(t *testing.T) {
	db := migrateTestDatabase(t)
	s := NewStore(db)
	ctx := context.Background()
	source, err := s.AddSource(ctx, 42, " Dune ")
	if err != nil || source.Source.ID == 0 || source.Source.Name != "Dune" || !source.Created {
		t.Fatalf("source: %+v %v", source, err)
	}
	duplicate, err := s.AddSource(ctx, 42, "DUNE")
	if err != nil || duplicate.Created || duplicate.Source != source.Source {
		t.Fatalf("duplicate: %+v %v", duplicate, err)
	}
	if _, err := s.AddSource(ctx, 42, "apple"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddSource(ctx, 43, "Hidden"); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListSources(ctx, 42, 1, 1)
	if err != nil || !reflect.DeepEqual(page, []storage.Source{source.Source}) {
		t.Fatalf("page: %+v %v", page, err)
	}
	meaning, err := s.AddMeaning(ctx, 42, "book", "Printed work.", nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.AddContext(ctx, 42, meaning.MeaningID, source.Source.ID, " A book. ", "")
	if err != nil || id == 0 {
		t.Fatalf("save context: %d %v", id, err)
	}
	got, err := s.ListWordContexts(ctx, 42, meaning.WordID)
	want := []storage.WordContext{{ID: id, MeaningID: meaning.MeaningID, Source: source.Source, Quote: "A book."}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("contexts: %+v %v", got, err)
	}
	if _, err := db.Exec("INSERT INTO contexts(user_id,meaning_id,source_id) VALUES(42,$1,$2)", meaning.MeaningID, source.Source.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ListWordContexts(ctx, 42, meaning.WordID); err != nil || len(got) != 2 || got[1].Quote != "" || got[1].Location != "" {
		t.Fatalf("legacy NULL context: %+v %v", got, err)
	}
	if got, err := s.ListWordContexts(ctx, 43, meaning.WordID); err != nil || len(got) != 0 {
		t.Fatalf("foreign read: %+v %v", got, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = s.AddSource(canceled, 42, "Canceled")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel source: %v", err)
	}
	_, err = s.ListSources(canceled, 42, 21, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel sources: %v", err)
	}
	_, err = s.AddContext(canceled, 42, meaning.MeaningID, source.Source.ID, "Canceled", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel context: %v", err)
	}
	_, err = s.ListWordContexts(canceled, 42, meaning.WordID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel contexts: %v", err)
	}
}

func TestConcurrentAddSourceReturnsSameSource(t *testing.T) {
	db := migrateTestDatabase(t)
	s := NewStore(db)
	const workers = 8
	results := make(chan storage.AddSourceResult, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			got, err := s.AddSource(context.Background(), 42, "Dune")
			if err != nil {
				t.Error(err)
			}
			results <- got
		})
	}
	wg.Wait()
	close(results)
	created := 0
	var id int64
	for got := range results {
		if got.Created {
			created++
		}
		if id == 0 {
			id = got.Source.ID
		}
		if got.Source.ID == 0 || got.Source.ID != id {
			t.Fatalf("different source: %+v", got)
		}
	}
	if created != 1 {
		t.Fatalf("created=%d", created)
	}
}
