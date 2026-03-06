// Package qdrant provides an HTTP client for Qdrant vector database.
package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const collectionName = "articles"

type Client struct {
	baseURL    string
	httpClient *http.Client
	vectorSize int
}

func New(baseURL string, vectorSize int) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{},
		vectorSize: vectorSize,
	}
}

// EnsureCollection creates the collection if it doesn't exist.
func (c *Client) EnsureCollection(ctx context.Context) error {
	// Check if collection exists
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/collections/%s", c.baseURL, collectionName), nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant: check collection: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}

	// Create collection
	body := map[string]any{
		"vectors": map[string]any{
			"size":     c.vectorSize,
			"distance": "Cosine",
		},
	}
	return c.put(ctx, fmt.Sprintf("/collections/%s", collectionName), body)
}

// Upsert inserts or updates a point in the collection.
func (c *Client) Upsert(ctx context.Context, id int64, vector []float32, payload map[string]any) error {
	body := map[string]any{
		"points": []map[string]any{
			{
				"id":      id,
				"vector":  vector,
				"payload": payload,
			},
		},
	}
	return c.put(ctx, fmt.Sprintf("/collections/%s/points", collectionName), body)
}

// SetPayload updates the payload of an existing point without changing the vector.
func (c *Client) SetPayload(ctx context.Context, id int64, payload map[string]any) error {
	body := map[string]any{
		"payload": payload,
		"points":  []int64{id},
	}
	return c.post(ctx, fmt.Sprintf("/collections/%s/points/payload", collectionName), body)
}

// Recommend returns article IDs recommended based on positive (liked) and negative (disliked) examples.
func (c *Client) Recommend(ctx context.Context, positiveIDs, negativeIDs []int64, limit int) ([]int64, []float32, error) {
	body := map[string]any{
		"positive": positiveIDs,
		"negative": negativeIDs,
		"limit":    limit,
		"filter": map[string]any{
			"must_not": []map[string]any{
				{"key": "vote", "match": map[string]any{"value": 1}},
				{"key": "vote", "match": map[string]any{"value": 0}},
			},
		},
	}
	respBody, err := c.postReturn(ctx, fmt.Sprintf("/collections/%s/points/recommend", collectionName), body)
	if err != nil {
		return nil, nil, err
	}

	var result struct {
		Result []struct {
			ID    int64   `json:"id"`
			Score float32 `json:"score"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, nil, fmt.Errorf("qdrant decode recommend: %w", err)
	}

	ids := make([]int64, len(result.Result))
	scores := make([]float32, len(result.Result))
	for i, r := range result.Result {
		ids[i] = r.ID
		scores[i] = r.Score
	}
	return ids, scores, nil
}

// GetVotedIDs returns article IDs that have a specific vote value.
func (c *Client) GetVotedIDs(ctx context.Context, vote int) ([]int64, error) {
	body := map[string]any{
		"filter": map[string]any{
			"must": []map[string]any{
				{"key": "vote", "match": map[string]any{"value": vote}},
			},
		},
		"limit":        1000,
		"with_payload": false,
		"with_vector":  false,
	}
	respBody, err := c.postReturn(ctx, fmt.Sprintf("/collections/%s/points/scroll", collectionName), body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Result struct {
			Points []struct {
				ID int64 `json:"id"`
			} `json:"points"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("qdrant decode scroll: %w", err)
	}

	ids := make([]int64, len(result.Result.Points))
	for i, p := range result.Result.Points {
		ids[i] = p.ID
	}
	return ids, nil
}

// PointExists checks if a point with the given ID exists.
func (c *Client) PointExists(ctx context.Context, id int64) (bool, error) {
	url := fmt.Sprintf("%s/collections/%s/points/%d", c.baseURL, collectionName, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

func (c *Client) put(ctx context.Context, path string, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant %s: status %d: %s", path, resp.StatusCode, string(respBody))
	}
	return nil
}

func (c *Client) post(ctx context.Context, path string, body any) error {
	_, err := c.postReturn(ctx, path, body)
	return err
}

func (c *Client) postReturn(ctx context.Context, path string, body any) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("qdrant %s: status %d: %s", path, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}
