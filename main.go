package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/joho/godotenv"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatalf("failed to load .env: %v", err)
	}

	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		log.Fatal("BOT_TOKEN is not set")
	}

	bot, err := tg.New(token, tg.WithDefaultHandler(handler))
	if err != nil {
		log.Fatal("Can't create bot")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	bot.Start(ctx)
}

func handler(ctx context.Context, b *tg.Bot, update *models.Update) {
	b.SendMessage(ctx, &tg.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   "Privet-privet",
	})
}
