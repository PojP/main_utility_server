package notify

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"
)

var client = &http.Client{Timeout: 10 * time.Second}

func SendTelegram(botToken string, chatID int64, title, articleURL string) {
	if botToken == "" || chatID == 0 {
		return
	}
	text := fmt.Sprintf("📰 %s\n%s", title, articleURL)
	u := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	resp, err := client.PostForm(u, url.Values{
		"chat_id": {fmt.Sprint(chatID)},
		"text":    {text},
	})
	if err != nil {
		log.Printf("notify: %v", err)
		return
	}
	resp.Body.Close()
}
