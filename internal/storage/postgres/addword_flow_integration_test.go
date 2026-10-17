//go:build integration

package postgres

import (
	"context"
	"fmt"
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

func TestAddwordHTTPToPostgres(t *testing.T) {
	db := migrateTestDatabase(t)
	var lookups atomic.Int32
	dictionaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/entries/en/book" {
			t.Errorf("unexpected dictionary request: %s %s", r.Method, r.URL)
		}
		call := lookups.Add(1)
		synonyms := `[" Volume ","volume","Tome"]`
		if call == 2 {
			synonyms = `["publication"]`
		}
		if call >= 3 {
			synonyms = `[]`
		}
		fmt.Fprintf(w, `{"word":"book","entries":[{"language":{"code":"en"},"partOfSpeech":"noun","synonyms":["group synonym"],"senses":[{"definition":"Printed work.","synonyms":%s},{"definition":"An account record."}]}],"source":{"url":"https://en.wiktionary.org/wiki/book","license":{"name":"CC BY-SA 4.0","url":"https://creativecommons.org/licenses/by-sa/4.0/"}}}`, synonyms)
	}))
	defer dictionaryServer.Close()
	var mu sync.Mutex
	var messages []string
	telegramServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("Telegram request: %v", err)
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
	d := dispatcher.New(freedictionaryapi.NewClient(dictionaryServer.URL+"/api/v1", dictionaryServer.Client()), NewStore(db))
	b, err := tg.New("1:test", tg.WithSkipGetMe(), tg.WithServerURL(telegramServer.URL), tg.WithNotAsyncHandlers(),
		tg.WithDefaultHandler(d.Handle), tg.WithMessageTextHandler("addword", tg.MatchTypeCommandStartOnly, d.HandleAddword))
	if err != nil {
		t.Fatal(err)
	}
	send := func(user int64, text string) {
		t.Helper()
		message := &models.Message{Chat: models.Chat{ID: 10}, From: &models.User{ID: user}, Text: text}
		if text == "/addword" {
			message.Entities = []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Offset: 0, Length: 8}}
		}
		b.ProcessUpdate(context.Background(), &models.Update{Message: message})
	}
	send(42, "/addword")
	send(42, "book")
	send(42, "99")
	requireEntryCounts(t, db, 0, 0, 0)
	send(42, "1")
	var meaningID int64
	if err := db.QueryRow("SELECT id FROM meanings WHERE user_id=42").Scan(&meaningID); err != nil {
		t.Fatal(err)
	}
	requireSynonyms(t, db, meaningID, `["Volume","Tome"]`)
	send(42, "1")
	requireEntryCounts(t, db, 1, 1, 1)
	for _, want := range []string{`["publication"]`, `[]`} {
		send(42, "/addword")
		send(42, "book")
		send(42, "1")
		requireSynonyms(t, db, meaningID, want)
		requireEntryCounts(t, db, 1, 1, 1)
	}
	send(43, "/addword")
	send(43, "book")
	send(43, "2")
	requireEntryCounts(t, db, 2, 2, 2)
	var definition string
	if err := db.QueryRow("SELECT definition FROM meanings WHERE user_id=43").Scan(&definition); err != nil || definition != "An account record." {
		t.Fatalf("wrong selection saved: %q, %v", definition, err)
	}
	send(44, "/addword")
	send(44, "book")
	send(44, "/cancel")
	send(44, "1")
	requireEntryCounts(t, db, 2, 2, 2)
	if lookups.Load() != 5 {
		t.Fatalf("selection triggered extra lookups: %d", lookups.Load())
	}
	mu.Lock()
	output := strings.Join(messages, "\n")
	mu.Unlock()
	for _, required := range []string{"1.", "2.", "Printed work.", "обновлены", "https://en.wiktionary.org/wiki/book", "https://freedictionaryapi.com", "CC BY-SA 4.0"} {
		if !strings.Contains(output, required) {
			t.Errorf("missing %q in Telegram output", required)
		}
	}
}
