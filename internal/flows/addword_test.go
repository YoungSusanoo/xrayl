package flows_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"xrayl/internal/dictionary"
	"xrayl/internal/flows"
	"xrayl/internal/storage"
)

type lookupFunc func(context.Context, string) ([]dictionary.Entry, error)

func (f lookupFunc) Lookup(ctx context.Context, word string) ([]dictionary.Entry, error) {
	return f(ctx, word)
}

type saveFunc func(context.Context, int64, string, string, []string) (storage.AddMeaningResult, error)

func (f saveFunc) AddMeaning(ctx context.Context, userID int64, word, definition string, synonyms []string) (storage.AddMeaningResult, error) {
	return f(ctx, userID, word, definition, synonyms)
}

type telegramLog struct {
	mu       sync.Mutex
	messages []string
	failAt   int
}

func telegram(t *testing.T) (*tg.Bot, *telegramLog) {
	t.Helper()
	record := &telegramLog{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse Telegram request: %v", err)
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		record.mu.Lock()
		record.messages = append(record.messages, r.FormValue("text"))
		failed := record.failAt == len(record.messages)
		record.mu.Unlock()
		if failed {
			io.WriteString(w, `{"ok":false,"error_code":500,"description":"test failure"}`)
			return
		}
		io.WriteString(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":10,"type":"private"}}}`)
	}))
	t.Cleanup(server.Close)
	b, err := tg.New("1:test", tg.WithSkipGetMe(), tg.WithServerURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	return b, record
}

func (r *telegramLog) texts() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.messages...)
}

func (r *telegramLog) failNext() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failAt = len(r.messages) + 1
}

func update(text string) *models.Update {
	return &models.Update{Message: &models.Message{Chat: models.Chat{ID: 10}, From: &models.User{ID: 42}, Text: text}}
}

func entries() []dictionary.Entry {
	return []dictionary.Entry{
		{Word: "book", Meanings: []dictionary.Meaning{
			{PartOfSpeech: "noun", Synonyms: []string{"group synonym"}, Definitions: []dictionary.Definition{
				{Text: "A written work.", Synonyms: []string{"volume"}}, {Text: "An account record."},
			}},
			{PartOfSpeech: "verb", Definitions: []dictionary.Definition{{Text: "To reserve.", Synonyms: []string{"hold"}}}},
		}},
		{Word: "bake", Meanings: []dictionary.Meaning{
			{PartOfSpeech: "verb", Synonyms: []string{"group bake"}, Definitions: []dictionary.Definition{{Text: "To cook in an oven.", Synonyms: []string{"cook"}}}},
		}, SourceURLs: []string{"https://en.wiktionary.org/wiki/bake"}, License: dictionary.License{Name: "CC BY-SA 3.0", URL: "https://creativecommons.org/licenses/by-sa/3.0/"}},
	}
}

func TestAddwordSelectsAndSavesChosenDefinition(t *testing.T) {
	b, sent := telegram(t)
	lookups, saves := 0, 0
	f := flows.NewAddword(lookupFunc(func(ctx context.Context, word string) ([]dictionary.Entry, error) {
		lookups++
		if word != "book" {
			t.Errorf("lookup word: %q", word)
		}
		return entries(), nil
	}), saveFunc(func(ctx context.Context, userID int64, word, definition string, synonyms []string) (storage.AddMeaningResult, error) {
		saves++
		if userID != 42 || word != "bake" || definition != "To cook in an oven." || !reflect.DeepEqual(synonyms, []string{"cook"}) {
			t.Errorf("wrong saved selection: %d %q %q %v", userID, word, definition, synonyms)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			t.Error("save must have a five-second deadline")
		}
		return storage.AddMeaningResult{WordID: 1, MeaningID: 2, MeaningCreated: true}, nil
	}))
	if f.Handle(context.Background(), b, update("/addword")) || f.Handle(context.Background(), b, update(" book ")) {
		t.Fatal("flow completed before selection")
	}
	list := strings.Join(sent.texts(), "\n")
	for _, text := range []string{"1.", "2.", "3.", "4.", "noun", "verb", "To reserve."} {
		if !strings.Contains(list, text) {
			t.Errorf("missing %q in choices: %s", text, list)
		}
	}
	if saves != 0 || !f.Handle(context.Background(), b, update("4")) || saves != 1 || lookups != 1 {
		t.Fatalf("wrong lifecycle: lookups=%d saves=%d", lookups, saves)
	}
	confirmation := strings.Join(sent.texts()[2:], "\n")
	for _, text := range []string{"bake", "To cook in an oven.", "cook", "https://en.wiktionary.org/wiki/bake"} {
		if !strings.Contains(confirmation, text) {
			t.Errorf("missing %q in saved card: %s", text, confirmation)
		}
	}
}

