//go:build integration

package integration

import (
	"context"
	"database/sql"
	"testing"

	"xrayl/internal/storage/postgres"
	"xrayl/internal/testutil"
)

func migrateTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db := testutil.Database(t)
	migrator, err := postgres.NewMigrator(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}
