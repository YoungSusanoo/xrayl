package dispatcher

import (
	"context"
	"testing"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
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
	d := New()
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
