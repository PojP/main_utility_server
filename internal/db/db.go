package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	pool *pgxpool.Pool
}

func New(databaseURL string) (*DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("pgx connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("pgx ping: %w", err)
	}
	if err := migrate(ctx, pool); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{pool: pool}, nil
}

func (db *DB) Close() {
	db.pool.Close()
}

func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS sources (
			id          SERIAL PRIMARY KEY,
			url         TEXT UNIQUE NOT NULL,
			name        TEXT NOT NULL DEFAULT '',
			source_type TEXT NOT NULL DEFAULT 'rss',
			chat_id     BIGINT NOT NULL DEFAULT 0,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		CREATE TABLE IF NOT EXISTS articles (
			id          SERIAL PRIMARY KEY,
			source_id   INTEGER NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
			external_id TEXT NOT NULL DEFAULT '',
			title       TEXT NOT NULL DEFAULT '',
			content     TEXT NOT NULL DEFAULT '',
			url         TEXT NOT NULL DEFAULT '',
			image_url   TEXT NOT NULL DEFAULT '',
			pub_date    TIMESTAMPTZ,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(source_id, external_id)
		);
		CREATE INDEX IF NOT EXISTS idx_articles_created_at ON articles(id DESC);
		CREATE INDEX IF NOT EXISTS idx_articles_source_id ON articles(source_id);
	`)
	if err != nil {
		return err
	}
	// Non-destructive column additions for existing deployments
	_, err = pool.Exec(ctx, `
		ALTER TABLE articles ADD COLUMN IF NOT EXISTS summary  TEXT NOT NULL DEFAULT '';
		ALTER TABLE articles ADD COLUMN IF NOT EXISTS ai_title TEXT NOT NULL DEFAULT '';
		ALTER TABLE articles ADD COLUMN IF NOT EXISTS tags     TEXT[] NOT NULL DEFAULT '{}';
		ALTER TABLE articles ADD COLUMN IF NOT EXISTS likes    INT NOT NULL DEFAULT 0;
		ALTER TABLE articles ADD COLUMN IF NOT EXISTS dislikes INT NOT NULL DEFAULT 0;
	`)
	if err != nil {
		return err
	}
	// GIN index for fast tag filtering
	_, err = pool.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS idx_articles_tags ON articles USING GIN(tags);
	`)
	if err != nil {
		return err
	}
	// last processed Telegram message ID per source
	_, err = pool.Exec(ctx, `
		ALTER TABLE sources ADD COLUMN IF NOT EXISTS last_tg_msg_id BIGINT NOT NULL DEFAULT 0;
	`)
	return err
}

