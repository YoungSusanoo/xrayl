package dispatcher

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"xrayl/internal/flows"
)

type flowFunc func(context.Context, flows.Message) flows.Result

func (f flowFunc) Handle(ctx context.Context, message flows.Message) flows.Result {
	return f(ctx, message)
}

type senderFunc func(context.Context, int64, string) error

func (f senderFunc) SendText(ctx context.Context, chat int64, text string) error {
	return f(ctx, chat, text)
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for worker")
		var zero T
		return zero
	}
}

func message(user int64, text string) flows.Message {
	return flows.Message{ChatID: 10, UserID: user, Text: text, Command: strings.TrimPrefix(text, "/")}
}

func testDispatcher(t *testing.T, factory func() flows.Flow, sender Sender) *Dispatcher {
	t.Helper()
	d := New(context.Background(), nil, nil, sender)
	d.factories = map[string]func() flows.Flow{"start": factory}
	t.Cleanup(d.Close)
	return d
}

func enqueue(t *testing.T, d *Dispatcher, m flows.Message) {
	t.Helper()
	if err := d.Enqueue(m); err != nil {
		t.Fatal(err)
	}
}

func TestSessionsPreserveFIFOWithoutBlockingEachOther(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	replies := make(chan flows.Message, 10)
	d := testDispatcher(t, func() flows.Flow {
		return flowFunc(func(ctx context.Context, m flows.Message) flows.Result {
			if m.UserID == 1 && m.Text == "/start" {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			replies <- m
			return flows.Result{}
		})
	}, senderFunc(func(context.Context, int64, string) error { return nil }))
	enqueue(t, d, message(1, "/start"))
	receive(t, entered)
	enqueue(t, d, message(1, "book"))
	enqueue(t, d, message(1, "1"))
	enqueue(t, d, message(2, "/start"))
	if got := receive(t, replies); got.UserID != 2 {
		t.Fatalf("other session blocked: %+v", got)
	}
	close(release)
	for _, want := range []string{"/start", "book", "1"} {
		if got := receive(t, replies); got.Text != want || got.UserID != 1 {
			t.Fatalf("FIFO: %+v want %s", got, want)
		}
	}
}

func TestRestartAndCompletionReleasePreviousFlow(t *testing.T) {
	replies := make(chan string, 10)
	created := 0
	d := testDispatcher(t, func() flows.Flow {
		created++
		id := created
		return flowFunc(func(_ context.Context, m flows.Message) flows.Result {
			return flows.Result{Texts: []string{strings.Repeat("x", id) + m.Text}, Done: m.Text == "/cancel"}
		})
	}, senderFunc(func(_ context.Context, _ int64, text string) error { replies <- text; return nil }))
	for _, input := range []string{"/start", "hello", "/start", "/cancel", "ignored", "/start"} {
		enqueue(t, d, message(1, input))
	}
	var got []string
	for range 5 {
		got = append(got, receive(t, replies))
	}
	if want := []string{"x/start", "xhello", "xx/start", "xx/cancel", "xxx/start"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestQueueOverflowDoesNotBlockOtherSessions(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	replies := make(chan string, 100)
	d := testDispatcher(t, func() flows.Flow {
		return flowFunc(func(ctx context.Context, m flows.Message) flows.Result {
			if m.UserID == 1 && m.Text == "/start" {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return flows.Result{Texts: []string{m.Text}}
		})
	}, senderFunc(func(_ context.Context, _ int64, text string) error { replies <- text; return nil }))
	enqueue(t, d, message(1, "/start"))
	receive(t, entered)
	for range 32 {
		enqueue(t, d, message(1, "queued"))
	}
	for range 3 {
		if err := d.Enqueue(message(1, "rejected")); !errors.Is(err, ErrQueueFull) {
			t.Fatalf("overflow: %v", err)
		}
	}
	enqueue(t, d, message(2, "/start"))
	if got := receive(t, replies); got != "/start" {
		t.Fatal(got)
	}
	close(release)
	for range 33 {
		if got := receive(t, replies); got == "rejected" {
			t.Fatal("rejected message was processed")
		}
	}
	if got := receive(t, replies); !strings.Contains(got, "повтор") {
		t.Fatalf("missing overflow notice: %s", got)
	}
}

func TestSendFailureDoesNotRepeatCompletedOperation(t *testing.T) {
	replies := make(chan string, 10)
	saves := 0
	d := testDispatcher(t, func() flows.Flow {
		return flowFunc(func(_ context.Context, m flows.Message) flows.Result {
			if m.Text == "save" {
				saves++
				return flows.Result{Texts: []string{"saved", "must not send"}, Done: true}
			}
			return flows.Result{Texts: []string{m.Text}}
		})
	}, senderFunc(func(_ context.Context, _ int64, text string) error {
		replies <- text
		if text == "saved" {
			return errors.New("delivery failed")
		}
		return nil
	}))
	for _, input := range []string{"/start", "save", "save", "/start"} {
		enqueue(t, d, message(1, input))
	}
	for _, want := range []string{"/start", "saved", "/start"} {
		if got := receive(t, replies); got != want {
			t.Fatalf("got %s want %s", got, want)
		}
	}
	d.Close()
	if saves != 1 {
		t.Fatalf("saved %d times", saves)
	}
}

type fakeClock struct {
	mu    sync.Mutex
	value time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.value }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value = c.value.Add(d)
}

func TestExpiryKeepsBusyAndQueuedSessions(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clock := &fakeClock{value: time.Unix(100, 0)}
		entered, release := make(chan struct{}), make(chan struct{})
		replies := make(chan string, 10)
		d := New(context.Background(), nil, nil, senderFunc(func(_ context.Context, _ int64, text string) error { replies <- text; return nil }))
		d.now = clock.Now
		d.factories = map[string]func() flows.Flow{"start": func() flows.Flow {
			return flowFunc(func(ctx context.Context, m flows.Message) flows.Result {
				if m.Text == "block" {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
					}
				}
				return flows.Result{Texts: []string{m.Text}}
			})
		}}
		t.Cleanup(d.Close)
		enqueue(t, d, message(1, "/start"))
		receive(t, replies)
		enqueue(t, d, message(1, "block"))
		receive(t, entered)
		enqueue(t, d, message(1, "queued"))
		clock.Advance(31 * time.Minute)
		d.expire(clock.Now())
		close(release)
		for _, want := range []string{"block", "queued"} {
			if got := receive(t, replies); got != want {
				t.Fatalf("lost pending session: %s", got)
			}
		}
		synctest.Wait()
		clock.Advance(31 * time.Minute)
		d.expire(clock.Now())
		d.mu.Lock()
		count := len(d.sessions)
		d.mu.Unlock()
		if count != 0 {
			t.Fatalf("idle session not removed: %d", count)
		}
	})
}

