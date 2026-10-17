package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRunRejectsInvalidCommand(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"up", "extra"}} {
		if err := run(context.Background(), args, io.Discard); err == nil {
			t.Fatalf("expected invalid command %v to fail", args)
		}
	}
}

func TestDatabaseURLWithoutDotEnv(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "postgres://localhost/xrayl")
	got, err := databaseURL()
	if err != nil || got != "postgres://localhost/xrayl" {
		t.Fatalf("environment configuration: got %q, err %v", got, err)
	}
}

func TestDatabaseURLRequiresNonemptyValue(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("DATABASE_URL", "  ")
	if _, err := databaseURL(); err == nil {
		t.Fatal("empty database configuration was accepted")
	}
}

func TestDatabaseURLLoadsDotEnvAndPreservesEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("DATABASE_URL=postgres://localhost/from_file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_URL", "")
	if err := os.Unsetenv("DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
	got, err := databaseURL()
	if err != nil || got != "postgres://localhost/from_file" {
		t.Fatalf("file configuration: got %q, err %v", got, err)
	}
	t.Setenv("DATABASE_URL", "postgres://localhost/from_environment")
	got, err = databaseURL()
	if err != nil || got != "postgres://localhost/from_environment" {
		t.Fatalf("environment must take precedence: got %q, err %v", got, err)
	}
}
