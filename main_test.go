package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunRequiresConfiguration(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("BOT_TOKEN", "")
	t.Setenv("DATABASE_URL", "")
	if err := run(context.Background()); err == nil || !strings.Contains(err.Error(), "BOT_TOKEN") {
		t.Fatalf("missing token: %v", err)
	}
	t.Setenv("BOT_TOKEN", "1:test")
	if err := run(context.Background()); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("missing database URL: %v", err)
	}
}

func TestInvalidDatabaseURLDoesNotExposeCredentials(t *testing.T) {
	db, err := openDatabase(context.Background(), "postgres://user:top-secret@host:invalid/db")
	if err == nil || db != nil || strings.Contains(err.Error(), "top-secret") {
		t.Fatalf("invalid database configuration: db=%v err=%v", db, err)
	}
}