func TestIdleSessionExpiresAndMustRestart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clock := &fakeClock{value: time.Unix(100, 0)}
		replies := make(chan string, 10)
		d := New(context.Background(), nil, nil, senderFunc(func(_ context.Context, _ int64, text string) error { replies <- text; return nil }))
		d.now = clock.Now
		d.factories = map[string]func() flows.Flow{"start": func() flows.Flow {
			return flowFunc(func(_ context.Context, m flows.Message) flows.Result { return flows.Result{Texts: []string{m.Text}} })
		}}
		t.Cleanup(d.Close)
		enqueue(t, d, message(1, "/start"))
		receive(t, replies)
		synctest.Wait()
		clock.Advance(30 * time.Minute)
		d.expire(clock.Now())
		enqueue(t, d, message(1, "orphan"))
		synctest.Wait()
		select {
		case got := <-replies:
			t.Fatalf("expired flow answered: %s", got)
		default:
		}
		enqueue(t, d, message(1, "/start"))
		if got := receive(t, replies); got != "/start" {
			t.Fatalf("restart: %s", got)
		}
	})
}

func TestCloseCancelsActiveWorkAndRejectsNewMessages(t *testing.T) {
	entered, stopped := make(chan struct{}), make(chan struct{})
	d := testDispatcher(t, func() flows.Flow {
		return flowFunc(func(ctx context.Context, m flows.Message) flows.Result {
			close(entered)
			<-ctx.Done()
			close(stopped)
			return flows.Result{}
		})
	}, senderFunc(func(context.Context, int64, string) error { return nil }))
	enqueue(t, d, message(1, "/start"))
	receive(t, entered)
	enqueue(t, d, message(1, "queued"))
	d.Close()
	receive(t, stopped)
	if err := d.Enqueue(message(1, "/start")); !errors.Is(err, ErrClosed) {
		t.Fatalf("enqueue after close: %v", err)
	}
	d.Close()
}