// AddSource adds a new source. Returns true if actually inserted.
func (db *DB) AddSource(ctx context.Context, url, name string, srcType SourceType, chatID int64) (bool, error) {
	tag, err := db.pool.Exec(ctx,
		"INSERT INTO sources (url, name, source_type, chat_id) VALUES ($1, $2, $3, $4) ON CONFLICT (url) DO NOTHING",
		url, name, string(srcType), chatID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func (db *DB) ListSources(ctx context.Context) ([]Source, error) {
	rows, err := db.pool.Query(ctx, "SELECT id, url, name, source_type, chat_id, created_at, last_tg_msg_id FROM sources ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []Source
	for rows.Next() {
		var s Source
		if err := rows.Scan(&s.ID, &s.URL, &s.Name, &s.SourceType, &s.ChatID, &s.CreatedAt, &s.LastTgMsgID); err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

func (db *DB) ListSourcesByType(ctx context.Context, srcType SourceType) ([]Source, error) {
	rows, err := db.pool.Query(ctx,
		"SELECT id, url, name, source_type, chat_id, created_at, last_tg_msg_id FROM sources WHERE source_type = $1 ORDER BY id",
		string(srcType))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []Source
	for rows.Next() {
		var s Source
		if err := rows.Scan(&s.ID, &s.URL, &s.Name, &s.SourceType, &s.ChatID, &s.CreatedAt, &s.LastTgMsgID); err != nil {
			return nil, err
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

// UpdateSourceLastMsgID saves the highest processed Telegram message ID for a source.
func (db *DB) UpdateSourceLastMsgID(ctx context.Context, sourceID, msgID int64) error {
	_, err := db.pool.Exec(ctx,
		"UPDATE sources SET last_tg_msg_id = $1 WHERE id = $2 AND last_tg_msg_id < $1",
		msgID, sourceID)
	return err
}

func (db *DB) RemoveSource(ctx context.Context, id int64) error {
	_, err := db.pool.Exec(ctx, "DELETE FROM sources WHERE id = $1", id)
	return err
}

// SaveArticle inserts an article. Returns true if it was new (not a duplicate).
// Kept for backward compatibility; prefer SaveArticleFull when the new ID is needed.
func (db *DB) SaveArticle(ctx context.Context, sourceID int64, externalID, title, content, url, imageURL string, pubDate *time.Time) (bool, error) {
	tag, err := db.pool.Exec(ctx,
		`INSERT INTO articles (source_id, external_id, title, content, url, image_url, pub_date)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (source_id, external_id) DO NOTHING`,
		sourceID, externalID, title, content, url, imageURL, pubDate)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// SaveArticleFull inserts an article and returns its new ID.
// Returns 0 (not an error) when ON CONFLICT fires (same source+externalID already exists).
func (db *DB) SaveArticleFull(ctx context.Context, sourceID int64, externalID, title, content, url, imageURL string, pubDate *time.Time) (int64, error) {
	var id int64
	err := db.pool.QueryRow(ctx,
		`INSERT INTO articles (source_id, external_id, title, content, url, image_url, pub_date)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (source_id, external_id) DO NOTHING
		 RETURNING id`,
		sourceID, externalID, title, content, url, imageURL, pubDate,
	).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// UpdateArticleContent replaces content/title/url for an existing article
// when a richer version from a different source is found.
func (db *DB) UpdateArticleContent(ctx context.Context, id int64, title, content, url string) error {
	_, err := db.pool.Exec(ctx,
		`UPDATE articles SET title=$1, content=$2, url=$3 WHERE id=$4`,
		title, content, url, id)
	return err
}

// UpdateArticleAI stores the AI-generated title, summary and tags.
func (db *DB) UpdateArticleAI(ctx context.Context, id int64, aiTitle, summary string, tags []string) error {
	if tags == nil {
		tags = []string{}
	}
	_, err := db.pool.Exec(ctx,
		`UPDATE articles SET ai_title=$1, summary=$2, tags=$3 WHERE id=$4`,
		aiTitle, summary, tags, id)
	return err
}

// RateArticle increments likes or dislikes counter for an article.
func (db *DB) RateArticle(ctx context.Context, id int64, isGood bool) error {
	var err error
	if isGood {
		_, err = db.pool.Exec(ctx, "UPDATE articles SET likes = likes + 1 WHERE id = $1", id)
	} else {
		_, err = db.pool.Exec(ctx, "UPDATE articles SET dislikes = dislikes + 1 WHERE id = $1", id)
	}
	return err
}

// RecentArticlesForSimilarity returns lightweight article data from the last 7 days.
func (db *DB) RecentArticlesForSimilarity(ctx context.Context, limit int) ([]ArticleLite, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT id, title, content FROM articles
		 WHERE created_at > NOW() - INTERVAL '7 days'
		 ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []ArticleLite
	for rows.Next() {
		var a ArticleLite
		if err := rows.Scan(&a.ID, &a.Title, &a.Content); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

// GetArticle returns a single article by ID, or nil if not found.
func (db *DB) GetArticle(ctx context.Context, id int64) (*Article, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT a.id, a.source_id, a.external_id, a.title, a.content, a.url, a.image_url,
		        a.pub_date, a.created_at, a.summary, a.ai_title, a.tags, a.likes, a.dislikes,
		        COALESCE(s.name, s.url) as source_name
		 FROM articles a
		 LEFT JOIN sources s ON s.id = a.source_id
		 WHERE a.id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	articles, err := scanArticles(rows)
	if err != nil || len(articles) == 0 {
		return nil, err
	}
	return &articles[0], nil
}

// LatestArticles returns the most recent articles.
func (db *DB) LatestArticles(ctx context.Context, limit int) ([]Article, error) {
	rows, err := db.pool.Query(ctx,
		`SELECT a.id, a.source_id, a.external_id, a.title, a.content, a.url, a.image_url,
		        a.pub_date, a.created_at, a.summary, a.ai_title, a.tags, a.likes, a.dislikes,
		        COALESCE(s.name, s.url) as source_name
		 FROM articles a
		 LEFT JOIN sources s ON s.id = a.source_id
		 ORDER BY a.id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArticles(rows)
}

// GetArticles returns articles matching the given filter with cursor-pagination.
func (db *DB) GetArticles(ctx context.Context, f ArticleFilter) ([]Article, error) {
	const base = `SELECT a.id, a.source_id, a.external_id, a.title, a.content, a.url, a.image_url,
	                     a.pub_date, a.created_at, a.summary, a.ai_title, a.tags, a.likes, a.dislikes,
	                     COALESCE(s.name, s.url) as source_name
	              FROM articles a
	              LEFT JOIN sources s ON s.id = a.source_id`

	var conditions []string
	var args []any

	if f.AfterID > 0 {
		args = append(args, f.AfterID)
		conditions = append(conditions, fmt.Sprintf("a.id < $%d", len(args)))
	} else if f.SinceID > 0 {
		args = append(args, f.SinceID)
		conditions = append(conditions, fmt.Sprintf("a.id > $%d", len(args)))
	}

	if len(f.Tags) > 0 {
		args = append(args, f.Tags)
		conditions = append(conditions, fmt.Sprintf("a.tags && $%d::text[]", len(args)))
	}

	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	orderDir := "DESC"
	if f.SinceID > 0 {
		orderDir = "ASC"
	}

	args = append(args, f.Limit)
	query := fmt.Sprintf("%s%s ORDER BY a.id %s LIMIT $%d", base, where, orderDir, len(args))

	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArticles(rows)
}

// ArticlesAfter returns articles with id < afterID (older), for scrolling down.
// Deprecated: use GetArticles with ArticleFilter.
func (db *DB) ArticlesAfter(ctx context.Context, afterID int64, limit int) ([]Article, error) {
	return db.GetArticles(ctx, ArticleFilter{AfterID: afterID, Limit: limit})
}

// ArticlesSince returns articles with id > sinceID (newer), for live updates.
// Deprecated: use GetArticles with ArticleFilter.
func (db *DB) ArticlesSince(ctx context.Context, sinceID int64, limit int) ([]Article, error) {
	return db.GetArticles(ctx, ArticleFilter{SinceID: sinceID, Limit: limit})
}

type pgxRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanArticles(rows pgxRows) ([]Article, error) {
	var articles []Article
	for rows.Next() {
		var a Article
		if err := rows.Scan(
			&a.ID, &a.SourceID, &a.ExternalID, &a.Title, &a.Content,
			&a.URL, &a.ImageURL, &a.PubDate, &a.CreatedAt,
			&a.Summary, &a.AITitle, &a.Tags, &a.Likes, &a.Dislikes,
			&a.SourceName,
		); err != nil {
			return nil, err
		}
		if a.Tags == nil {
			a.Tags = []string{}
		}
		articles = append(articles, a)
	}
	return articles, rows.Err()
}
