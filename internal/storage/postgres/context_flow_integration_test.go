//go:build integration

package postgres

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"xrayl/internal/dictionary/freedictionaryapi"
	"xrayl/internal/dispatcher"
)

func TestContextHTTPToPostgres(t *testing.T) {
	db := migrateTestDatabase(t)
	var lookups atomic.Int32
	dictionaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lookups.Add(1)
		io.WriteString(w, `{"word":"book","entries":[{"language":{"code":"en"},"partOfSpeech":"noun","senses":[{"definition":"Printed work.","synonyms":["volume"]},{"definition":"To reserve."}]}]}`)
	}))
	defer dictionaryServer.Close()
	var mu sync.Mutex
	var messages []string
	telegramServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		mu.Lock()
		messages = append(messages, r.FormValue("text"))
		mu.Unlock()
		io.WriteString(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":10,"type":"private"}}}`)
	}))
	defer telegramServer.Close()
	d := dispatcher.New(freedictionaryapi.NewClient(dictionaryServer.URL, dictionaryServer.Client()), NewStore(db))
	b, err := tg.New("1:test", tg.WithSkipGetMe(), tg.WithServerURL(telegramServer.URL), tg.WithNotAsyncHandlers(), tg.WithDefaultHandler(d.Handle),
		tg.WithMessageTextHandler("addword", tg.MatchTypeCommandStartOnly, d.HandleAddword),
		tg.WithMessageTextHandler("words", tg.MatchTypeCommandStartOnly, d.HandleWords),
		tg.WithMessageTextHandler("sources", tg.MatchTypeCommandStartOnly, d.HandleSources),
		tg.WithMessageTextHandler("addsource", tg.MatchTypeCommandStartOnly, d.HandleAddsource))
	if err != nil {
		t.Fatal(err)
	}
	send := func(user int64, text string) string {
		t.Helper()
		mu.Lock()
		start := len(messages)
		mu.Unlock()
		message := &models.Message{Chat: models.Chat{ID: 10}, From: &models.User{ID: user}, Text: text}
		if strings.HasPrefix(text, "/") {
			message.Entities = []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Length: len(text)}}
		}
		b.ProcessUpdate(context.Background(), &models.Update{Message: message})
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(messages[start:], "\n")
	}
	send(42, "/addsource")
	if got := send(42, " Dune "); !strings.Contains(got, "Dune") {
		t.Fatalf("source creation: %s", got)
	}
	send(42, "/addsource")
	if got := send(42, "DUNE"); !strings.Contains(got, "уже есть") {
		t.Fatalf("source duplicate: %s", got)
	}
	if got := send(42, "/sources"); !strings.Contains(got, "1. Dune") {
		t.Fatalf("sources list: %s", got)
	}
	send(42, "/addword")
	send(42, "book")
	if got := send(42, "1"); !strings.Contains(got, "номер источника") {
		t.Fatalf("missing source selection: %s", got)
	}
	requireEntryCounts(t, db, 1, 1, 1)
	var count int
	if err := db.QueryRow("SELECT count(*) FROM contexts").Scan(&count); err != nil || count != 0 {
		t.Fatalf("premature context: %d %v", count, err)
	}
	send(42, "1")
	send(42, "Read the book.")
	if got := send(42, "page 3"); !strings.Contains(got, "Контекст сохранён") {
		t.Fatalf("context confirmation: %s", got)
	}
	send(42, "/addword")
	send(42, "book")
	send(42, "1")
	send(42, "/skip")
	send(42, "/addword")
	send(42, "book")
	send(42, "2")
	send(42, "1")
	send(42, "Book a room.")
	send(42, "/skip")
	send(42, "/addword")
	send(42, "book")
	send(42, "1")
	send(42, "1")
	send(42, "Not saved.")
	if got := send(42, "/cancel"); !strings.Contains(got, "Значение осталось в словаре") {
		t.Fatalf("cancel: %s", got)
	}
	if got := send(42, "page 4"); got != "" {
		t.Fatalf("canceled session responded: %s", got)
	}
	requireEntryCounts(t, db, 1, 1, 2)
	if err := db.QueryRow("SELECT count(*) FROM contexts").Scan(&count); err != nil || count != 2 {
		t.Fatalf("contexts after cancel/skip: %d %v", count, err)
	}
	send(42, "/words")
	card := send(42, "1")
	for _, text := range []string{"Printed work.", "Read the book.", "page 3", "To reserve.", "Book a room.", "Источник: Dune"} {
		if !strings.Contains(card, text) {
			t.Errorf("missing %q in %s", text, card)
		}
	}
	if strings.Contains(card, "Not saved.") || strings.Index(card, "Read the book.") > strings.Index(card, "To reserve.") {
		t.Fatalf("wrong context grouping: %s", card)
	}
	if got := send(43, "/sources"); !strings.Contains(got, "Источников пока нет") {
		t.Fatalf("foreign sources: %s", got)
	}
	send(43, "/addword")
	send(43, "book")
	if got := send(43, "1"); !strings.Contains(got, "/addsource") {
		t.Fatalf("no-source finish: %s", got)
	}
	if got := send(43, "/words"); !strings.Contains(got, "book") {
		t.Fatalf("missing saved word without context: %s", got)
	}
	foreignCard := send(43, "1")
	if strings.Contains(foreignCard, "Dune") || strings.Contains(foreignCard, "Read the book.") {
		t.Fatalf("foreign contexts leaked: %s", foreignCard)
	}
	if lookups.Load() != 5 {
		t.Fatalf("unexpected dictionary requests: %d", lookups.Load())
	}
}
