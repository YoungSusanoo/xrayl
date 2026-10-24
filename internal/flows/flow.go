package flows

import "context"

type Message struct {
	ChatID  int64
	UserID  int64
	Text    string
	Command string
}

type Result struct {
	Texts []string
	Done  bool
}

type Flow interface {
	Handle(context.Context, Message) Result
}
