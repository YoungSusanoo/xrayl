package flows

import (
	"context"
	"log"
	"time"
	"unicode/utf16"

	tg "github.com/go-telegram/bot"
)

func sendText(ctx context.Context, bot *tg.Bot, chatID int64, text string) error {
	for _, chunk := range splitText(text) {
		sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := bot.SendMessage(sendCtx, &tg.SendMessageParams{ChatID: chatID, Text: chunk})
		cancel()
		if err != nil {
			log.Printf("send message to chat %d failed: %v", chatID, err)
			return err
		}
	}
	return nil
}

func splitText(text string) []string {
	var chunks []string
	start, units := 0, 0
	for index, r := range text {
		length := utf16.RuneLen(r)
		if units+length > 3000 {
			chunks = append(chunks, text[start:index])
			start, units = index, 0
		}
		units += length
	}
	if start < len(text) {
		chunks = append(chunks, text[start:])
	}
	return chunks
}
