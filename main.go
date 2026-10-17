package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tg "github.com/go-telegram/bot"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"

	"xrayl/internal/dictionary/freedictionaryapi"
	"xrayl/internal/dispatcher"
	"xrayl/internal/storage/postgres"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("load .env: %w", err)
	}

	token := strings.TrimSpace(os.Getenv("BOT_TOKEN"))
	if token == "" {
		return errors.New("BOT_TOKEN is not set")
	}
	url := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if url == "" {
		return errors.New("DATABASE_URL is not set")
	}
	db, err := openDatabase(ctx, url)
	if err != nil {
		return err
	}
	defer db.Close()

	dictionary := freedictionaryapi.NewClient(freedictionaryapi.BaseURL, &http.Client{})
	d := dispatcher.New(dictionary, postgres.NewStore(db))
	bot, err := tg.New(token,
		tg.WithDefaultHandler(d.Handle),
		tg.WithMessageTextHandler("addword", tg.MatchTypeCommandStartOnly, d.HandleAddword),
	)
	if err != nil {
		return fmt.Errorf("create Telegram bot: %w", err)
	}

	bot.Start(ctx)
	return nil
}

func openDatabase(ctx context.Context, url string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL")
	}
	db := stdlib.OpenDB(*cfg)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return db, nil
}
