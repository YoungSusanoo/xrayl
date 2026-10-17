//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type meaningResult struct {
	wordID, meaningID int64
	created           bool
}

func addMeaning(ctx context.Context, db *sql.DB, userID, word, definition any) (meaningResult, error) {
	var result meaningResult
	err := db.QueryRowContext(ctx, "SELECT word_id, meaning_id, meaning_created FROM add_word_meaning($1, $2, $3)",
		userID, word, definition).Scan(&result.wordID, &result.meaningID, &result.created)
	return result, err
}

func requireSQLState(t *testing.T, err error, code string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != code {
		t.Fatalf("wanted PostgreSQL error %s, got %v", code, err)
	}
}

func requireEntryCounts(t *testing.T, db *sql.DB, users, words, meanings int) {
	t.Helper()
	for table, want := range map[string]int{"users": users, "words": words, "meanings": meanings} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%s: wanted %d rows, got %d", table, want, count)
		}
	}
}

func TestAddWordMeaningCreatesAndReusesEntries(t *testing.T) {
	db := migrateTestDatabase(t)
	ctx := context.Background()
	first, err := addMeaning(ctx, db, 1001, " Book ", " Printed work ")
	if err != nil {
		t.Fatal(err)
	}
	if !first.created || first.wordID <= 0 || first.meaningID <= 0 {
		t.Fatalf("invalid first result: %+v", first)
	}
	var word, definition string
	var userID int64
	if err := db.QueryRow(`SELECT w.text, m.definition, u.id
		FROM meanings m JOIN words w ON (w.user_id, w.id) = (m.user_id, m.word_id)
		JOIN users u ON u.id = w.user_id WHERE m.id = $1`, first.meaningID).
		Scan(&word, &definition, &userID); err != nil {
		t.Fatal(err)
	}
	if word != "Book" || definition != "Printed work" || userID != 1001 {
		t.Fatalf("unexpected saved entry: %q, %q, %d", word, definition, userID)
	}
	for _, input := range []struct{ word, definition string }{
		{"Book", "Printed work"}, {" book ", " PRINTED WORK "},
	} {
		repeated, err := addMeaning(ctx, db, 1001, input.word, input.definition)
		if err != nil || repeated.created || repeated.wordID != first.wordID || repeated.meaningID != first.meaningID {
			t.Fatalf("duplicate changed entry: %+v, %v", repeated, err)
		}
	}
	second, err := addMeaning(ctx, db, 1001, "BOOK", "Reserve")
	if err != nil || !second.created || second.wordID != first.wordID || second.meaningID == first.meaningID {
		t.Fatalf("second meaning: %+v, %v", second, err)
	}
	otherUser, err := addMeaning(ctx, db, 1002, "Book", "Printed work")
	if err != nil || !otherUser.created || otherUser.wordID == first.wordID || otherUser.meaningID == first.meaningID {
		t.Fatalf("another user's entry: %+v, %v", otherUser, err)
	}
	otherWord, err := addMeaning(ctx, db, 1001, "Volume", "Printed work")
	if err != nil || !otherWord.created || otherWord.wordID == first.wordID || otherWord.meaningID == first.meaningID {
		t.Fatalf("another word's entry: %+v, %v", otherWord, err)
	}
	if err := db.QueryRow("SELECT text FROM words WHERE id = $1", first.wordID).Scan(&word); err != nil || word != "Book" {
		t.Fatalf("original spelling changed: %q, %v", word, err)
	}
	requireEntryCounts(t, db, 2, 3, 4)
}

func TestAddWordMeaningRejectsInvalidInput(t *testing.T) {
	db := migrateTestDatabase(t)
	for _, tc := range []struct {
		name                     string
		userID, word, definition any
	}{
		{"null user", nil, "Book", "Printed work"},
		{"null word", 1001, nil, "Printed work"},
		{"null definition", 1001, "Book", nil},
		{"empty word", 1001, "", "Printed work"},
		{"blank word", 1001, "   ", "Printed work"},
		{"empty definition", 1001, "Book", ""},
		{"blank definition", 1001, "Book", "   "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := addMeaning(context.Background(), db, tc.userID, tc.word, tc.definition)
			requireSQLState(t, err, "22023")
			requireEntryCounts(t, db, 0, 0, 0)
		})
	}
}

func TestAddWordMeaningRollsBackOnInsertFailure(t *testing.T) {
	db := migrateTestDatabase(t)
	if _, err := db.Exec("ALTER TABLE meanings ADD CONSTRAINT reject_test_definition CHECK (definition <> 'reject')"); err != nil {
		t.Fatal(err)
	}
	_, err := addMeaning(context.Background(), db, 1001, "Book", "reject")
	requireSQLState(t, err, "23514")
	requireEntryCounts(t, db, 0, 0, 0)
}