func TestAddwordInvalidInputDoesNotAdvanceOrSave(t *testing.T) {
	b, _ := telegram(t)
	lookups, saves := 0, 0
	f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) {
		lookups++
		return entries(), nil
	}), saveFunc(func(_ context.Context, _ int64, _, _ string, synonyms []string) (storage.AddMeaningResult, error) {
		saves++
		if len(synonyms) != 0 {
			t.Errorf("group synonyms leaked into definition: %v", synonyms)
		}
		return storage.AddMeaningResult{MeaningCreated: true}, nil
	}))
	f.Handle(context.Background(), b, update("/addword"))
	for _, text := range []string{"", "  ", "/unknown"} {
		if f.Handle(context.Background(), b, update(text)) {
			t.Fatal("invalid word completed flow")
		}
	}
	if lookups != 0 {
		t.Fatal("invalid input reached dictionary")
	}
	f.Handle(context.Background(), b, update("book"))
	for _, text := range []string{"", "0", "-1", "5", "1.5", "text", "/unknown"} {
		if f.Handle(context.Background(), b, update(text)) {
			t.Fatal("invalid selection completed flow")
		}
	}
	if lookups != 1 || saves != 0 || !f.Handle(context.Background(), b, update("2")) || saves != 1 {
		t.Fatalf("invalid input changed state: lookups=%d saves=%d", lookups, saves)
	}
}

func TestAddwordDictionaryErrorsAllowNewWord(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"not found", dictionary.ErrNotFound}, {"rate limit", dictionary.ErrRateLimited},
		{"unavailable", errors.New("network unavailable")}, {"invalid response", dictionary.ErrInvalidResponse},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := telegram(t)
			lookups := 0
			f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) {
				lookups++
				if lookups == 1 {
					return nil, tc.err
				}
				return entries(), nil
			}), nil)
			f.Handle(context.Background(), b, update("/addword"))
			if f.Handle(context.Background(), b, update("missing")) || f.Handle(context.Background(), b, update("book")) || lookups != 2 {
				t.Fatalf("failed lookup advanced state: %d calls", lookups)
			}
		})
	}
}

func TestAddwordSaveFailureRetriesWithoutLookup(t *testing.T) {
	b, sent := telegram(t)
	lookups, saves := 0, 0
	f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) { lookups++; return entries(), nil }),
		saveFunc(func(context.Context, int64, string, string, []string) (storage.AddMeaningResult, error) {
			saves++
			if saves == 1 {
				return storage.AddMeaningResult{}, errors.New("database unavailable")
			}
			return storage.AddMeaningResult{MeaningID: 1, MeaningCreated: false}, nil
		}))
	f.Handle(context.Background(), b, update("/addword"))
	f.Handle(context.Background(), b, update("book"))
	if f.Handle(context.Background(), b, update("1")) || !f.Handle(context.Background(), b, update("1")) || saves != 2 || lookups != 1 {
		t.Fatalf("retry state: lookups=%d saves=%d", lookups, saves)
	}
	if !strings.Contains(strings.Join(sent.texts(), "\n"), "обновлены") {
		t.Fatal("duplicate confirmation does not explain synonym update")
	}
}

