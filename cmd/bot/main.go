package main

import (
	"log"
	"os"

	"rss-reader/internal/ai"
	"rss-reader/internal/bot"
	"rss-reader/internal/db"
)

func main() {
	token := mustEnv("TELEGRAM_TOKEN")
	dbURL := mustEnv("DATABASE_URL")

	database, err := db.New(dbURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	// Gemini client for media analysis (optional)
	var geminiClient *ai.GeminiClient
	if geminiKey := os.Getenv("GEMINI_API_KEY"); geminiKey != "" {
		model := getenv("GEMINI_MODEL", "gemini-2.0-flash")
		vaultPath := os.Getenv("OBSIDIAN_VAULT_PATH")
		geminiClient = ai.NewGeminiClient(geminiKey, model, vaultPath)
		log.Printf("bot: Gemini enabled (model=%s, vault=%s)", model, vaultPath)
	} else {
		log.Println("bot: GEMINI_API_KEY not set — media analysis disabled")
	}

	tgBot, err := bot.New(token, database, geminiClient)
	if err != nil {
		log.Fatalf("bot: %v", err)
	}

	log.Println("bot started")
	tgBot.Run()
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("env %s is required", key)
	}
	return v
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
