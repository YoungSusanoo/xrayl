//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"reflect"
	"sync"
	"testing"
	"time"

	"xrayl/internal/storage"
)

func requireSynonyms(t *testing.T, db *sql.DB, id int64, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow("SELECT array_to_json(synonyms)::text FROM meanings WHERE id = $1", id).Scan(&got); err != nil || got != want {
		t.Fatalf("synonyms: got %s, want %s, err=%v", got, want, err)
	}
}

func addSynonyms(ctx context.Context, db *sql.DB, userID int64, word, definition string, synonyms any) (storage.AddMeaningResult, error) {
	var r storage.AddMeaningResult
	err := db.QueryRowContext(ctx, "SELECT word_id, meaning_id, meaning_created FROM add_word_meaning($1,$2,$3,$4)", userID, word, definition, synonyms).Scan(&r.WordID, &r.MeaningID, &r.MeaningCreated)
	return r, err
}

func TestStoreAddMeaning(t *testing.T) {
	db := migrateTestDatabase(t)
	store := NewStore(db)
	ctx := context.Background()
	first, err := store.AddMeaning(ctx, 1001, " Book ", " Printed work ", []string{" Volume ", "volume", "", " Tome ", " VOLUME ", "tome", ""})
	if err != nil || !first.MeaningCreated || first.WordID <= 0 || first.MeaningID <= 0 {
		t.Fatalf("create: %+v, %v", first, err)
	}
	requireSynonyms(t, db, first.MeaningID, `["Volume","Tome"]`)
	for _, tc := range []struct {
		synonyms []string
		want     string
	}{
		{[]string{" Text ", "TEXT", "Novel"}, `["Text","Novel"]`},
		{[]string{}, `[]`},
		{[]string{"Volume"}, `["Volume"]`},
		{nil, `[]`},
	} {
		got, err := store.AddMeaning(ctx, 1001, "BOOK", " PRINTED WORK ", tc.synonyms)
		if err != nil || got.MeaningCreated || got.WordID != first.WordID || got.MeaningID != first.MeaningID {
			t.Fatalf("refresh: %+v, %v", got, err)
		}
		requireSynonyms(t, db, first.MeaningID, tc.want)
	}
	var word, definition string
	if err := db.QueryRow("SELECT w.text, m.definition FROM words w JOIN meanings m ON m.word_id=w.id WHERE m.id=$1", first.MeaningID).Scan(&word, &definition); err != nil || word != "Book" || definition != "Printed work" {
		t.Fatalf("spelling: %q, %q, %v", word, definition, err)
	}
	other, err := store.AddMeaning(ctx, 1002, "Book", "Printed work", []string{"Other"})
	if err != nil || !other.MeaningCreated || other.WordID == first.WordID || other.MeaningID == first.MeaningID {
		t.Fatalf("owner: %+v, %v", other, err)
	}
	requireSynonyms(t, db, first.MeaningID, `[]`)
	requireSynonyms(t, db, other.MeaningID, `["Other"]`)
	second, err := store.AddMeaning(ctx, 1001, "Book", "Reserve", nil)
	if err != nil || !second.MeaningCreated || second.WordID != first.WordID || second.MeaningID == first.MeaningID {
		t.Fatalf("second meaning: %+v, %v", second, err)
	}
	requireEntryCounts(t, db, 2, 2, 3)
}

func TestSynonymsSQLNormalizationAndNullPreservation(t *testing.T) {
	db := migrateTestDatabase(t)
	ctx := context.Background()
	first, err := addSynonyms(ctx, db, 1001, "Book", "Printed work", `{NULL,"  "," Volume ","volume","Tome"," VOLUME "," tome ","Novel"}`)
	if err != nil || !first.MeaningCreated {
		t.Fatalf("create: %+v, %v", first, err)
	}
	requireSynonyms(t, db, first.MeaningID, `["Volume","Tome","Novel"]`)
	preserved, err := addSynonyms(ctx, db, 1001, "BOOK", "PRINTED WORK", nil)
	if err != nil || preserved.MeaningCreated || preserved.MeaningID != first.MeaningID {
		t.Fatalf("SQL null refresh: %+v, %v", preserved, err)
	}
	requireSynonyms(t, db, first.MeaningID, `["Volume","Tome","Novel"]`)
	legacy, err := addMeaning(ctx, db, 1001, "book", "printed work")
	if err != nil || legacy.created || legacy.meaningID != first.MeaningID {
		t.Fatalf("legacy refresh: %+v, %v", legacy, err)
	}
	requireSynonyms(t, db, first.MeaningID, `["Volume","Tome","Novel"]`)
	fresh, err := addSynonyms(ctx, db, 1001, "Book", "Reserve", nil)
	if err != nil || !fresh.MeaningCreated {
		t.Fatalf("SQL null insert: %+v, %v", fresh, err)
	}
	requireSynonyms(t, db, fresh.MeaningID, `[]`)
	cleared, err := addSynonyms(ctx, db, 1001, "Book", "Printed work", `{NULL," "}`)
	if err != nil || cleared.MeaningCreated {
		t.Fatalf("blank refresh: %+v, %v", cleared, err)
	}
	requireSynonyms(t, db, first.MeaningID, `[]`)
	for _, signature := range []string{"add_word_meaning(bigint,text,text)", "add_word_meaning(bigint,text,text,text[])"} {
		var definer bool
		if err := db.QueryRow("SELECT prosecdef FROM pg_proc WHERE oid=to_regprocedure($1)", signature).Scan(&definer); err != nil || definer {
			t.Fatalf("security %s: %v, %v", signature, definer, err)
		}
	}
}