func TestAddwordCancelDoesNotSave(t *testing.T) {
	for _, selectState := range []bool{false, true} {
		t.Run(fmt.Sprint(selectState), func(t *testing.T) {
			b, _ := telegram(t)
			f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) { return entries(), nil }),
				saveFunc(func(context.Context, int64, string, string, []string) (storage.AddMeaningResult, error) {
					t.Error("cancel saved data")
					return storage.AddMeaningResult{}, nil
				}))
			f.Handle(context.Background(), b, update("/addword"))
			if selectState {
				f.Handle(context.Background(), b, update("book"))
			}
			if !f.Handle(context.Background(), b, update("/cancel")) {
				t.Fatal("cancel did not complete flow")
			}
		})
	}
}

func TestAddwordIgnoresInvalidUpdates(t *testing.T) {
	f := flows.NewAddword(nil, nil)
	for _, input := range []*models.Update{nil, {}, {Message: &models.Message{}}} {
		if f.Handle(context.Background(), nil, input) {
			t.Fatal("invalid update completed flow")
		}
	}
}

func TestAddwordSplitsLongOutputWithoutTruncatingSavedDefinition(t *testing.T) {
	b, sent := telegram(t)
	definition := strings.Repeat("long definition 📖 ", 600)
	f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) {
		return []dictionary.Entry{{Word: "book", Meanings: []dictionary.Meaning{{PartOfSpeech: "noun", Definitions: []dictionary.Definition{{Text: definition}}}}}}, nil
	}), saveFunc(func(_ context.Context, _ int64, _, saved string, _ []string) (storage.AddMeaningResult, error) {
		if saved != definition {
			t.Error("saved definition was truncated")
		}
		return storage.AddMeaningResult{MeaningCreated: true}, nil
	}))
	f.Handle(context.Background(), b, update("/addword"))
	f.Handle(context.Background(), b, update("book"))
	if !strings.Contains(strings.Join(sent.texts(), ""), definition) {
		t.Fatal("displayed definition was truncated")
	}
	if !f.Handle(context.Background(), b, update("1")) {
		t.Fatal("long definition not saved")
	}
	for _, text := range sent.texts() {
		if len(utf16.Encode([]rune(text))) > 3000 {
			t.Fatalf("message too long: %d runes", len([]rune(text)))
		}
	}
}

func TestAddwordTelegramFailuresPreserveLifecycle(t *testing.T) {
	t.Run("initial prompt", func(t *testing.T) {
		b, sent := telegram(t)
		sent.failNext()
		lookups := 0
		f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) { lookups++; return entries(), nil }), nil)
		f.Handle(context.Background(), b, update("/addword"))
		f.Handle(context.Background(), b, update("/addword"))
		f.Handle(context.Background(), b, update("book"))
		if lookups != 1 {
			t.Fatalf("prompt failure advanced state: %d lookups", lookups)
		}
	})
	t.Run("choices", func(t *testing.T) {
		b, sent := telegram(t)
		lookups := 0
		f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) { lookups++; return entries(), nil }), nil)
		f.Handle(context.Background(), b, update("/addword"))
		sent.failNext()
		f.Handle(context.Background(), b, update("book"))
		f.Handle(context.Background(), b, update("book"))
		if lookups != 2 {
			t.Fatalf("choices failure advanced state: %d lookups", lookups)
		}
	})
	t.Run("confirmation", func(t *testing.T) {
		b, sent := telegram(t)
		saves := 0
		f := flows.NewAddword(lookupFunc(func(context.Context, string) ([]dictionary.Entry, error) { return entries(), nil }),
			saveFunc(func(context.Context, int64, string, string, []string) (storage.AddMeaningResult, error) {
				saves++
				return storage.AddMeaningResult{MeaningCreated: true}, nil
			}))
		f.Handle(context.Background(), b, update("/addword"))
		f.Handle(context.Background(), b, update("book"))
		sent.failNext()
		if !f.Handle(context.Background(), b, update("1")) || saves != 1 {
			t.Fatal("confirmation failure did not finish saved flow")
		}
	})
}
