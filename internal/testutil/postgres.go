//go:build integration

package testutil

import (
	"context"
	"crypto/rand"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func Database(t testing.TB) *sql.DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL must point to a separate test database")
	}
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal("invalid TEST_DATABASE_URL")
	}
	schema := "integration_test_" + strings.ToLower(rand.Text())
	admin := stdlib.OpenDB(*cfg)
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

func RequireEntryCounts(t testing.TB, db *sql.DB, users, words, meanings int) {
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

func RequireSynonyms(t testing.TB, db *sql.DB, id int64, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow("SELECT array_to_json(synonyms)::text FROM meanings WHERE id = $1", id).Scan(&got); err != nil || got != want {
		t.Fatalf("synonyms: got %s, want %s, err=%v", got, want, err)
	}
}
