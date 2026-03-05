// Package ai contains clients for OpenRouter and Gemini APIs.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultRPM is the default requests-per-minute limit for OpenRouter free tier.
const defaultRPM = 15

const openRouterURL = "https://openrouter.ai/api/v1/chat/completions"

// OpenRouterClient calls OpenRouter (OpenAI-compatible API).
type OpenRouterClient struct {
	apiKey     string
	model      string
	httpClient *http.Client
	tokens     chan struct{} // rate limiter tokens
}

// NewOpenRouterClient creates a client.
// model example: "qwen/qwen-2.5-72b-instruct:free"
func NewOpenRouterClient(apiKey, model string) *OpenRouterClient {
	interval := time.Minute / time.Duration(defaultRPM)
	tokens := make(chan struct{}, 1)
	tokens <- struct{}{} // seed with one token so first request is immediate
	go func() {
		ticker := time.NewTicker(interval)
		for range ticker.C {
			select {
			case tokens <- struct{}{}:
			default: // channel full, drop token
			}
		}
	}()
	return &OpenRouterClient{
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: 90 * time.Second},
		tokens:     tokens,
	}
}

type orRequest struct {
	Model    string      `json:"model"`
	Messages []orMessage `json:"messages"`
}

type orMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type orResponse struct {
	Choices []struct {
		Message orMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// EnrichArticle generates an AI title, 2-3 sentence summary, and tags.
// Returns ("", "", nil, nil) when content is too short to enrich.
func (c *OpenRouterClient) EnrichArticle(ctx context.Context, title, content string) (aiTitle, summary string, tags []string, err error) {
	if len([]rune(title+content)) < 80 {
		return "", "", nil, nil
	}
	result, err := c.complete(ctx, buildEnrichPrompt(title, content))
	if err != nil {
		return "", "", nil, err
	}
	aiTitle, summary, tags = parseEnrichResponse(result)
	return aiTitle, summary, tags, nil
}

// buildEnrichPrompt creates the prompt for news title + summary + tags generation.
func buildEnrichPrompt(title, content string) string {
	// Cap content to ~5000 runes to stay within model context
	runes := []rune(content)
	if len(runes) > 5000 {
		content = string(runes[:5000]) + "…"
	}

	return fmt.Sprintf(`Ты — опытный редактор новостного агрегатора. Выполни все три задания.

━━ ЗАДАНИЕ 1: ЗАГОЛОВОК ━━
Создай заголовок, который точно и конкретно отвечает на вопрос "что случилось".

Правила:
• Конкретика: имена людей/компаний, страны, цифры, конкретные действия
• Глагол в действительном залоге: "Россия ввела санкции", а не "введены санкции"
• Никаких вводных фраз: "По данным", "Стало известно", "Эксперты сообщили"
• Никаких оценочных слов: "скандальный", "резонансный", "шокирующий"
• Длина: 6–12 слов

━━ ЗАДАНИЕ 2: КРАТКОЕ ИЗЛОЖЕНИЕ ━━
Перескажи суть новости в 2–3 предложениях.

Правила:
• Структура: кто + что сделал + где/когда + каков результат/зачем
• Числа, даты, имена — обязательно включи, если есть
• Только факты из текста, никаких домыслов и оценок
• Длина: 60–150 слов

━━ ЗАДАНИЕ 3: ТЕГИ ━━
Назначь 1–4 тега из строго фиксированного списка.

Допустимые теги (только они, никаких других):
политика, экономика, война, технологии, ии, наука, космос, безопасность, энергетика, общество, россия, мир

Правила:
• Выбирай только из списка выше — никаких собственных тегов
• Строчные буквы, без символа #
• Перечисли через запятую
• Если новость про Россию — добавь тег "россия"; про международные события — "мир"
• Тег "ии" только для новостей про искусственный интеллект и нейросети

━━ ФОРМАТ ОТВЕТА (строго, без отступлений) ━━
ЗАГОЛОВОК: <текст заголовка>
КРАТКОЕ: <текст изложения>
ТЕГИ: <тег1>, <тег2>, <тег3>

━━ ИСХОДНАЯ НОВОСТЬ ━━
Заголовок: %s
Текст: %s`, title, content)
}

// parseEnrichResponse extracts ЗАГОЛОВОК, КРАТКОЕ and ТЕГИ from the model response.
func parseEnrichResponse(text string) (aiTitle, summary string, tags []string) {
	const titleMark = "ЗАГОЛОВОК:"
	const briefMark = "КРАТКОЕ:"
	const tagsMark = "ТЕГИ:"

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, titleMark); ok && aiTitle == "" {
			aiTitle = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, briefMark); ok && summary == "" {
			summary = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, tagsMark); ok && len(tags) == 0 {
			tags = parseTags(v)
		}
	}

	// Fallback: scan full text (model may split across lines)
	if summary == "" {
		if idx := strings.Index(text, briefMark); idx >= 0 {
			tail := strings.TrimSpace(text[idx+len(briefMark):])
			if end := strings.Index(tail, titleMark); end >= 0 {
				tail = tail[:end]
			}
			if end := strings.Index(tail, tagsMark); end >= 0 {
				tail = tail[:end]
			}
			summary = strings.TrimSpace(tail)
		}
	}
	if aiTitle == "" {
		if idx := strings.Index(text, titleMark); idx >= 0 {
			line := text[idx+len(titleMark):]
			if nl := strings.Index(line, "\n"); nl >= 0 {
				line = line[:nl]
			}
			aiTitle = strings.TrimSpace(line)
		}
	}
	if len(tags) == 0 {
		if idx := strings.Index(text, tagsMark); idx >= 0 {
			line := text[idx+len(tagsMark):]
			if nl := strings.Index(line, "\n"); nl >= 0 {
				line = line[:nl]
			}
			tags = parseTags(line)
		}
	}

	return aiTitle, summary, tags
}

