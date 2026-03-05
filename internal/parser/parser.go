package parser

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/mmcdole/gofeed"
	"rss-reader/internal/db"
	"rss-reader/internal/notify"
	"rss-reader/internal/processor"
)

type Parser struct {
	db        *db.DB
	proc      *processor.Processor
	interval  time.Duration
	botToken  string
	chatID    int64
}

func New(database *db.DB, proc *processor.Processor, interval time.Duration, botToken string, chatID int64) *Parser {
	return &Parser{
		db:       database,
		proc:     proc,
		interval: interval,
		botToken: botToken,
		chatID:   chatID,
	}
}

func (p *Parser) Run(ctx context.Context) {
	p.parse(ctx)
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.parse(ctx)
		}
	}
}

func (p *Parser) parse(ctx context.Context) {
	sources, err := p.db.ListSourcesByType(ctx, db.SourceRSS)
	if err != nil {
		log.Printf("parser: list sources: %v", err)
		return
	}
	if len(sources) == 0 {
		return
	}

	fp := gofeed.NewParser()
	fp.UserAgent = "rss-reader/1.0"
	fp.Client = &http.Client{Timeout: 30 * time.Second}

	for _, src := range sources {
		feed, err := fp.ParseURLWithContext(src.URL, ctx)
		if err != nil {
			log.Printf("parser: fetch %s: %v", src.URL, err)
			continue
		}

		newCount := 0
		for _, item := range feed.Items {
			var pubDate *time.Time
			if item.PublishedParsed != nil {
				pubDate = item.PublishedParsed
			}

			imageURL := ""
			if item.Image != nil {
				imageURL = item.Image.URL
			}

			content := item.Description
			if item.Content != "" {
				content = item.Content
			}

			id, result, err := p.proc.Process(ctx, src.ID, item.Link, item.Title, content, item.Link, imageURL, pubDate)
			if err != nil {
				log.Printf("parser: process article: %v", err)
				continue
			}

			switch result {
			case processor.ResultNew:
				newCount++
				notify.SendTelegram(p.botToken, p.chatID, item.Title, item.Link)
			case processor.ResultReplaced:
				log.Printf("parser: article %d updated with richer content from %s", id, src.URL)
			}
		}
		log.Printf("parser: %s — %d items, %d new", src.URL, len(feed.Items), newCount)
	}
}
