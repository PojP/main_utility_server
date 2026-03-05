package db

import "time"

type SourceType string

const (
	SourceRSS      SourceType = "rss"
	SourceTelegram SourceType = "telegram"
)

type Source struct {
	ID           int64
	URL          string
	Name         string
	SourceType   SourceType
	ChatID       int64 // telegram chat_id of user who added
	CreatedAt    time.Time
	LastTgMsgID  int64 // highest processed Telegram message ID (0 = never polled)
}

type Article struct {
	ID         int64
	SourceID   int64
	ExternalID string // RSS link or telegram message ID
	Title      string
	Content    string
	URL        string
	ImageURL   string
	PubDate    *time.Time
	CreatedAt  time.Time
	Summary    string   // AI-generated summary
	AITitle    string   // AI-generated title
	Tags       []string // AI-generated tags
	Likes      int
	Dislikes   int
	SourceName string // filled by joins, not stored separately
}

// ArticleLite is a lightweight version used for similarity comparison.
type ArticleLite struct {
	ID      int64
	Title   string
	Content string
}

// ArticleFilter defines query parameters for GetArticles.
type ArticleFilter struct {
	AfterID int64    // return articles with id < AfterID (scroll down); 0 = no filter
	SinceID int64    // return articles with id > SinceID (live updates); 0 = no filter
	Tags    []string // filter by tags: any match (OR). nil/empty = no filter
	Limit   int
}
