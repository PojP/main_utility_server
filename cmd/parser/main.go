package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"rss-reader/internal/ai"
	"rss-reader/internal/db"
	"rss-reader/internal/embeddings"
	"rss-reader/internal/parser"
	"rss-reader/internal/processor"
	"rss-reader/internal/qdrant"
)

func main() {
	dbURL := mustEnv("DATABASE_URL")
	botToken := os.Getenv("TELEGRAM_TOKEN")
	notifyChat := getenvInt64("NOTIFY_CHAT_ID", 0)
	intervalMin := getenvInt("PARSE_INTERVAL_MIN", 15)

	database, err := db.New(dbURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	// OpenRouter client for AI enrichment (optional)
	var orClient *ai.OpenRouterClient
	if orKey := os.Getenv("OPENROUTER_API_KEY"); orKey != "" {
		model := getenv("OPENROUTER_MODEL", "qwen/qwen-2.5-72b-instruct:free")
		orClient = ai.NewOpenRouterClient(orKey, model)
		log.Printf("parser: AI enrichment enabled (model=%s)", model)
	} else {
		log.Println("parser: OPENROUTER_API_KEY not set — AI enrichment disabled")
	}

	// Qdrant + embeddings for indexing articles (optional)
	var embedClient *embeddings.Client
	var qdrantClient *qdrant.Client
	qdrantURL := os.Getenv("QDRANT_URL")
	geminiKey := os.Getenv("GEMINI_API_KEY")
	if qdrantURL != "" && geminiKey != "" {
		embedClient = embeddings.New(geminiKey, getenv("EMBEDDING_MODEL", ""))
		qdrantClient = qdrant.New(qdrantURL, embeddings.VectorSize)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := qdrantClient.EnsureCollection(ctx); err != nil {
			log.Printf("parser: qdrant init: %v (embedding disabled)", err)
			qdrantClient = nil
			embedClient = nil
		} else {
			log.Printf("parser: Qdrant enabled (%s)", qdrantURL)
		}
		cancel()
	}

	proc := processor.New(database, orClient, embedClient, qdrantClient)
	p := parser.New(database, proc, time.Duration(intervalMin)*time.Minute, botToken, notifyChat)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Printf("rss parser started, interval: %d min", intervalMin)
	p.Run(ctx)
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

func getenvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func getenvInt64(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}
