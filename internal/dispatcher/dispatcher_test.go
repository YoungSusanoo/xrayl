package dispatcher

import (
	"context"
	"io"
	"net/http"
	"reflect"
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
		return commandUpdate(user, 10, text)
	}
	ctx := context.Background()
	d.Handle(ctx, b, message(1, "/addword"))
	d.Handle(ctx, b, message(1, "book"))
	d.Handle(ctx, b, message(2, "/addword"))
	d.Handle(ctx, b, message(2, "book"))
	d.Handle(ctx, b, message(1, "/addword"))
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
		return commandUpdate(user, chat, text)
	}
	ctx := context.Background()
	d.Handle(ctx, b, message(1, 10, "/words"))
	first := d.sessions[sessionKey{chatID: 10, userID: 1}]
	if _, ok := first.(*flows.Words); !ok {
		t.Fatal("words flow was not started")
	}
	d.Handle(ctx, b, message(1, 10, "1"))
	if d.sessions[sessionKey{chatID: 10, userID: 1}] != first {
		t.Fatal("selection ended browsing")
	}
	d.Handle(ctx, b, message(2, 10, "/words"))
	d.Handle(ctx, b, message(1, 20, "/words"))
	d.Handle(ctx, b, message(1, 10, "/words"))
	if d.sessions[sessionKey{chatID: 10, userID: 1}] == first {
		t.Fatal("words did not restart")
	}
	d.Handle(ctx, b, message(1, 10, "/addword"))
	if _, ok := d.sessions[sessionKey{chatID: 10, userID: 1}].(*flows.Addword); !ok {
		t.Fatal("addword did not replace browsing")
	}
	d.Handle(ctx, b, message(1, 10, "book"))
	d.Handle(ctx, b, message(1, 10, "/words"))
	d.Handle(ctx, b, message(1, 10, "1"))
	if len(store.users) != 0 {
		t.Fatal("selection reached old addword flow")
	}
	d.Handle(ctx, b, message(1, 10, "/cancel"))
	if len(d.sessions) != 2 {
		t.Fatal("cancel affected other user/chat")
	}
	d.Handle(ctx, b, nil)
	d.Handle(ctx, b, &models.Update{})
}

func commandUpdate(user, chat int64, text string) *models.Update {
	message := &models.Message{Chat: models.Chat{ID: chat}, From: &models.User{ID: user}, Text: text}
	if strings.HasPrefix(text, "/") {
		command := strings.Fields(text)[0]
		message.Entities = []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Offset: 0, Length: len(command)}}
	}
	return &models.Update{Message: message}
}

func TestHandleStartsEveryMainCommand(t *testing.T) {
	for _, tc := range []struct {
		command string
		want    flows.Flow
	}{
		{"/addword", &flows.Addword{}},
		{"/words", &flows.Words{}},
		{"/addsource", &flows.Addsource{}},
		{"/sources", nil},
	} {
		t.Run(tc.command, func(t *testing.T) {
			d := New(testDictionary{}, &testStore{})
			key := sessionKey{chatID: 10, userID: 42}
			previous := &testFlow{}
			d.sessions[key] = previous
			b, err := bot.New("1:test", bot.WithSkipGetMe(), bot.WithHTTPClient(time.Second, &http.Client{Transport: telegramTransport{}}))
			if err != nil {
				t.Fatal(err)
			}
			d.Handle(context.Background(), b, commandUpdate(42, 10, tc.command))
			if previous.calls != 0 || reflect.TypeOf(d.sessions[key]) != reflect.TypeOf(tc.want) {
				t.Fatalf("wrong command flow: got %T want %T previouscalls=%d", d.sessions[key], tc.want, previous.calls)
			}
		})
	}
}

func TestHandleCommandRecognition(t *testing.T) {
	entity := func(offset, length int) []models.MessageEntity {
		return []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Offset: offset, Length: length}}
	}
	for _, tc := range []struct {
		name, text string
		entities   []models.MessageEntity
		restart    bool
	}{
		{"plain command", "/words", entity(0, 6), true},
		{"arguments", "/words anything", entity(0, 6), true},
		{"multiline arguments", "/words\nanything", entity(0, 6), true},
		{"missing entity", "/words", nil, false},
		{"unknown command", "/unknown", entity(0, 8), false},
		{"flow command", "/cancel", entity(0, 7), false},
		{"case sensitive", "/Words", entity(0, 6), false},
		{"addressed command", "/words@my_bot", entity(0, 13), false},
		{"middle of text", "hi /words", entity(3, 6), false},
		{"unicode prefix", "📖 /words", entity(3, 6), false},
		{"caption", "", entity(0, 6), false},
		{"negative offset", "/words", entity(-1, 6), false},
		{"negative length", "/words", entity(0, -1), false},
		{"zero length", "/words", entity(0, 0), false},
		{"slash only", "/", entity(0, 1), false},
		{"out of bounds", "/words", entity(0, 99), false},
		{"overflow length", "/words", entity(0, int(^uint(0)>>1)), false},
		{"overflow offset", "/words", entity(int(^uint(0)>>1), 6), false},
		{"wrong entity", "/words", []models.MessageEntity{{Type: models.MessageEntityTypeBold, Length: 6}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := New(testDictionary{}, &testStore{})
			key := sessionKey{chatID: 10, userID: 42}
			previous := &testFlow{}
			d.sessions[key] = previous
			b, err := bot.New("1:test", bot.WithSkipGetMe(), bot.WithHTTPClient(time.Second, &http.Client{Transport: telegramTransport{}}))
			if err != nil {
				t.Fatal(err)
			}
			update := &models.Update{Message: &models.Message{Chat: models.Chat{ID: 10}, From: &models.User{ID: 42}, Text: tc.text, Entities: tc.entities}}
			d.Handle(context.Background(), b, update)
			if tc.restart {
				if _, ok := d.sessions[key].(*flows.Words); !ok || previous.calls != 0 {
					t.Fatalf("command did not replace flow: %T calls=%d", d.sessions[key], previous.calls)
				}
			} else if d.sessions[key] != previous || previous.calls != 1 {
				t.Fatalf("message not passed to existing flow: %T calls=%d", d.sessions[key], previous.calls)
			}
		})
	}
	d := New(nil, nil)
	for _, u := range []*models.Update{nil, {}, {Message: &models.Message{}}, commandUpdate(42, 10, "/unknown"), commandUpdate(42, 10, "text")} {
		d.Handle(context.Background(), nil, u)
	}
	if len(d.sessions) != 0 {
		t.Fatal("invalid/unrouted input created a session")
	}
}
