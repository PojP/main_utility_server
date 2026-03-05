package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const (
	geminiBaseURL   = "https://generativelanguage.googleapis.com/v1beta"
	geminiUploadURL = "https://generativelanguage.googleapis.com/upload/v1beta"
)

// GeminiClient analyzes media via Google Gemini and saves results to Obsidian.
type GeminiClient struct {
	apiKey     string
	model      string
	vaultPath  string // Obsidian vault mount path
	httpClient *http.Client
}

// NewGeminiClient creates a Gemini client.
// model: e.g. "gemini-2.0-flash", vaultPath: local path to Obsidian vault (container side).
func NewGeminiClient(apiKey, model, vaultPath string) *GeminiClient {
	return &GeminiClient{
		apiKey:    apiKey,
		model:     model,
		vaultPath: vaultPath,
		httpClient: &http.Client{Timeout: 0}, // rely on context timeout
	}
}

// --- Gemini request/response types ---

type geminiReq struct {
	Contents []geminiContent `json:"contents"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text          string            `json:"text,omitempty"`
	FileData      *geminiFileRef    `json:"fileData,omitempty"`
	InlineData    *geminiInline     `json:"inlineData,omitempty"`
	VideoMetadata *geminiVideoMeta  `json:"videoMetadata,omitempty"`
}

type geminiVideoMeta struct {
	StartOffset *geminiDuration `json:"startOffset,omitempty"`
	EndOffset   *geminiDuration `json:"endOffset,omitempty"`
}

type geminiDuration struct {
	Seconds int `json:"seconds"`
}

type geminiFileRef struct {
	FileURI  string `json:"fileUri"`
	MimeType string `json:"mimeType"`
}

type geminiInline struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64-encoded
}

type geminiResp struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// buildVideoPrompt returns the analysis prompt. If query is non-empty it is used
// instead of the default structural prompt.
func buildVideoPrompt(query string) string {
	if query == "" {
		return videoAnalysisPrompt
	}
	return metaInstruction + fmt.Sprintf(`Проанализируй видео и подробно ответь на запрос пользователя. Структурируй ответ в Markdown.

Запрос: %s

Формат:
# <Заголовок, отражающий запрос>

## Ответ
<Подробный ответ на основе содержимого видео>

## Теги
#<тег1> #<тег2> #<тег3>`, query)
}

// AnalyzeYouTube analyzes a YouTube video by URL.
// query is an optional user question; pass "" to use the default structural prompt.
// For long videos (context limit exceeded) it automatically falls back to
// chunked analysis in 30-minute segments (up to 4 chunks = 2 hours).
func (g *GeminiClient) AnalyzeYouTube(ctx context.Context, url, query string) (string, error) {
	result, err := g.analyzeYouTubeSegment(ctx, url, 0, 0, query)
	if err == nil {
		return result, nil
	}
	if !isContextLengthError(err) {
		return "", err
	}

	// Context too long — split into 30-minute chunks.
	// Segments use a prompt without metaInstruction to avoid duplicates.
	const segmentSec = 30 * 60
	const maxSegments = 4
	var parts []string
	for i := range maxSegments {
		startSec := i * segmentSec
		endSec := startSec + segmentSec
		seg, err := g.analyzeYouTubeSegment(ctx, url, startSec, endSec, query)
		if err != nil {
			if len(parts) == 0 {
				return "", fmt.Errorf("анализ видео по частям не удался: %w", err)
			}
			break
		}
		// Strip any meta directives that appeared inside a segment.
		_, _, seg = parseSaveMeta(seg)
		parts = append(parts, fmt.Sprintf("## Часть %d (%d–%d мин)\n\n%s", i+1, startSec/60, endSec/60, seg))
	}
	combined := strings.Join(parts, "\n\n---\n\n")
	// Prepend meta so SaveToObsidian knows where to save the combined result.
	title := extractMarkdownTitle(parts[0])
	return fmt.Sprintf("ПАПКА: видео\nФАЙЛ: %s\n\n%s", title, combined), nil
}

