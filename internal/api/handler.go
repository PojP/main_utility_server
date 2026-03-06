package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rss-reader/internal/ai"
	"rss-reader/internal/db"
	"rss-reader/internal/embeddings"
	"rss-reader/internal/qdrant"
)

type Handler struct {
	db       *db.DB
	gemini   *ai.GeminiClient    // nil if Gemini not configured
	embedder *embeddings.Client  // nil if embeddings not configured
	qdrant   *qdrant.Client      // nil if Qdrant not configured
}

func NewHandler(database *db.DB, gemini *ai.GeminiClient, embedder *embeddings.Client, qd *qdrant.Client) *Handler {
	return &Handler{db: database, gemini: gemini, embedder: embedder, qdrant: qd}
}

type articleJSON struct {
	ID        int64      `json:"id"`
	SourceID  int64      `json:"source_id"`
	Title     string     `json:"title"`
	AITitle   string     `json:"ai_title,omitempty"`
	Summary   string     `json:"summary,omitempty"`
	Tags      []string   `json:"tags,omitempty"`
	Content   string     `json:"content"`
	URL       string     `json:"url"`
	ImageURL  string     `json:"image_url,omitempty"`
	Source    string     `json:"source"`
	Likes     int        `json:"likes"`
	Dislikes  int        `json:"dislikes"`
	PubDate   *time.Time `json:"pub_date,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type newsResponse struct {
	Articles []articleJSON `json:"articles"`
	HasMore  bool          `json:"has_more"`
}

func (h *Handler) NewMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", h.health)
	mux.HandleFunc("GET /api/news", h.getNews)
	mux.HandleFunc("GET /api/news/recommended", h.getRecommended)
	mux.HandleFunc("GET /api/news/{id}", h.getArticle)
	mux.HandleFunc("GET /api/news/{id}/summary", h.getArticleSummary)
	mux.HandleFunc("POST /api/news/{id}/rate", h.rateArticle)
	mux.HandleFunc("POST /api/news/{id}/save", h.saveArticle)
	return withCORS(mux)
}

// GET /api/news?limit=20&after=<id>&since=<id>&tags=tag1,tag2
//
// Pagination modes:
//   - No params: latest articles
//   - after=123: articles older than id 123 (scroll down)
//   - since=456: articles newer than id 456 (live updates / pull-to-refresh)
//
// Tag filter (OR semantics — any match):
//   - tags=россия,санкции
func (h *Handler) getNews(w http.ResponseWriter, r *http.Request) {
	limit := queryInt(r, "limit", 20)
	if limit < 1 || limit > 100 {
		limit = 20
	}

	// Parse optional tags filter
	var tags []string
	if t := r.URL.Query().Get("tags"); t != "" {
		for _, tag := range strings.Split(t, ",") {
			if tag = strings.TrimSpace(tag); tag != "" {
				tags = append(tags, tag)
			}
		}
	}

	f := db.ArticleFilter{
		Limit: limit + 1, // fetch one extra to determine has_more
		Tags:  tags,
	}
	if afterStr := r.URL.Query().Get("after"); afterStr != "" {
		f.AfterID, _ = strconv.ParseInt(afterStr, 10, 64)
	} else if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		f.SinceID, _ = strconv.ParseInt(sinceStr, 10, 64)
	}

	articles, err := h.db.GetArticles(r.Context(), f)
	if err != nil {
		log.Printf("api: getNews: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	hasMore := len(articles) > limit
	if hasMore {
		articles = articles[:limit]
	}

	resp := newsResponse{
		Articles: make([]articleJSON, 0, len(articles)),
		HasMore:  hasMore,
	}
	for _, a := range articles {
		resp.Articles = append(resp.Articles, toArticleJSON(a))
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("api: encode response: %v", err)
	}
}

// GET /api/news/{id} — full article JSON.
func (h *Handler) getArticle(w http.ResponseWriter, r *http.Request) {
	article, ok := h.fetchArticle(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(toArticleJSON(*article)); err != nil {
		log.Printf("api: encode article: %v", err)
	}
}

// GET /api/news/{id}/summary — lightweight summary: ai_title, summary, tags.
func (h *Handler) getArticleSummary(w http.ResponseWriter, r *http.Request) {
	article, ok := h.fetchArticle(w, r)
	if !ok {
		return
	}
	type summaryResp struct {
		ID      int64    `json:"id"`
		Title   string   `json:"title"`
		AITitle string   `json:"ai_title,omitempty"`
		Summary string   `json:"summary,omitempty"`
		Tags    []string `json:"tags,omitempty"`
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(summaryResp{
		ID:      article.ID,
		Title:   article.Title,
		AITitle: article.AITitle,
		Summary: article.Summary,
		Tags:    article.Tags,
	}); err != nil {
		log.Printf("api: encode summary: %v", err)
	}
}

// POST /api/news/{id}/rate — rate an article.
// Body: {"vote":"good"} or {"vote":"bad"}
// Response: {"likes":N,"dislikes":N}
func (h *Handler) rateArticle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	var body struct {
		Vote string `json:"vote"` // "good" or "bad"
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.Vote != "good" && body.Vote != "bad") {
		http.Error(w, `{"error":"vote must be \"good\" or \"bad\""}`, http.StatusBadRequest)
		return
	}

	isGood := body.Vote == "good"
	if err := h.db.RateArticle(r.Context(), id, isGood); err != nil {
		log.Printf("api: rateArticle %d: %v", id, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Embed and store in Qdrant for recommendations
	if h.embedder != nil && h.qdrant != nil {
		go h.embedAndRate(id, isGood)
	}

	// Return updated counts
	article, err := h.db.GetArticle(r.Context(), id)
	if err != nil || article == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]int{
		"likes":    article.Likes,
		"dislikes": article.Dislikes,
	}); err != nil {
		log.Printf("api: encode ratings: %v", err)
	}
}

// embedAndRate ensures the article is embedded in Qdrant and sets the vote payload.
func (h *Handler) embedAndRate(articleID int64, isGood bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	vote := 0
	if isGood {
		vote = 1
	}

	// Check if point already exists in Qdrant
	exists, err := h.qdrant.PointExists(ctx, articleID)
	if err != nil {
		log.Printf("api: qdrant check %d: %v", articleID, err)
	}

	if exists {
		// Just update the vote payload
		if err := h.qdrant.SetPayload(ctx, articleID, map[string]any{"vote": vote}); err != nil {
			log.Printf("api: qdrant set vote %d: %v", articleID, err)
		}
		return
	}

	// Need to embed first
	article, err := h.db.GetArticle(ctx, articleID)
	if err != nil || article == nil {
		log.Printf("api: get article %d for embedding: %v", articleID, err)
		return
	}

	text := article.Title + " " + article.Content
	vector, err := h.embedder.Embed(ctx, text)
	if err != nil {
		log.Printf("api: embed article %d: %v", articleID, err)
		return
	}

	payload := map[string]any{
		"article_id": articleID,
		"vote":       vote,
	}
	if err := h.qdrant.Upsert(ctx, articleID, vector, payload); err != nil {
		log.Printf("api: qdrant upsert %d: %v", articleID, err)
	}
}

// GET /api/news/recommended?limit=20
// Returns articles recommended based on user's like/dislike history.
func (h *Handler) getRecommended(w http.ResponseWriter, r *http.Request) {
	if h.qdrant == nil || h.embedder == nil {
		http.Error(w, `{"error":"recommendation system not configured"}`, http.StatusServiceUnavailable)
		return
	}

	limit := queryInt(r, "limit", 20)
	if limit < 1 || limit > 100 {
		limit = 20
	}

	ctx := r.Context()

	// Get liked and disliked article IDs from Qdrant
	likedIDs, err := h.qdrant.GetVotedIDs(ctx, 1)
	if err != nil {
		log.Printf("api: get liked IDs: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(likedIDs) == 0 {
		// No likes yet — return empty result
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(newsResponse{Articles: []articleJSON{}, HasMore: false})
		return
	}

	dislikedIDs, err := h.qdrant.GetVotedIDs(ctx, 0)
	if err != nil {
		log.Printf("api: get disliked IDs: %v", err)
		// Continue without negatives
		dislikedIDs = nil
	}

	recommendedIDs, _, err := h.qdrant.Recommend(ctx, likedIDs, dislikedIDs, limit)
	if err != nil {
		log.Printf("api: recommend: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if len(recommendedIDs) == 0 {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(newsResponse{Articles: []articleJSON{}, HasMore: false})
		return
	}

	// Fetch full articles from DB
	articles, err := h.db.GetArticlesByIDs(ctx, recommendedIDs)
	if err != nil {
		log.Printf("api: get articles by IDs: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := newsResponse{
		Articles: make([]articleJSON, 0, len(articles)),
		HasMore:  false,
	}
	for _, a := range articles {
		resp.Articles = append(resp.Articles, toArticleJSON(a))
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("api: encode recommended: %v", err)
	}
}

// POST /api/news/{id}/save — analyze the article with Gemini and save to Obsidian.
// Response: {"file":"inbox/2026-03-05-title.md"}
// Requires GEMINI_API_KEY and OBSIDIAN_VAULT_PATH to be configured.
func (h *Handler) saveArticle(w http.ResponseWriter, r *http.Request) {
	if h.gemini == nil {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"Gemini not configured — set GEMINI_API_KEY and OBSIDIAN_VAULT_PATH"}`, http.StatusServiceUnavailable)
		return
	}

	article, ok := h.fetchArticle(w, r)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()

	filePath, err := h.gemini.SaveArticleToObsidian(ctx, article.Title, article.Content, article.URL)
	if err != nil {
		log.Printf("api: saveArticle %d to obsidian: %v", article.ID, err)
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	// Return short path (strip container prefix for readability)
	shortPath := filePath
	if idx := strings.LastIndex(filePath, "/inbox/"); idx >= 0 {
		shortPath = "inbox/" + filePath[idx+7:]
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"file": shortPath}); err != nil {
		log.Printf("api: encode file path: %v", err)
	}
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// --- helpers ---

// fetchArticle parses {id} from path, loads the article, writes error responses on failure.
func (h *Handler) fetchArticle(w http.ResponseWriter, r *http.Request) (*db.Article, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return nil, false
	}
	article, err := h.db.GetArticle(r.Context(), id)
	if err != nil {
		log.Printf("api: GetArticle %d: %v", id, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	if article == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return nil, false
	}
	return article, true
}

// pathID extracts the {id} path value and writes a 400 on parse failure.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return 0, false
	}
	return id, true
}

func toArticleJSON(a db.Article) articleJSON {
	tags := a.Tags
	if tags == nil {
		tags = []string{}
	}
	return articleJSON{
		ID:        a.ID,
		SourceID:  a.SourceID,
		Title:     a.Title,
		AITitle:   a.AITitle,
		Summary:   a.Summary,
		Tags:      tags,
		Content:   a.Content,
		URL:       a.URL,
		ImageURL:  a.ImageURL,
		Source:    a.SourceName,
		Likes:     a.Likes,
		Dislikes:  a.Dislikes,
		PubDate:   a.PubDate,
		CreatedAt: a.CreatedAt,
	}
}

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
