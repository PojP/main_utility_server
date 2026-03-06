package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"rss-reader/internal/ai"
	"rss-reader/internal/api"
	"rss-reader/internal/db"
	"rss-reader/internal/embeddings"
	"rss-reader/internal/qdrant"
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
	geminiKey := os.Getenv("GEMINI_API_KEY")
	if geminiKey != "" {
		model := getenv("GEMINI_MODEL", "gemini-2.0-flash")
		vaultPath := os.Getenv("OBSIDIAN_VAULT_PATH")
		geminiClient = ai.NewGeminiClient(geminiKey, model, vaultPath)
		log.Printf("api: Gemini enabled (model=%s, vault=%s)", model, vaultPath)
	} else {
		log.Println("api: GEMINI_API_KEY not set — POST /api/news/{id}/save disabled")
	}

	// Qdrant + embeddings for recommendations (optional)
	var embedClient *embeddings.Client
	var qdrantClient *qdrant.Client
	qdrantURL := os.Getenv("QDRANT_URL")
	if qdrantURL != "" && geminiKey != "" {
		embedClient = embeddings.New(geminiKey, getenv("EMBEDDING_MODEL", ""))
		qdrantClient = qdrant.New(qdrantURL, embeddings.VectorSize)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := qdrantClient.EnsureCollection(ctx); err != nil {
			log.Printf("api: qdrant collection init: %v (recommendations disabled)", err)
			qdrantClient = nil
			embedClient = nil
		} else {
			log.Printf("api: Qdrant enabled (%s)", qdrantURL)
		}
		cancel()
	} else {
		log.Println("api: QDRANT_URL or GEMINI_API_KEY not set — recommendations disabled")
	}

	handler := api.NewHandler(database, geminiClient, embedClient, qdrantClient)
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