// analyzeYouTubeSegment sends a single segment request.
// startSec=0, endSec=0 means "analyze the full video" (no videoMetadata).
func (g *GeminiClient) analyzeYouTubeSegment(ctx context.Context, url string, startSec, endSec int, query string) (string, error) {
	part := geminiPart{
		FileData: &geminiFileRef{FileURI: url, MimeType: "video/*"},
	}
	if endSec > 0 {
		part.VideoMetadata = &geminiVideoMeta{
			StartOffset: &geminiDuration{Seconds: startSec},
			EndOffset:   &geminiDuration{Seconds: endSec},
		}
	}
	req := geminiReq{
		Contents: []geminiContent{{
			Parts: []geminiPart{part, {Text: buildVideoPrompt(query)}},
		}},
	}
	return g.generate(ctx, req)
}

// isContextLengthError returns true when the Gemini error indicates the request
// exceeded the model's context window.
func isContextLengthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "context") ||
		strings.Contains(msg, "token") ||
		strings.Contains(msg, "too large") ||
		strings.Contains(msg, "exceeds") ||
		strings.Contains(msg, "quota")
}

// ProcessNote structures a user's raw text into a formatted Obsidian note.
// If the text contains instructions like "find X" or "add info about Y",
// Gemini uses its own knowledge to enrich the note.
func (g *GeminiClient) ProcessNote(ctx context.Context, text string) (string, error) {
	prompt := fmt.Sprintf("%s\n\n---\nТекст от пользователя:\n%s", notePrompt, text)
	req := geminiReq{
		Contents: []geminiContent{{
			Parts: []geminiPart{{Text: prompt}},
		}},
	}
	return g.generate(ctx, req)
}

// AnalyzeAudio uploads an audio file (voice note, mp3, etc.) to Gemini Files API,
// transcribes it and structures the content as an Obsidian note.
func (g *GeminiClient) AnalyzeAudio(ctx context.Context, data []byte, mimeType, filename string) (string, error) {
	fileURI, err := g.uploadFile(ctx, data, filename, mimeType)
	if err != nil {
		return "", fmt.Errorf("upload audio: %w", err)
	}
	req := geminiReq{
		Contents: []geminiContent{{
			Parts: []geminiPart{
				{FileData: &geminiFileRef{FileURI: fileURI, MimeType: mimeType}},
				{Text: audioNotePrompt},
			},
		}},
	}
	return g.generate(ctx, req)
}

// AnalyzeImage analyzes image bytes inline (base64).
func (g *GeminiClient) AnalyzeImage(ctx context.Context, data []byte, mimeType string) (string, error) {
	req := geminiReq{
		Contents: []geminiContent{{
			Parts: []geminiPart{
				{InlineData: &geminiInline{MimeType: mimeType, Data: base64.StdEncoding.EncodeToString(data)}},
				{Text: imageAnalysisPrompt},
			},
		}},
	}
	return g.generate(ctx, req)
}

// AnalyzeVideo uploads a video file to Gemini Files API and analyzes it.
// query is an optional user question; pass "" to use the default structural prompt.
func (g *GeminiClient) AnalyzeVideo(ctx context.Context, data []byte, filename, query string) (string, error) {
	mimeType := guessMimeType(filename)
	fileURI, err := g.uploadFile(ctx, data, filename, mimeType)
	if err != nil {
		return "", fmt.Errorf("upload video: %w", err)
	}
	req := geminiReq{
		Contents: []geminiContent{{
			Parts: []geminiPart{
				{FileData: &geminiFileRef{FileURI: fileURI, MimeType: mimeType}},
				{Text: buildVideoPrompt(query)},
			},
		}},
	}
	return g.generate(ctx, req)
}

