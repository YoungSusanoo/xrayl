package dispatcher

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"xrayl/internal/dictionary"
	"xrayl/internal/flows"
	"xrayl/internal/storage"
)

type testFlow struct {
	calls int
	done  bool
}

func (f *testFlow) Handle(context.Context, *bot.Bot, *models.Update) bool {
	f.calls++
	return f.done
}

func TestHandleRoutesAndClearsCompletedFlow(t *testing.T) {
	d := New(nil, nil)
	firstKey := sessionKey{chatID: 10, userID: 1}
	secondKey := sessionKey{chatID: 10, userID: 2}
	first := &testFlow{}
	second := &testFlow{}
	d.sessions[firstKey] = first
	d.sessions[secondKey] = second

	firstUpdate := &models.Update{Message: &models.Message{
		Chat: models.Chat{ID: 10}, From: &models.User{ID: 1},
	}}
	d.Handle(context.Background(), nil, firstUpdate)
	if first.calls != 1 || second.calls != 0 {
		t.Fatalf("wrong flow handled update: first=%d second=%d", first.calls, second.calls)
	}
	if d.sessions[firstKey] != first {
		t.Fatal("unfinished flow was removed")
	}

	first.done = true
	d.Handle(context.Background(), nil, firstUpdate)
	if _, ok := d.sessions[firstKey]; ok {
		t.Fatal("completed flow was not removed")
	}
	if d.sessions[secondKey] != second {
		t.Fatal("another user's flow was changed")
	}

	d.Handle(context.Background(), nil, nil)
	d.Handle(context.Background(), nil, &models.Update{})
}

type testDictionary struct{}

func (testDictionary) Lookup(context.Context, string) ([]dictionary.Entry, error) {
	return []dictionary.Entry{{Word: "book", Meanings: []dictionary.Meaning{{PartOfSpeech: "noun", Definitions: []dictionary.Definition{{Text: "A written work."}}}}}}, nil
}

type testStore struct{ users []int64 }

func (s *testStore) AddSource(context.Context, int64, string) (storage.AddSourceResult, error) {
	return storage.AddSourceResult{}, nil
}
func (s *testStore) ListSources(context.Context, int64, int, int) ([]storage.Source, error) {
	return nil, nil
}
func (s *testStore) AddContext(context.Context, int64, int64, int64, string) (int64, error) {
	return 0, nil
}
func (s *testStore) ListWordContexts(context.Context, int64, int64) ([]storage.WordContext, error) {
	return nil, nil
}

func (s *testStore) ListWords(context.Context, int64, int, int) ([]storage.Word, error) {
	return []storage.Word{{ID: 1, Text: "book"}}, nil
}

func (s *testStore) ListMeanings(context.Context, int64, int64) ([]storage.Meaning, error) {
	return []storage.Meaning{{ID: 1, Definition: "A written work."}}, nil
}

func (s *testStore) AddMeaning(_ context.Context, user int64, _, _ string, _ []string) (storage.AddMeaningResult, error) {
	s.users = append(s.users, user)
	return storage.AddMeaningResult{MeaningCreated: true}, nil
}

type telegramTransport struct{}

func (telegramTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"message_id":1,"chat":{"id":10,"type":"private"}}}`))}, nil
}

func TestAddwordRestartCancelAndSessionIsolation(t *testing.T) {
	store := &testStore{}
	d := New(testDictionary{}, store)
	b, err := bot.New("1:test", bot.WithSkipGetMe(), bot.WithHTTPClient(time.Second, &http.Client{Transport: telegramTransport{}}))
	if err != nil {
		t.Fatal(err)
	}
	message := func(user int64, text string) *models.Update {
		return &models.Update{Message: &models.Message{Chat: models.Chat{ID: 10}, From: &models.User{ID: user}, Text: text}}
	}
	ctx := context.Background()
	d.HandleAddword(ctx, b, message(1, "/addword"))
	d.Handle(ctx, b, message(1, "book"))
	d.HandleAddword(ctx, b, message(2, "/addword"))
	d.Handle(ctx, b, message(2, "book"))
	d.HandleAddword(ctx, b, message(1, "/addword"))
	d.Handle(ctx, b, message(1, "/cancel"))
	if len(d.sessions) != 1 || d.sessions[sessionKey{chatID: 10, userID: 2}] == nil {
		t.Fatal("restart/cancel changed another user's session")
	}
	d.Handle(ctx, b, message(1, "1"))
	d.Handle(ctx, b, message(2, "1"))
	d.Handle(ctx, b, message(2, "1"))
	if len(d.sessions) != 0 || len(store.users) != 1 || store.users[0] != 2 {
		t.Fatalf("wrong saves: %v; sessions=%d", store.users, len(d.sessions))
	}
}

func TestWordsRestartAndSwitchFlows(t *testing.T) {
	store := &testStore{}
	d := New(testDictionary{}, store)
	b, err := bot.New("1:test", bot.WithSkipGetMe(), bot.WithHTTPClient(time.Second, &http.Client{Transport: telegramTransport{}}))
	if err != nil {
		t.Fatal(err)
	}
	message := func(user, chat int64, text string) *models.Update {
		return &models.Update{Message: &models.Message{Chat: models.Chat{ID: chat}, From: &models.User{ID: user}, Text: text}}
	}
	ctx := context.Background()
	d.HandleWords(ctx, b, message(1, 10, "/words"))
	first := d.sessions[sessionKey{chatID: 10, userID: 1}]
	if _, ok := first.(*flows.Words); !ok {
		t.Fatal("words flow was not started")
	}
	d.Handle(ctx, b, message(1, 10, "1"))
	if d.sessions[sessionKey{chatID: 10, userID: 1}] != first {
		t.Fatal("selection ended browsing")
	}
	d.HandleWords(ctx, b, message(2, 10, "/words"))
	d.HandleWords(ctx, b, message(1, 20, "/words"))
	d.HandleWords(ctx, b, message(1, 10, "/words"))
	if d.sessions[sessionKey{chatID: 10, userID: 1}] == first {
		t.Fatal("words did not restart")
	}
	d.HandleAddword(ctx, b, message(1, 10, "/addword"))
	if _, ok := d.sessions[sessionKey{chatID: 10, userID: 1}].(*flows.Addword); !ok {
		t.Fatal("addword did not replace browsing")
	}
	d.Handle(ctx, b, message(1, 10, "book"))
	d.HandleWords(ctx, b, message(1, 10, "/words"))
	d.Handle(ctx, b, message(1, 10, "1"))
	if len(store.users) != 0 {
		t.Fatal("selection reached old addword flow")
	}
	d.Handle(ctx, b, message(1, 10, "/cancel"))
	if len(d.sessions) != 2 {
		t.Fatal("cancel affected other user/chat")
	}
	d.HandleWords(ctx, b, nil)
	d.HandleWords(ctx, b, &models.Update{})
}
