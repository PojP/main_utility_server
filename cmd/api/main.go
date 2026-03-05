package main

import (
	"log"
	"net/http"
	"os"

	"rss-reader/internal/ai"
	"rss-reader/internal/api"
	"rss-reader/internal/db"
)

func main() {
	dbURL := mustEnv("DATABASE_URL")
	addr := getenv("API_ADDR", ":8080")

	database, err := db.New(dbURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	// Gemini client for saving articles to Obsidian (optional)
	var geminiClient *ai.GeminiClient
	if geminiKey := os.Getenv("GEMINI_API_KEY"); geminiKey != "" {
		model := getenv("GEMINI_MODEL", "gemini-2.0-flash")
		vaultPath := os.Getenv("OBSIDIAN_VAULT_PATH")
		geminiClient = ai.NewGeminiClient(geminiKey, model, vaultPath)
		log.Printf("api: Gemini enabled (model=%s, vault=%s)", model, vaultPath)
	} else {
		log.Println("api: GEMINI_API_KEY not set — POST /api/news/{id}/save disabled")
	}

	handler := api.NewHandler(database, geminiClient)
	mux := handler.NewMux()

	log.Printf("api server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("api: %v", err)
	}
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