// parseSaveMeta extracts ПАПКА and ФАЙЛ directives from the first lines of
// Gemini output and returns them along with the cleaned content (directives stripped).
// Falls back to ("inbox", "") when directives are absent.
func parseSaveMeta(content string) (folder, fileBase, clean string) {
	folder = "inbox"
	lines := strings.Split(content, "\n")
	remaining := lines
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(trimmed, "ПАПКА:"); ok {
			folder = strings.TrimSpace(v)
			remaining = append(lines[:i], lines[i+1:]...)
			lines = remaining
			break
		}
		// Stop scanning after first non-empty, non-directive line
		if trimmed != "" && !strings.HasPrefix(trimmed, "ФАЙЛ:") {
			break
		}
	}
	for i, line := range remaining {
		trimmed := strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(trimmed, "ФАЙЛ:"); ok {
			fileBase = strings.TrimSpace(v)
			remaining = append(remaining[:i], remaining[i+1:]...)
			break
		}
		if trimmed != "" && !strings.HasPrefix(trimmed, "ПАПКА:") {
			break
		}
	}
	// Strip leading blank lines left after removal
	clean = strings.TrimLeft(strings.Join(remaining, "\n"), "\n")
	// Sanitize folder name
	folder = strings.Trim(slugify(folder), "-")
	if folder == "" {
		folder = "inbox"
	}
	return folder, fileBase, clean
}

// SaveToObsidian saves Gemini analysis as an Obsidian-compatible .md file.
// Returns the saved file path (container-side).
func (g *GeminiClient) SaveToObsidian(content, sourceURL, mediaType string) (string, error) {
	if g.vaultPath == "" {
		return "", fmt.Errorf("OBSIDIAN_VAULT_PATH не настроен")
	}

	// Let Gemini choose folder and filename.
	folder, fileBase, content := parseSaveMeta(content)

	date := time.Now().Format("2006-01-02")
	var slug string
	if fileBase != "" {
		slug = slugify(fileBase)
	}
	if slug == "" {
		slug = slugify(extractMarkdownTitle(content))
	}

	destDir := filepath.Join(g.vaultPath, folder)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", destDir, err)
	}

	// Extract tags from Gemini response, then strip the ## Теги section
	contentTags := extractGeminiTags(content)
	cleanContent := removeTagsSection(content)

	allTags := append([]string{folder, mediaType + "-analysis"}, contentTags...)

	frontmatter := fmt.Sprintf("---\ntags: [%s]\ndate: %s\nsource: \"%s\"\ntype: %s-analysis\ncreated: %s\n---\n\n",
		strings.Join(allTags, ", "),
		date,
		sourceURL,
		mediaType,
		time.Now().Format(time.RFC3339),
	)

	filePath := filepath.Join(destDir, fmt.Sprintf("%s-%s.md", date, slug))
	// Avoid overwriting if file already exists
	if _, err := os.Stat(filePath); err == nil {
		filePath = filepath.Join(destDir, fmt.Sprintf("%s-%s-%d.md", date, slug, time.Now().UnixMilli()))
	}

	if err := os.WriteFile(filePath, []byte(frontmatter+cleanContent), 0644); err != nil {
		return "", err
	}
	return filePath, nil
}

// --- HTTP helpers ---

func (g *GeminiClient) generate(ctx context.Context, req geminiReq) (string, error) {
	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", geminiBaseURL, g.model, g.apiKey)
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := g.httpClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var gr geminiResp
	if err := json.Unmarshal(respBody, &gr); err != nil {
		return "", fmt.Errorf("gemini decode: %w (body: %.300s)", err, string(respBody))
	}
	if gr.Error != nil {
		return "", fmt.Errorf("gemini error %d: %s", gr.Error.Code, gr.Error.Message)
	}
	if len(gr.Candidates) == 0 {
		return "", fmt.Errorf("gemini: no candidates in response")
	}

	var sb strings.Builder
	for _, p := range gr.Candidates[0].Content.Parts {
		sb.WriteString(p.Text)
	}
	return sb.String(), nil
}

