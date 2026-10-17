package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"

	"xrayl/internal/storage/postgres"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ctx, timeoutCancel := context.WithTimeout(ctx, time.Minute)
	defer timeoutCancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func databaseURL() (string, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("load .env: %w", err)
	}
	url := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if url == "" {
		return "", errors.New("DATABASE_URL is not set")
	}
	return url, nil
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 1 || (args[0] != "up" && args[0] != "down" && args[0] != "status") {
		return errors.New("usage: migrate <up|down|status>")
	}
	url, err := databaseURL()
	if err != nil {
		return err
	}
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		// Parse errors can include the connection string and its password.
		return errors.New("invalid DATABASE_URL")
	}
	db := stdlib.OpenDB(*cfg)
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	migrator, err := postgres.NewMigrator(db)
	if err != nil {
		return fmt.Errorf("initialize migrations: %w", err)
	}
	switch args[0] {
	case "up":
		results, err := migrator.Up(ctx)
		if err != nil {
			return fmt.Errorf("apply migrations: %w", err)
		}
		for _, result := range results {
			if _, err := fmt.Fprintln(out, result); err != nil {
				return err
			}
		}
		if len(results) == 0 {
			_, err = fmt.Fprintln(out, "No pending migrations.")
			return err
		}
	case "down":
		result, err := migrator.Down(ctx)
		if err != nil {
			return fmt.Errorf("roll back migration: %w", err)
		}
		_, err = fmt.Fprintln(out, result)
		return err
	case "status":
		statuses, err := migrator.Status(ctx)
		if err != nil {
			return fmt.Errorf("migration status: %w", err)
		}
		for _, status := range statuses {
			if _, err := fmt.Fprintf(out, "%d\t%s\t%s\n", status.Source.Version, status.State, status.Source.Path); err != nil {
				return err
			}
		}
	}
	return nil
}