func TestStoreAddMeaningRollsBackFailedStatement(t *testing.T) {
	db := migrateTestDatabase(t)
	store := NewStore(db)
	ctx := context.Background()
	if _, err := db.Exec("ALTER TABLE meanings ADD CONSTRAINT reject_synonyms CHECK (NOT ('reject' = ANY(synonyms)))"); err != nil {
		t.Fatal(err)
	}
	_, err := store.AddMeaning(ctx, 1001, "Book", "Printed work", []string{"reject"})
	requireSQLState(t, err, "23514")
	requireEntryCounts(t, db, 0, 0, 0)
	first, err := store.AddMeaning(ctx, 1001, "Book", "Printed work", []string{"Volume"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AddMeaning(ctx, 1001, "Book", "Printed work", []string{"reject"})
	requireSQLState(t, err, "23514")
	requireSynonyms(t, db, first.MeaningID, `["Volume"]`)
}

func TestStoreAddMeaningConcurrentCalls(t *testing.T) {
	for _, setup := range []string{"", "INSERT INTO users(id) VALUES (1001)", "INSERT INTO users(id) VALUES (1001); INSERT INTO words(user_id,text) VALUES (1001,'Book')"} {
		t.Run(setup, func(t *testing.T) {
			db := migrateTestDatabase(t)
			if setup != "" {
				if _, err := db.Exec(setup); err != nil {
					t.Fatal(err)
				}
			}
			store := NewStore(db)
			const workers = 16
			db.SetMaxOpenConns(workers)
			type outcome struct {
				r   storage.AddMeaningResult
				err error
			}
			results := make(chan outcome, workers)
			start := make(chan struct{})
			var ready sync.WaitGroup
			ready.Add(workers)
			for worker := range workers {
				go func() {
					ready.Done()
					<-start
					word, definition := "Book", "Printed work"
					if worker%2 != 0 {
						word, definition = " BOOK ", " PRINTED WORK "
					}
					r, err := store.AddMeaning(context.Background(), 1001, word, definition, []string{" Volume ", "volume", "Tome"})
					results <- outcome{r, err}
				}()
			}
			ready.Wait()
			close(start)
			var first storage.AddMeaningResult
			created := 0
			for range workers {
				got := <-results
				if got.err != nil {
					t.Errorf("concurrent: %v", got.err)
					continue
				}
				if first.WordID == 0 {
					first = got.r
				}
				if got.r.WordID != first.WordID || got.r.MeaningID != first.MeaningID {
					t.Errorf("IDs: %+v vs %+v", got.r, first)
				}
				if got.r.MeaningCreated {
					created++
				}
			}
			if created != 1 {
				t.Fatalf("created=%d", created)
			}
			requireEntryCounts(t, db, 1, 1, 1)
			requireSynonyms(t, db, first.MeaningID, `["Volume","Tome"]`)
		})
	}
}

func TestStoreAddMeaningContextAndSaveTimeout(t *testing.T) {
	db := migrateTestDatabase(t)
	store := NewStore(db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.AddMeaning(ctx, 1001, "Book", "Printed work", nil); err == nil {
		t.Fatal("canceled context accepted")
	}
	requireEntryCounts(t, db, 0, 0, 0)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("LOCK TABLE meanings IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = store.AddMeaning(context.Background(), 1001, "Book", "Printed work", nil)
	elapsed := time.Since(start)
	if err == nil || elapsed < 4*time.Second || elapsed > 8*time.Second {
		t.Fatalf("save timeout: elapsed=%v, err=%v", elapsed, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestSynonymsMigrationUpgradeDownReapply(t *testing.T) {
	db := testDatabase(t)
	p, err := NewMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := p.UpTo(ctx, 2); err != nil {
		t.Fatal(err)
	}
	first, err := addMeaning(ctx, db, 1001, "Book", "Printed work")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	requireSynonyms(t, db, first.meaningID, `[]`)
	got, err := NewStore(db).AddMeaning(ctx, 1001, "BOOK", "PRINTED WORK", []string{"Volume"})
	if err != nil || got.MeaningCreated || got.WordID != first.wordID || got.MeaningID != first.meaningID {
		t.Fatalf("upgrade: %+v, %v", got, err)
	}
	if _, err := p.DownTo(ctx, 2); err != nil {
		t.Fatal(err)
	}
	legacy, err := addMeaning(ctx, db, 1001, "BOOK", "PRINTED WORK")
	if err != nil || !reflect.DeepEqual(legacy, meaningResult{first.wordID, first.meaningID, false}) {
		t.Fatalf("down: %+v, %v", legacy, err)
	}
	_, err = addSynonyms(ctx, db, 1001, "Book", "Printed work", nil)
	requireSQLState(t, err, "42883")
	var hasColumn bool
	if err := db.QueryRow("SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='meanings' AND column_name='synonyms')").Scan(&hasColumn); err != nil || hasColumn {
		t.Fatalf("column after down: %v, %v", hasColumn, err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	requireSynonyms(t, db, first.meaningID, `[]`)
	requireEntryCounts(t, db, 1, 1, 1)
}