// uploadFile uploads data to Gemini Files API via multipart, then polls until ACTIVE.
func (g *GeminiClient) uploadFile(ctx context.Context, data []byte, filename, mimeType string) (string, error) {
	const boundary = "gemini_file_boundary_rss"

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.SetBoundary(boundary); err != nil {
		return "", fmt.Errorf("set boundary: %w", err)
	}

	// Part 1: JSON metadata
	metaH := make(textproto.MIMEHeader)
	metaH.Set("Content-Type", "application/json")
	metaPart, err := mw.CreatePart(metaH)
	if err != nil {
		return "", fmt.Errorf("create metadata part: %w", err)
	}
	if err := json.NewEncoder(metaPart).Encode(map[string]interface{}{
		"file": map[string]string{"display_name": filename},
	}); err != nil {
		return "", fmt.Errorf("encode metadata: %w", err)
	}

	// Part 2: file bytes
	fileH := make(textproto.MIMEHeader)
	fileH.Set("Content-Type", mimeType)
	filePart, err := mw.CreatePart(fileH)
	if err != nil {
		return "", fmt.Errorf("create file part: %w", err)
	}
	if _, err := filePart.Write(data); err != nil {
		return "", fmt.Errorf("write file data: %w", err)
	}
	if err := mw.Close(); err != nil {
		return "", fmt.Errorf("close multipart: %w", err)
	}

	uploadURL := fmt.Sprintf("%s/files?key=%s", geminiUploadURL, g.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &buf)
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("X-Goog-Upload-Protocol", "multipart")
	httpReq.Header.Set("Content-Type", "multipart/related; boundary="+boundary)

	resp, err := g.httpClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini upload: status %d: %.300s", resp.StatusCode, string(respBody))
	}

	var uploadResp struct {
		File struct {
			Name  string `json:"name"`
			URI   string `json:"uri"`
			State string `json:"state"`
		} `json:"file"`
	}
	if err := json.Unmarshal(respBody, &uploadResp); err != nil {
		return "", fmt.Errorf("gemini upload decode: %w", err)
	}

	if uploadResp.File.State == "ACTIVE" {
		return uploadResp.File.URI, nil
	}
	// Poll until ACTIVE
	return g.waitForFile(ctx, uploadResp.File.Name, uploadResp.File.URI)
}

// waitForFile polls Gemini Files API until the file becomes ACTIVE.
func (g *GeminiClient) waitForFile(ctx context.Context, fileName, fallbackURI string) (string, error) {
	pollURL := fmt.Sprintf("%s/%s?key=%s", geminiBaseURL, fileName, g.apiKey)
	for i := 0; i < 30; i++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
		resp, err := g.httpClient.Get(pollURL)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var fileResp struct {
			URI   string `json:"uri"`
			State string `json:"state"`
		}
		if json.Unmarshal(body, &fileResp) != nil {
			continue
		}
		switch fileResp.State {
		case "ACTIVE":
			return fileResp.URI, nil
		case "FAILED":
			return "", fmt.Errorf("gemini: file processing failed")
		}
	}
	// Return fallback URI and hope for the best
	if fallbackURI != "" {
		return fallbackURI, nil
	}
	return "", fmt.Errorf("gemini: file not ready after 90s")
}

// --- Markdown / Obsidian helpers ---

func extractMarkdownTitle(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	for _, line := range strings.Split(text, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			r := []rune(t)
			if len(r) > 80 {
				return string(r[:80])
			}
			return t
		}
	}
	return "analysis"
}

var hashtagRe = regexp.MustCompile(`#([а-яёА-ЯЁa-zA-Z0-9_\-]+)`)

func extractGeminiTags(content string) []string {
	inSection := false
	var tags []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "## Теги" || trimmed == "## Tags" {
			inSection = true
			continue
		}
		if inSection {
			if strings.HasPrefix(trimmed, "##") {
				break
			}
			for _, m := range hashtagRe.FindAllStringSubmatch(trimmed, -1) {
				tags = append(tags, m[1])
			}
		}
	}
	return tags
}

// removeTagsSection strips the ## Теги section from content
// (tags are moved to YAML frontmatter).
func removeTagsSection(content string) string {
	lines := strings.Split(content, "\n")
	result := make([]string, 0, len(lines))
	skip := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "## Теги" || trimmed == "## Tags" {
			skip = true
			continue
		}
		if skip && strings.HasPrefix(trimmed, "##") {
			skip = false
		}
		if !skip {
			result = append(result, line)
		}
	}
	return strings.TrimRight(strings.Join(result, "\n"), "\n")
}

// slugify converts a title to a filesystem-safe ASCII-like slug.
func slugify(title string) string {
	title = strings.ToLower(title)
	// Transliterate common Cyrillic letters
	title = cyrillic(title)
	var sb strings.Builder
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('-')
		}
	}
	slug := sb.String()
	// Collapse runs of dashes
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}
	slug = strings.Trim(slug, "-")
	r := []rune(slug)
	if len(r) > 60 {
		slug = string(r[:60])
	}
	if slug == "" {
		slug = "note"
	}
	return slug
}

