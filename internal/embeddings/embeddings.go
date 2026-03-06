// Package embeddings provides text embedding via Gemini API.
package embeddings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"
	DefaultModel  = "text-embedding-004"
	VectorSize    = 768
)

type Client struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

func New(apiKey, model string) *Client {
	if model == "" {
		model = DefaultModel
	}
	return &Client{
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{},
	}
}

type embedReq struct {
	Model   string       `json:"model"`
	Content embedContent `json:"content"`
}

type embedContent struct {
	Parts []embedPart `json:"parts"`
}

type embedPart struct {
	Text string `json:"text"`
}

type embedResp struct {
	Embedding *struct {
		Values []float32 `json:"values"`
	} `json:"embedding"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Embed returns a vector embedding for the given text.
func (c *Client) Embed(ctx context.Context, text string) ([]float32, error) {
	// Truncate to ~8000 chars to stay within model limits
	runes := []rune(text)
	if len(runes) > 8000 {
		text = string(runes[:8000])
	}

	req := embedReq{
		Model:   fmt.Sprintf("models/%s", c.model),
		Content: embedContent{Parts: []embedPart{{Text: text}}},
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/models/%s:embedContent?key=%s", geminiBaseURL, c.model, c.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var er embedResp
	if err := json.Unmarshal(respBody, &er); err != nil {
		return nil, fmt.Errorf("embed decode: %w (body: %.200s)", err, string(respBody))
	}
	if er.Error != nil {
		return nil, fmt.Errorf("embed error %d: %s", er.Error.Code, er.Error.Message)
	}
	if er.Embedding == nil || len(er.Embedding.Values) == 0 {
		return nil, fmt.Errorf("embed: empty response")
	}
	return er.Embedding.Values, nil
}
