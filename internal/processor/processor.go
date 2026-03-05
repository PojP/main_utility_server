// Package processor handles article deduplication and AI enrichment.
package processor

import (
	"context"
	"log"
	"time"

	"rss-reader/internal/ai"
	"rss-reader/internal/db"
	"rss-reader/internal/similarity"
)

// Result describes what happened to a processed article.
type Result int

const (
	ResultNew       Result = iota // new article saved
	ResultDuplicate               // similar article exists and is richer, skipped
	ResultReplaced                // existing article replaced by richer version
)

const recentLimit = 2000 // how many recent articles to scan for similarity

// Processor deduplicates articles and enriches them with AI-generated titles/summaries/tags.
type Processor struct {
	db         *db.DB
	openrouter *ai.OpenRouterClient // nil if not configured
}

// New creates a Processor. openrouter may be nil (AI enrichment disabled).
func New(database *db.DB, openrouter *ai.OpenRouterClient) *Processor {
	return &Processor{db: database, openrouter: openrouter}
}

// Process saves an article after checking for semantic duplicates.
// Returns (articleID, result, error). articleID is 0 when skipped (ResultDuplicate).
func (p *Processor) Process(
	ctx context.Context,
	sourceID int64,
	externalID, title, content, url, imageURL string,
	pubDate *time.Time,
) (int64, Result, error) {
	// Fetch recent articles for similarity comparison
	recent, err := p.db.RecentArticlesForSimilarity(ctx, recentLimit)
	if err != nil {
		// Log but don't abort — fall through to normal save
		log.Printf("processor: fetch recent for similarity: %v", err)
	}

	// Find most similar article
	newText := title + " " + content
	var bestMatch *db.ArticleLite
	var bestSim float64
	for i := range recent {
		sim := similarity.Cosine(newText, recent[i].Title+" "+recent[i].Content)
		if sim > bestSim {
			bestSim = sim
			bestMatch = &recent[i]
		}
	}

	if bestMatch != nil && bestSim >= similarity.Threshold {
		log.Printf("processor: duplicate detected (sim=%.2f) new=%q similar_id=%d",
			bestSim, externalID, bestMatch.ID)

		newLen := len([]rune(content))
		existLen := len([]rune(bestMatch.Content))

		if newLen > existLen {
			// New article is more detailed — update the existing one
			if err := p.db.UpdateArticleContent(ctx, bestMatch.ID, title, content, url); err != nil {
				return 0, ResultDuplicate, err
			}
			log.Printf("processor: replaced article %d with richer version (%d > %d runes)",
				bestMatch.ID, newLen, existLen)
			p.enrichAsync(bestMatch.ID, title, content)
			return bestMatch.ID, ResultReplaced, nil
		}

		// Existing article is richer — skip the new one
		return 0, ResultDuplicate, nil
	}

	// No duplicate — save as new article
	id, err := p.db.SaveArticleFull(ctx, sourceID, externalID, title, content, url, imageURL, pubDate)
	if err != nil {
		return 0, ResultNew, err
	}
	if id == 0 {
		// ON CONFLICT fired (same source+externalID already in DB) — exact duplicate
		return 0, ResultDuplicate, nil
	}

	p.enrichAsync(id, title, content)
	return id, ResultNew, nil
}

// enrichAsync runs AI title+summary+tags generation in a goroutine.
// Failures are only logged — the article is already saved.
func (p *Processor) enrichAsync(articleID int64, title, content string) {
	if p.openrouter == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		aiTitle, summary, tags, err := p.openrouter.EnrichArticle(ctx, title, content)
		if err != nil {
			log.Printf("processor: AI enrich article %d: %v", articleID, err)
			return
		}
		if aiTitle == "" && summary == "" && len(tags) == 0 {
			return
		}
		if err := p.db.UpdateArticleAI(ctx, articleID, aiTitle, summary, tags); err != nil {
			log.Printf("processor: save AI fields for article %d: %v", articleID, err)
		}
	}()
}