func cyrillic(s string) string {
	r := strings.NewReplacer(
		"а", "a", "б", "b", "в", "v", "г", "g", "д", "d", "е", "e", "ё", "yo",
		"ж", "zh", "з", "z", "и", "i", "й", "y", "к", "k", "л", "l", "м", "m",
		"н", "n", "о", "o", "п", "p", "р", "r", "с", "s", "т", "t", "у", "u",
		"ф", "f", "х", "kh", "ц", "ts", "ч", "ch", "ш", "sh", "щ", "sch",
		"ъ", "", "ы", "y", "ь", "", "э", "e", "ю", "yu", "я", "ya",
	)
	return r.Replace(s)
}

// SaveArticleToObsidian analyzes a news article with Gemini and saves the structured
// knowledge note to the Obsidian vault. Returns the saved file path (container-side).
func (g *GeminiClient) SaveArticleToObsidian(ctx context.Context, title, content, url string) (string, error) {
	prompt := buildArticleNotesPrompt(title, content, url)
	req := geminiReq{
		Contents: []geminiContent{{
			Parts: []geminiPart{{Text: prompt}},
		}},
	}
	analysis, err := g.generate(ctx, req)
	if err != nil {
		return "", fmt.Errorf("gemini analyze article: %w", err)
	}
	return g.SaveToObsidian(analysis, url, "article")
}

// buildArticleNotesPrompt creates a prompt that turns a news article into a
// structured knowledge note: key facts, insights, actionable takeaways, and
// wiki-links for related topics.
func buildArticleNotesPrompt(title, content, url string) string {
	runes := []rune(content)
	if len(runes) > 6000 {
		content = string(runes[:6000]) + "…"
	}
	fragment := content
	if len([]rune(fragment)) > 400 {
		fragment = string([]rune(fragment)[:400]) + "…"
	}

	return metaInstruction + fmt.Sprintf(`Ты — ассистент по созданию базы знаний. Преврати новостную статью в структурированную заметку Obsidian.

Задача: не просто пересказать, а извлечь и структурировать знания, инсайты и практические выводы.

Формат (строго соблюдай Markdown-заголовки):

# <Точный заголовок: что произошло, кто, ключевой факт>

## Суть
<2–3 предложения: кто, что сделал, где, когда, почему важно>

## Ключевые факты и цифры
- <конкретный факт, цифра, имя, дата>
- <ещё факт>
(только то, что реально есть в тексте)

## Знания и инсайты
- **<Тезис>** — <почему это важно, какой вывод следует, связь с более широким контекстом>
(глубже простого пересказа — что это означает, какие следствия)

## Практические выводы
- <конкретное действие или вывод для читателя>
(оставь раздел только если есть что-то реально применимое)

## Связанные темы
- [[<тема или связанное событие для изучения>]]
(2–4 ссылки в формате Obsidian wiki-links, конкретные)

## Источник
**Заголовок:** %s
**URL:** %s
**Оригинал (фрагмент):**
> %s

## Теги
#<тег1> #<тег2> #<тег3>
(5–8 конкретных тегов: #россия #санкции #экономика — не #новости #статья)

---
Правила:
- Только факты из текста, никаких домыслов
- Инсайты должны быть глубже чем просто пересказ
- Раздел "Практические выводы" — только если реально есть что-то применимое
- Связанные темы — конкретные, не абстрактные`, title, url, fragment)
}

func guessMimeType(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".mp4":
		return "video/mp4"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".3gp":
		return "video/3gpp"
	default:
		return "video/mp4"
	}
}

// --- Prompts ---

// metaInstruction is prepended to every content prompt so Gemini always
// specifies the target folder and filename for SaveToObsidian.
const metaInstruction = `ВАЖНО: Самая первая строка ответа — папка, вторая — название файла. Без отступов, строго этот формат:
ПАПКА: <одно из: inbox, идеи, заметки, видео, голос, изображения, новости, задачи, ресурсы, обучение>
ФАЙЛ: <3–6 слов на русском или латинице, описывающих суть>

Затем сам контент.

`

