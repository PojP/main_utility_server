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
	"rss-reader/internal/processor"
	"rss-reader/internal/qdrant"
	"rss-reader/internal/s3"
	"rss-reader/internal/userbot"
)

func main() {
	dbURL := mustEnv("DATABASE_URL")
	apiID, err := strconv.Atoi(mustEnv("TG_API_ID"))
	if err != nil {
		log.Fatalf("TG_API_ID must be a number: %v", err)
	}
	apiHash := mustEnv("TG_API_HASH")

	database, err := db.New(dbURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	s3cfg := s3.Config{
		Endpoint:  getenv("S3_ENDPOINT", "minio:9000"),
		AccessKey: getenv("S3_ACCESS_KEY", "minioadmin"),
		SecretKey: getenv("S3_SECRET_KEY", "minioadmin"),
		Bucket:    getenv("S3_BUCKET", "news-images"),
		PublicURL: getenv("S3_PUBLIC_URL", "http://localhost:9000"),
		UseSSL:    false,
	}
	s3client, err := s3.New(s3cfg)
	if err != nil {
		log.Fatalf("s3: %v", err)
	}

	// OpenRouter client for AI enrichment (optional)
	var orClient *ai.OpenRouterClient
	if orKey := os.Getenv("OPENROUTER_API_KEY"); orKey != "" {
		model := getenv("OPENROUTER_MODEL", "qwen/qwen-2.5-72b-instruct:free")
		orClient = ai.NewOpenRouterClient(orKey, model)
		log.Printf("userbot: AI enrichment enabled (model=%s)", model)
	} else {
		log.Println("userbot: OPENROUTER_API_KEY not set — AI enrichment disabled")
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
			log.Printf("userbot: qdrant init: %v (embedding disabled)", err)
			qdrantClient = nil
			embedClient = nil
		} else {
			log.Printf("userbot: Qdrant enabled (%s)", qdrantURL)
		}
		cancel()
	}

	proc := processor.New(database, orClient, embedClient, qdrantClient)

	cfg := userbot.Config{
		APIID:       apiID,
		APIHash:     apiHash,
		Phone:       os.Getenv("TG_PHONE"),
		Password:    os.Getenv("TG_2FA_PASSWORD"),
		SessionPath: getenv("TG_SESSION_PATH", "/data/userbot.session"),
		Interval:    time.Duration(getenvInt("PARSE_INTERVAL_MIN", 15)) * time.Minute,
		BotToken:    os.Getenv("TELEGRAM_TOKEN"),
		NotifyChat:  getenvInt64("NOTIFY_CHAT_ID", 0),
	}

	ub := userbot.New(cfg, database, s3client, proc)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log.Println("userbot starting (first run requires interactive auth: docker compose run -it userbot)")
	if err := ub.Run(ctx); err != nil {
		log.Fatalf("userbot: %v", err)
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
