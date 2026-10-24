//go:build integration

package integration

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
	"xrayl/internal/storage/postgres"
	"xrayl/internal/testutil"
)

func TestWordsHTTPToPostgres(t *testing.T) {
	db := migrateTestDatabase(t)
	ctx := context.Background()
	var lookups atomic.Int32
	dictionaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lookups.Add(1)
		if r.URL.Path != "/entries/en/book" {
			t.Errorf("unexpected lookup: %s", r.URL)
		}
		io.WriteString(w, `{"word":"book","entries":[{"language":{"code":"en"},"partOfSpeech":"noun","senses":[{"definition":"Printed work.","synonyms":["volume","tome"]},{"definition":"To reserve.","synonyms":["hold"]}]}]}`)
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
	store := postgres.NewStore(db)
	d := dispatcher.New(freedictionaryapi.NewClient(dictionaryServer.URL, dictionaryServer.Client()), store)
	b, err := tg.New("1:test", tg.WithSkipGetMe(), tg.WithServerURL(telegramServer.URL), tg.WithNotAsyncHandlers(),
		tg.WithDefaultHandler(d.Handle))
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
		b.ProcessUpdate(ctx, &models.Update{Message: message})
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(messages[start:], "\n")
	}
	for _, number := range []string{"1", "2"} {
		send(42, "/addword")
		send(42, "book")
		send(42, number)
	}
	for _, item := range []struct {
		user             int64
		word, definition string
	}{{42, "apple", "A fruit."}, {43, "hidden", "Private meaning."}} {
		if _, err := store.AddMeaning(ctx, item.user, item.word, item.definition, nil); err != nil {
			t.Fatal(err)
		}
	}
	list := send(42, "/words")
	if strings.Count(list, "book") != 1 || !strings.Contains(list, "1. apple\n2. book") || strings.Contains(list, "hidden") || strings.Contains(list, "Printed work.") {
		t.Fatalf("word list: %s", list)
	}
	card := send(42, "2")
	for _, want := range []string{"book", "Printed work.", "To reserve.", "volume, tome", "hold"} {
		if !strings.Contains(card, want) {
			t.Errorf("missing %q in %s", want, card)
		}
	}
	if got := send(42, "1"); !strings.Contains(got, "A fruit.") {
		t.Fatalf("browsing ended after first selection: %s", got)
	}
	if got := send(43, "/words"); strings.Contains(got, "book") || !strings.Contains(got, "hidden") {
		t.Fatalf("foreign list: %s", got)
	}
	if got := send(44, "/words"); !strings.Contains(got, "/addword") {
		t.Fatalf("empty list: %s", got)
	}
	send(42, "/cancel")
	if got := send(42, "1"); got != "" {
		t.Fatalf("canceled session answered: %s", got)
	}
	send(42, "/words")
	send(42, "/addword")
	if got := send(42, "book"); !strings.Contains(got, "Пришли номер значения") {
		t.Fatalf("addword did not replace browsing: %s", got)
	}
	send(42, "/words")
	if got := send(42, "2"); !strings.Contains(got, "To reserve.") {
		t.Fatalf("words did not replace adding: %s", got)
	}
	if lookups.Load() != 3 {
		t.Fatalf("browsing queried external dictionary: %d lookups", lookups.Load())
	}
	testutil.RequireEntryCounts(t, db, 2, 3, 4)
}