const videoAnalysisPrompt = metaInstruction + `Ты — ассистент для создания базы знаний. Проанализируй видео и составь подробный структурированный конспект на русском языке.

Формат (строго соблюдай Markdown-заголовки):

# <Точное название темы или главный вопрос видео — конкретно, не обобщённо>

## Суть
<2–4 предложения: о чём видео, кто автор/спикер если упоминается, почему это важно>

## Ключевые мысли
- **<Тезис>** — <краткое пояснение или пример>
- **<Тезис>** — <краткое пояснение или пример>
(перечисли ВСЕ важные идеи и утверждения из видео)

## Лайфхаки и практические советы
- <Конкретный совет: что сделать, как применить, зачем>
(оставь раздел только если в видео есть практические советы)

## Важные факты, цифры, исследования
- <Факт / цифра / название исследования с контекстом>
(оставь раздел только если упоминаются конкретные данные)

## Инструменты и ресурсы
- **<Название>** — <для чего используется, где взять>
(оставь раздел только если что-то рекомендуется)

## Выводы
<Что главное запомнить из видео. Как это применить на практике. 2–5 предложений.>

## Теги
#<тема> #<подтема> #<область>
(5–10 тегов на русском без пробелов, максимально конкретных: #продуктивность, #тайм-менеджмент, #привычки — а не #видео, #контент)

---
Правила:
- Пиши только то, что реально было сказано в видео
- Никакой воды, шаблонных фраз и общих рассуждений
- Если раздел не применим — пропусти его`

const notePrompt = metaInstruction + `Ты — ассистент по созданию базы знаний. Обработай текст и создай структурированную заметку для Obsidian на русском языке.

Правила обработки:
1. Если текст — идея, мысль или заметка — оформи структурированно
2. Если в тексте есть инструкции "найди X", "добавь информацию о Y", "что такое Z" — используй свои знания чтобы дополнить заметку этой информацией
3. Если текст — список дел или задачи — оформи как чеклист с [ ]
4. Исправь очевидные ошибки и улучши формулировки, но не меняй суть
5. Если текст уже хорошо написан — сохрани его почти дословно, просто добавь структуру

Формат:
# <Заголовок, отражающий суть>

## Содержание
<Основное содержание — структурированно>

(добавляй другие секции по необходимости: ## Задачи, ## Факты, ## Связанные темы, ## Вопросы)

## Теги
#<тег1> #<тег2> #<тег3>
(3–7 конкретных тегов)`

const audioNotePrompt = metaInstruction + `Ты — ассистент по созданию базы знаний. Прослушай аудио и выполни два шага:

1. Транскрибируй речь дословно (исправь только явные оговорки)
2. На основе транскрипции создай структурированную заметку для Obsidian

Правила:
- Если говорящий описывает идею — оформи как концептуальную заметку
- Если перечисляет задачи — оформи как чеклист
- Если есть инструкции вроде "найди X" или "добавь инфо о Y" — используй свои знания для дополнения
- Сохрани все имена, цифры, конкретные детали

Формат:

# <Заголовок, отражающий суть аудио>

## Транскрипция
<Точная транскрипция речи>

## Структурированная заметка
<Основное содержание — структурированно, с секциями по необходимости>

## Теги
#<тег1> #<тег2> #<тег3>`

const imageAnalysisPrompt = metaInstruction + `Проанализируй изображение и составь заметку для базы знаний на русском языке.

Если изображение содержит текст, советы, инфографику, схему, таблицу или обучающий материал:
- Полностью извлеки и структурируй всю полезную информацию
- Сохрани все числа, шаги, списки, ключевые термины
- Если текст частично нечитаем — укажи это

Если изображение — скриншот интерфейса, кода или результатов:
- Опиши что показано и зачем это важно

Если обычное фото:
- Опиши что на нём и выдели всё интересное или полезное

Формат:

# <Тема или точное описание содержимого>

## Содержание
<Основное содержание — структурированно, с сохранением иерархии если есть>

## Ключевые факты и выводы
- <конкретный факт, совет или вывод>

## Теги
#<тег1> #<тег2> #<тег3>
(3–7 тегов на русском, конкретных)`