func TestMeaningIndexRejectsDirectDuplicates(t *testing.T) {
	db := migrateTestDatabase(t)
	entry, err := addMeaning(context.Background(), db, 1001, "Book", "Printed work")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO meanings(user_id, word_id, definition) VALUES (1001, $1, ' PRINTED WORK ')", entry.wordID)
	requireSQLState(t, err, "23505")
	requireEntryCounts(t, db, 1, 1, 1)
}

func TestAddWordMeaningConcurrentCalls(t *testing.T) {
	for _, tc := range []struct{ name, setup string }{
		{"new user", ""},
		{"existing user", "INSERT INTO users(id) VALUES (1001)"},
		{"existing word", "INSERT INTO users(id) VALUES (1001); INSERT INTO words(user_id, text) VALUES (1001, 'Book')"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := migrateTestDatabase(t)
			if tc.setup != "" {
				if _, err := db.Exec(tc.setup); err != nil {
					t.Fatal(err)
				}
			}
			testConcurrentAddWordMeaning(t, db)
		})
	}
}

func testConcurrentAddWordMeaning(t *testing.T, db *sql.DB) {
	t.Helper()
	const workers = 16
	db.SetMaxOpenConns(workers)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	type outcome struct {
		result meaningResult
		err    error
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
			if worker%2 == 1 {
				word, definition = " BOOK ", " PRINTED WORK "
			}
			result, err := addMeaning(ctx, db, 1001, word, definition)
			results <- outcome{result, err}
		}()
	}
	ready.Wait()
	close(start)
	var first meaningResult
	created := 0
	for range workers {
		got := <-results
		if got.err != nil {
			t.Errorf("concurrent call: %v", got.err)
			continue
		}
		if first.wordID == 0 {
			first = got.result
		}
		if got.result.wordID != first.wordID || got.result.meaningID != first.meaningID {
			t.Errorf("different IDs: %+v vs %+v", got.result, first)
		}
		if got.result.created {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("wanted exactly one creation, got %d", created)
	}
	requireEntryCounts(t, db, 1, 1, 1)
}

func TestAddWordMeaningMigrationRollbackPreservesData(t *testing.T) {
	db := migrateTestDatabase(t)
	ctx := context.Background()
	entry, err := addMeaning(ctx, db, 1001, "Book", "Printed work")
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Down(ctx); err != nil {
		t.Fatal(err)
	}
	requireEntryCounts(t, db, 1, 1, 1)
	_, err = addMeaning(ctx, db, 1001, "Book", "Printed work")
	requireSQLState(t, err, "42883")
	// Without the second migration's index, direct duplicates are allowed again.
	var duplicateID int64
	if err := db.QueryRow("INSERT INTO meanings(user_id, word_id, definition) VALUES (1001, $1, ' PRINTED WORK ') RETURNING id", entry.wordID).Scan(&duplicateID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM meanings WHERE id = $1", duplicateID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatal(err)
	}
	repeated, err := addMeaning(ctx, db, 1001, "BOOK", "PRINTED WORK")
	if err != nil || repeated.created || repeated.wordID != entry.wordID || repeated.meaningID != entry.meaningID {
		t.Fatalf("reapply changed entry: %+v, %v", repeated, err)
	}
	var securityDefiner bool
	if err := db.QueryRow("SELECT prosecdef FROM pg_proc WHERE oid = 'add_word_meaning(bigint,text,text)'::regprocedure").Scan(&securityDefiner); err != nil || securityDefiner {
		t.Fatalf("function must use invoker privileges: definer=%v, err=%v", securityDefiner, err)
	}
}

func TestAddWordMeaningMigrationRejectsExistingDuplicates(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	p, err := NewMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users(id) VALUES (1001);
		INSERT INTO words(user_id, text) VALUES (1001, 'Book');
		INSERT INTO meanings(user_id, word_id, definition)
		SELECT 1001, id, d FROM words CROSS JOIN (VALUES ('Printed work'), (' PRINTED WORK ')) AS definitions(d)`); err != nil {
		t.Fatal(err)
	}
	_, err = p.Up(ctx)
	requireSQLState(t, err, "23505")
	requireEntryCounts(t, db, 1, 1, 2)
	version, err := p.GetDBVersion(ctx)
	if err != nil || version != 1 {
		t.Fatalf("failed migration advanced version: %d, %v", version, err)
	}
	var definition string
	if err := db.QueryRow("SELECT definition FROM meanings ORDER BY id LIMIT 1").Scan(&definition); err != nil || definition != "Printed work" {
		t.Fatalf("existing data changed: %q, %v", definition, err)
	}
	var functionExists bool
	if err := db.QueryRow("SELECT to_regprocedure('add_word_meaning(bigint,text,text)') IS NOT NULL").Scan(&functionExists); err != nil || functionExists {
		t.Fatalf("failed migration left function: %v, %v", functionExists, err)
	}
}
