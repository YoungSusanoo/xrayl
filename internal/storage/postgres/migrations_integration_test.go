//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func testDatabase(t *testing.T) *sql.DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL must point to a separate test database")
	}
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal("invalid TEST_DATABASE_URL")
	}
	admin := stdlib.OpenDB(*cfg)
	schema := fmt.Sprintf("migration_test_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	cfg.RuntimeParams["search_path"] = schema
	cfg.RuntimeParams["statement_timeout"] = "10000"
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() {
		db.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if _, err := admin.ExecContext(cleanupCtx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("clean up test schema: %v", err)
		}
		admin.Close()
	})
	return db
}

func migrateTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db := testDatabase(t)
	p, err := NewMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMigrationLifecycle(t *testing.T) {
	db := testDatabase(t)
	p, err := NewMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, direction := range []string{"up", "down", "up"} {
		if direction == "up" {
			_, err = p.Up(ctx)
		} else {
			_, err = p.DownTo(ctx, 0)
		}
		if err != nil {
			t.Fatalf("%s: %v", direction, err)
		}
		for _, table := range []string{"users", "words", "meanings", "sources", "contexts"} {
			var exists bool
			if err := db.QueryRow("SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists != (direction == "up") {
				t.Fatalf("table %s after %s: exists=%v", table, direction, exists)
			}
		}
	}
	results, err := p.Up(ctx)
	if err != nil || len(results) != 0 {
		t.Fatalf("repeated up must leave applied schema alone: %v, %v", results, err)
	}
	status, err := p.Status(ctx)
	if err != nil || len(status) != 6 {
		t.Fatalf("migration status: %v, %v", status, err)
	}
	for _, migration := range status {
		if migration.State != goose.StateApplied {
			t.Fatalf("migration not applied: %v", migration)
		}
	}
}

type fixture struct {
	word1, word2, meaning1, meaning2, source1, source2 int64
}

func seed(t *testing.T, db *sql.DB) fixture {
	t.Helper()
	if _, err := db.Exec("INSERT INTO users(id) VALUES (1001), (1002)"); err != nil {
		t.Fatal(err)
	}
	insert := func(query string, args ...any) int64 {
		var id int64
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f := fixture{}
	f.word1 = insert("INSERT INTO words(user_id, text) VALUES (1001, 'Book') RETURNING id")
	f.word2 = insert("INSERT INTO words(user_id, text) VALUES (1002, 'Book') RETURNING id")
	f.meaning1 = insert("INSERT INTO meanings(user_id, word_id, definition) VALUES (1001, $1, 'printed work') RETURNING id", f.word1)
	f.meaning2 = insert("INSERT INTO meanings(user_id, word_id, definition) VALUES (1002, $1, 'printed work') RETURNING id", f.word2)
	f.source1 = insert("INSERT INTO sources(user_id, name) VALUES (1001, 'Dune') RETURNING id")
	f.source2 = insert("INSERT INTO sources(user_id, name) VALUES (1002, 'Dune') RETURNING id")
	insert("INSERT INTO contexts(user_id, meaning_id, source_id) VALUES (1001, $1, $2) RETURNING id", f.meaning1, f.source1)
	insert("INSERT INTO contexts(user_id, meaning_id, source_id) VALUES (1002, $1, $2) RETURNING id", f.meaning2, f.source2)
	return f
}

func TestSchemaRejectsDuplicatesAndForeignOwners(t *testing.T) {
	db := migrateTestDatabase(t)
	f := seed(t, db)
	for _, tc := range []struct {
		name, query, code string
		args              []any
	}{
		{"word case", "INSERT INTO words(user_id, text) VALUES (1001, 'book')", "23505", nil},
		{"source case", "INSERT INTO sources(user_id, name) VALUES (1001, 'DUNE')", "23505", nil},
		{"foreign word", "INSERT INTO meanings(user_id, word_id, definition) VALUES (1001, $1, 'other')", "23503", []any{f.word2}},
		{"foreign meaning", "INSERT INTO contexts(user_id, meaning_id, source_id) VALUES (1001, $1, $2)", "23503", []any{f.meaning2, f.source1}},
		{"foreign source", "INSERT INTO contexts(user_id, meaning_id, source_id) VALUES (1001, $1, $2)", "23503", []any{f.meaning1, f.source2}},
		{"blank word", "INSERT INTO words(user_id, text) VALUES (1001, '   ')", "23514", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.Exec(tc.query, tc.args...)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != tc.code {
				t.Fatalf("wanted PostgreSQL error %s, got %v", tc.code, err)
			}
		})
	}
	if _, err := db.Exec("INSERT INTO contexts(user_id, meaning_id, source_id, quote) VALUES (1001, $1, $2, 'another example / page 42')", f.meaning1, f.source1); err != nil {
		t.Fatalf("multiple contexts in one source must be allowed: %v", err)
	}
}

func TestSchemaCascades(t *testing.T) {
	for _, tc := range []struct {
		name, query                        string
		words, meanings, sources, contexts int
	}{
		{"user", "DELETE FROM users WHERE id = 1001", 0, 0, 0, 0},
		{"word", "DELETE FROM words WHERE user_id = 1001", 0, 0, 1, 0},
		{"meaning", "DELETE FROM meanings WHERE user_id = 1001", 1, 0, 1, 0},
		{"source", "DELETE FROM sources WHERE user_id = 1001", 1, 1, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := migrateTestDatabase(t)
			seed(t, db)
			if _, err := db.Exec(tc.query); err != nil {
				t.Fatal(err)
			}
			for table, want := range map[string]int{"words": tc.words, "meanings": tc.meanings, "sources": tc.sources, "contexts": tc.contexts} {
				for owner, expected := range map[int64]int{1001: want, 1002: 1} {
					var count int
					if err := db.QueryRow("SELECT count(*) FROM "+table+" WHERE user_id = $1", owner).Scan(&count); err != nil {
						t.Fatal(err)
					}
					if count != expected {
						t.Fatalf("%s for user %d: wanted %d rows, got %d", table, owner, expected, count)
					}
				}
			}
		})
	}
}

func TestFailedTransactionLeavesNoPartialEntry(t *testing.T) {
	db := migrateTestDatabase(t)
	if _, err := db.Exec("INSERT INTO users(id) VALUES (1001)"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec("INSERT INTO words(user_id, text) VALUES (1001, 'Example')"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO meanings(user_id, word_id, definition) VALUES (1001, -1, 'invalid')"); err == nil {
		t.Fatal("invalid reference was accepted")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM words").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial entry remained: count=%d, err=%v", count, err)
	}
}
