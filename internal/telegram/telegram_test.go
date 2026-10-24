package telegram

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"xrayl/internal/flows"
)

type receiverFunc func(flows.Message) error

func (f receiverFunc) Enqueue(message flows.Message) error { return f(message) }

func TestCommandRecognition(t *testing.T) {
	entity := func(offset, length int) []models.MessageEntity {
		return []models.MessageEntity{{Type: models.MessageEntityTypeBotCommand, Offset: offset, Length: length}}
	}
	for _, tc := range []struct {
		name, text string
		entities   []models.MessageEntity
		recognized bool
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
			var got flows.Message
			h := Handler(receiverFunc(func(m flows.Message) error { got = m; return nil }))
			h(context.Background(), nil, &models.Update{Message: &models.Message{Chat: models.Chat{ID: 10}, From: &models.User{ID: 42}, Text: tc.text, Entities: tc.entities}})
			known := got.Command == "words"
			if known != tc.recognized || got.UserID != 42 || got.ChatID != 10 || got.Text != tc.text {
				t.Fatalf("decoded message: %+v", got)
			}
		})
	}
}

func TestHandlerIgnoresInvalidUpdates(t *testing.T) {
	calls := 0
	h := Handler(receiverFunc(func(flows.Message) error { calls++; return nil }))
	for _, u := range []*models.Update{nil, {}, {Message: &models.Message{}}} {
		h(context.Background(), nil, u)
	}
	if calls != 0 {
		t.Fatalf("invalid updates forwarded: %d", calls)
	}
}

func TestOptionsForwardUpdatesSynchronously(t *testing.T) {
	calls := 0
	b, err := tg.New("1:test", append(Options(receiverFunc(func(flows.Message) error { calls++; return nil })), tg.WithSkipGetMe())...)
	if err != nil {
		t.Fatal(err)
	}
	b.ProcessUpdate(context.Background(), &models.Update{Message: &models.Message{From: &models.User{ID: 1}, Text: "hello"}})
	if calls != 1 {
		t.Fatalf("ingress is asynchronous: calls=%d", calls)
	}
}

func TestSenderSplitsUnicodeAndStopsOnPartialFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "partial failure"}[fail], func(t *testing.T) {
			var sent []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
				}
				if r.MultipartForm != nil {
					defer r.MultipartForm.RemoveAll()
				}
				sent = append(sent, r.FormValue("text"))
				if fail && len(sent) == 2 {
					io.WriteString(w, `{"ok":false,"error_code":500,"description":"failure"}`)
					return
				}
				io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
			}))
			defer server.Close()
			b, err := tg.New("1:test", tg.WithSkipGetMe(), tg.WithServerURL(server.URL))
			if err != nil {
				t.Fatal(err)
			}
			text := strings.Repeat("📖Русский English\n", 1000)
			err = (Sender{Bot: b}).SendText(context.Background(), 10, text)
			if (err != nil) != fail {
				t.Fatalf("send error: %v", err)
			}
			if fail && len(sent) != 2 {
				t.Fatalf("continued after partial failure: %d", len(sent))
			}
			if !fail && strings.Join(sent, "") != text {
				t.Fatal("text truncated or changed")
			}
			for _, chunk := range sent {
				if len(utf16.Encode([]rune(chunk))) > 3000 {
					t.Fatal("oversized chunk")
				}
			}
		})
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSenderBoundsRequestAndRespectsCancellation(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			t.Error("missing send deadline")
		}
		return nil, errors.New("unavailable")
	})}
	b, err := tg.New("1:test", tg.WithSkipGetMe(), tg.WithHTTPClient(time.Second, client))
	if err != nil {
		t.Fatal(err)
	}
	if err := (Sender{Bot: b}).SendText(context.Background(), 10, "hello"); err == nil {
		t.Fatal("error swallowed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (Sender{Bot: b}).SendText(ctx, 10, "hello"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if calls != 1 {
		t.Fatalf("canceled send reached network: %d", calls)
	}
}