// parseTags splits a comma-separated tags string, trims spaces and # prefix.
func parseTags(raw string) []string {
	var result []string
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		t = strings.TrimPrefix(t, "#")
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" {
			result = append(result, t)
		}
	}
	return result
}

func (c *OpenRouterClient) complete(ctx context.Context, prompt string) (string, error) {
	body, err := json.Marshal(orRequest{
		Model:    c.model,
		Messages: []orMessage{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", err
	}

	const maxRetries = 3
	backoff := 10 * time.Second

	for attempt := range maxRetries {
		// Wait for a rate-limit token before each attempt.
		select {
		case <-c.tokens:
		case <-ctx.Done():
			return "", ctx.Err()
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, openRouterURL, bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return "", err
		}
		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return "", err
		}

		// Retry on HTTP 429
		if resp.StatusCode == http.StatusTooManyRequests {
			if attempt < maxRetries-1 {
				select {
				case <-time.After(backoff):
					backoff *= 2
					continue
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			return "", fmt.Errorf("openrouter: rate limit exceeded after %d retries", maxRetries)
		}

		var orResp orResponse
		if err := json.Unmarshal(respBody, &orResp); err != nil {
			return "", fmt.Errorf("openrouter decode: %w (body: %.200s)", err, string(respBody))
		}
		if orResp.Error != nil {
			msg := orResp.Error.Message
			if strings.Contains(strings.ToLower(msg), "rate limit") && attempt < maxRetries-1 {
				select {
				case <-time.After(backoff):
					backoff *= 2
					continue
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			return "", fmt.Errorf("openrouter: %s", msg)
		}
		if len(orResp.Choices) == 0 {
			return "", fmt.Errorf("openrouter: empty response")
		}
		return orResp.Choices[0].Message.Content, nil
	}
	return "", fmt.Errorf("openrouter: all retries exhausted")
}
