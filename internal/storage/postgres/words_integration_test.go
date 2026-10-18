//go:build integration

package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"xrayl/internal/storage"
)

func TestStoreReadsWordsAndMeanings(t *testing.T) {
	db := migrateTestDatabase(t)
	s := NewStore(db)
	ctx := context.Background()
	first, err := s.AddMeaning(ctx, 42, "Book", "Printed work.", []string{"volume"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.AddMeaning(ctx, 42, "BOOK", "To reserve.", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMeaning(ctx, 42, "apple", "Fruit.", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddMeaning(ctx, 43, "hidden", "Private.", nil); err != nil {
		t.Fatal(err)
	}
	words, err := s.ListWords(ctx, 42, 21, 0)
	if err != nil || len(words) != 2 || words[0].Text != "apple" || words[1] != (storage.Word{ID: first.WordID, Text: "Book"}) {
		t.Fatalf("words: %+v, %v", words, err)
	}
	page, err := s.ListWords(ctx, 42, 1, 1)
	if err != nil || len(page) != 1 || page[0] != words[1] {
		t.Fatalf("page: %+v, %v", page, err)
	}
	meanings, err := s.ListMeanings(ctx, 42, first.WordID)
	want := []storage.Meaning{{ID: first.MeaningID, Definition: "Printed work.", Synonyms: []string{"volume"}}, {ID: second.MeaningID, Definition: "To reserve.", Synonyms: []string{}}}
	if err != nil || !reflect.DeepEqual(meanings, want) {
		t.Fatalf("meanings: %+v, %v", meanings, err)
	}
	for _, owner := range []int64{43, 999} {
		got, err := s.ListMeanings(ctx, owner, first.WordID)
		if err != nil || len(got) != 0 {
			t.Fatalf("foreign meanings: %+v, %v", got, err)
		}
	}
	if got, err := s.ListWords(ctx, 999, 21, 0); err != nil || len(got) != 0 {
		t.Fatalf("empty words: %+v, %v", got, err)
	}
	if got, err := s.ListMeanings(ctx, 42, -1); err != nil || len(got) != 0 {
		t.Fatalf("unknown word: %+v, %v", got, err)
	}
	_, err = s.ListWords(ctx, 42, 0, 0)
	requireSQLState(t, err, "22023")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.ListWords(canceled, 42, 21, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled list: %v", err)
	}
	if _, err := s.ListMeanings(canceled, 42, first.WordID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled meanings: %v", err)
	}
}

func TestStoreReadsHaveTimeouts(t *testing.T) {
	for _, which := range []string{"words", "meanings"} {
		t.Run(which, func(t *testing.T) {
			db := migrateTestDatabase(t)
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec("LOCK TABLE " + which + " IN ACCESS EXCLUSIVE MODE"); err != nil {
				t.Fatal(err)
			}
			s := NewStore(db)
			start := time.Now()
			if which == "words" {
				_, err = s.ListWords(context.Background(), 42, 21, 0)
			} else {
				_, err = s.ListMeanings(context.Background(), 42, 1)
			}
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 8*time.Second {
				t.Fatalf("timeout: %v after %v", err, time.Since(start))
			}
		})
	}
}

func TestReadWordFunctions(t *testing.T) {
	db := migrateTestDatabase(t)
	ctx := context.Background()
	first, err := NewStore(db).AddMeaning(ctx, 42, "Book", "Printed work.", []string{"volume"})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		user             int64
		word, definition string
	}{
		{42, "BOOK", "To reserve."}, {42, "apple", "Fruit."}, {42, "Zebra", "Animal."}, {43, "hidden", "Private."},
	} {
		if _, err := NewStore(db).AddMeaning(ctx, item.user, item.word, item.definition, nil); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT word_id, word FROM list_words($1,$2,$3)", 42, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	var words []string
	for rows.Next() {
		var id int64
		var word string
		if err := rows.Scan(&id, &word); err != nil {
			t.Fatal(err)
		}
		words = append(words, word)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(words) != 2 || words[0] != "apple" || words[1] != "Book" {
		t.Fatalf("word page: %v", words)
	}
	var last string
	if err := db.QueryRowContext(ctx, "SELECT word FROM list_words($1,$2,$3)", 42, 2, 2).Scan(&last); err != nil || last != "Zebra" {
		t.Fatalf("last page: %q, %v", last, err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM list_word_meanings($1,$2)", 42, first.WordID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("meanings: %d, %v", count, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM list_word_meanings($1,$2)", 43, first.WordID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("owner isolation: %d, %v", count, err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM list_words($1,$2,$3)", 999, 21, 0).Scan(&count); err != nil || count != 0 {
		t.Fatalf("empty user: %d, %v", count, err)
	}
	for _, args := range [][]any{{42, 0, 0}, {42, 21, -1}, {nil, 21, 0}, {42, nil, 0}, {42, 21, nil}} {
		_, err := db.ExecContext(ctx, "SELECT * FROM list_words($1,$2,$3)", args...)
		requireSQLState(t, err, "22023")
	}
	for _, signature := range []string{"list_words(bigint,integer,integer)", "list_word_meanings(bigint,bigint)"} {
		var definer bool
		if err := db.QueryRow("SELECT prosecdef FROM pg_proc WHERE oid=to_regprocedure($1)", signature).Scan(&definer); err != nil || definer {
			t.Fatalf("security: %s, %v", signature, err)
		}
	}
}

func TestReadWordMigrationRollback(t *testing.T) {
	db := migrateTestDatabase(t)
	ctx := context.Background()
	if _, err := NewStore(db).AddMeaning(ctx, 42, "book", "Printed work.", nil); err != nil {
		t.Fatal(err)
	}
	p, err := NewMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.DownTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	var missing bool
	if err := db.QueryRow("SELECT to_regprocedure('list_words(bigint,integer,integer)') IS NULL AND to_regprocedure('list_word_meanings(bigint,bigint)') IS NULL").Scan(&missing); err != nil || !missing {
		t.Fatalf("functions after rollback: %v, %v", missing, err)
	}
	requireEntryCounts(t, db, 1, 1, 1)
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM list_words(42,21,0)").Scan(&count); err != nil || count != 1 {
		t.Fatalf("reapply: %d, %v", count, err)
	}
}
