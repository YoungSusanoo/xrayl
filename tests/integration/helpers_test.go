//go:build integration

package integration

import (
	"context"
	"database/sql"
	tg "github.com/go-telegram/bot"
	"testing"
	"time"
	"xrayl/internal/dispatcher"
	"xrayl/internal/flows"
	"xrayl/internal/telegram"

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

type notifyingSender struct {
	sender *telegram.Sender
	sent   chan struct{}
}

func (s notifyingSender) SendText(ctx context.Context, chatID int64, text string) error {
	err := s.sender.SendText(ctx, chatID, text)
	s.sent <- struct{}{}
	return err
}

func integrationBot(t *testing.T, dictionary flows.Dictionary, store flows.Store, url string) (*tg.Bot, <-chan struct{}) {
	t.Helper()
	sender := notifyingSender{sender: &telegram.Sender{}, sent: make(chan struct{}, 128)}
	d := dispatcher.New(context.Background(), dictionary, store, sender)
	t.Cleanup(d.Close)
	options := append(telegram.Options(d), tg.WithSkipGetMe(), tg.WithServerURL(url))
	b, err := tg.New("1:test", options...)
	if err != nil {
		t.Fatal(err)
	}
	sender.sender.Bot = b
	return b, sender.sent
}

func waitReplies(t *testing.T, sent <-chan struct{}, count int) {
	t.Helper()
	for range count {
		select {
		case <-sent:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for Telegram reply")
		}
	}
}
