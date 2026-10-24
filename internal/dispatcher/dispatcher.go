package dispatcher

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	"xrayl/internal/flows"
)

var (
	ErrQueueFull = errors.New("session queue is full")
	ErrClosed    = errors.New("dispatcher is closed")
)

const (
	queueSize     = 32
	idleTimeout   = 30 * time.Minute
	sweepInterval = time.Minute
)

type Sender interface {
	SendText(context.Context, int64, string) error
}

type sessionKey struct{ chatID, userID int64 }

type session struct {
	flow       flows.Flow
	queue      chan flows.Message
	wake       chan struct{}
	busy       bool
	overflow   bool
	lastActive time.Time
}

type Dispatcher struct {
	mu        sync.Mutex
	sessions  map[sessionKey]*session
	factories map[string]func() flows.Flow
	sender    Sender
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closed    bool
	now       func() time.Time
}

func New(ctx context.Context, dictionary flows.Dictionary, store flows.Store, sender Sender) *Dispatcher {
	ctx, cancel := context.WithCancel(ctx)
	d := &Dispatcher{
		sessions: make(map[sessionKey]*session), sender: sender, ctx: ctx, cancel: cancel, now: time.Now,
		factories: map[string]func() flows.Flow{
			"addword":   func() flows.Flow { return flows.NewAddword(dictionary, store) },
			"words":     func() flows.Flow { return flows.NewWords(store) },
			"addsource": func() flows.Flow { return flows.NewAddsource(store) },
			"sources":   func() flows.Flow { return flows.NewSources(store) },
		},
	}
	d.wg.Add(1)
	go d.sweep()
	return d
}

func (d *Dispatcher) Enqueue(message flows.Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.ctx.Err() != nil {
		return ErrClosed
	}
	key := sessionKey{chatID: message.ChatID, userID: message.UserID}
	s := d.sessions[key]
	if s == nil {
		if d.factories[message.Command] == nil {
			return nil
		}
		s = &session{queue: make(chan flows.Message, queueSize), wake: make(chan struct{}, 1), lastActive: d.now()}
		d.sessions[key] = s
		d.wg.Add(1)
		go d.run(key, s)
	}
	select {
	case s.queue <- message:
		s.lastActive = d.now()
		signal(s)
		return nil
	default:
		s.overflow = true
		return ErrQueueFull
	}
}

func signal(s *session) {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (d *Dispatcher) run(key sessionKey, s *session) {
	defer d.wg.Done()
	for {
		d.mu.Lock()
		if d.ctx.Err() != nil || d.sessions[key] != s {
			d.mu.Unlock()
			return
		}
		select {
		case message := <-s.queue:
			s.busy = true
			d.mu.Unlock()
			d.process(s, message)
		default:
			if s.overflow {
				s.overflow = false
				s.busy = true
				d.mu.Unlock()
				d.send(key, []string{"Слишком много сообщений. Некоторые не приняты — повтори нужный ввод."})
			} else {
				d.mu.Unlock()
				select {
				case <-d.ctx.Done():
					return
				case <-s.wake:
				}
				continue
			}
		}
		d.mu.Lock()
		s.busy = false
		s.lastActive = d.now()
		d.mu.Unlock()
	}
}

func (d *Dispatcher) process(s *session, message flows.Message) {
	if create := d.factories[message.Command]; create != nil {
		s.flow = create()
	}
	if s.flow == nil {
		return
	}
	result := s.flow.Handle(d.ctx, message)
	if result.Done {
		s.flow = nil
	}
	d.send(sessionKey{chatID: message.ChatID, userID: message.UserID}, result.Texts)
}

func (d *Dispatcher) send(key sessionKey, texts []string) {
	for _, text := range texts {
		if d.ctx.Err() != nil {
			return
		}
		if err := d.sender.SendText(d.ctx, key.chatID, text); err != nil {
			log.Printf("send reply failed chat=%d user=%d: %v", key.chatID, key.userID, err)
			return
		}
	}
}

func (d *Dispatcher) sweep() {
	defer d.wg.Done()
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-d.ctx.Done():
			return
		case now := <-ticker.C:
			d.expire(now)
		}
	}
}

func (d *Dispatcher) expire(now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for key, s := range d.sessions {
		if !s.busy && len(s.queue) == 0 && !s.overflow && now.Sub(s.lastActive) >= idleTimeout {
			delete(d.sessions, key)
			signal(s)
		}
	}
}

func (d *Dispatcher) Close() {
	d.mu.Lock()
	d.closed = true
	d.cancel()
	d.mu.Unlock()
	d.wg.Wait()
	d.mu.Lock()
	clear(d.sessions)
	d.mu.Unlock()
}
