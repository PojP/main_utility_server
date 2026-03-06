package bot

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"rss-reader/internal/ai"
	"rss-reader/internal/db"
)

const (
	videoWorkers    = 2            // concurrent Gemini video requests
	videoQueueCap   = 10           // max queued jobs
	pendingQueryTTL = 5 * time.Minute
	callbackNoQuery = "video:no_query"
)

type videoJobKind int

const (
	jobYouTube videoJobKind = iota
	jobTelegramVideo
)

type videoJob struct {
	kind     videoJobKind
	chatID   int64
	url      string // YouTube URL
	fileID   string // Telegram file ID
	filename string
	query    string // optional user query; "" = use default prompt
}

// pendingVideo holds a video job waiting for an optional text query from the user.
type pendingVideo struct {
	job       videoJob
	promptMsg int // message ID of the "what to find?" prompt (for deletion)
	expires   time.Time
}

type Bot struct {
	api        *tgbotapi.BotAPI
	db         *db.DB
	gemini     *ai.GeminiClient // nil if not configured
	videoQueue chan videoJob

	pendingMu sync.Mutex
	pending   map[int64]*pendingVideo // chatID → pending state
}

func New(token string, database *db.DB, gemini *ai.GeminiClient) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}
	log.Printf("bot: authorized as @%s", api.Self.UserName)
	return &Bot{
		api:        api,
		db:         database,
		gemini:     gemini,
		videoQueue: make(chan videoJob, videoQueueCap),
		pending:    make(map[int64]*pendingVideo),
	}, nil
}

func (b *Bot) Run() {
	for range videoWorkers {
		go b.videoWorker()
	}
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	for update := range b.api.GetUpdatesChan(u) {
		if update.CallbackQuery != nil {
			b.handleCallback(update.CallbackQuery)
			continue
		}
		if update.Message == nil {
			continue
		}
		b.handle(update.Message)
	}
}

func (b *Bot) videoWorker() {
	for job := range b.videoQueue {
		b.processVideoJob(job)
	}
}

func (b *Bot) enqueueVideo(job videoJob) {
	select {
	case b.videoQueue <- job:
	default:
		b.send(job.chatID, fmt.Sprintf("⏳ Очередь анализа занята (%d/%d). Попробуй через минуту.", len(b.videoQueue), videoQueueCap), false)
	}
}

// setPending stores a pending video job and asks the user for an optional query.
func (b *Bot) setPending(job videoJob) {
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("▶ Отправить без запроса", callbackNoQuery),
		),
	)
	msg := tgbotapi.NewMessage(job.chatID, "🎬 Видео получено. Напиши, что именно нужно найти или проанализировать — или нажми кнопку, чтобы получить стандартный конспект.")
	msg.ReplyMarkup = kb
	sent, err := b.api.Send(msg)
	if err != nil {
		log.Printf("bot: setPending send: %v", err)
	}

	b.pendingMu.Lock()
	b.pending[job.chatID] = &pendingVideo{
		job:       job,
		promptMsg: sent.MessageID,
		expires:   time.Now().Add(pendingQueryTTL),
	}
	b.pendingMu.Unlock()
}

// popPending returns and removes the pending state for a chat, or nil if none/expired.
func (b *Bot) popPending(chatID int64) *pendingVideo {
	b.pendingMu.Lock()
	defer b.pendingMu.Unlock()
	p := b.pending[chatID]
	if p == nil {
		return nil
	}
	delete(b.pending, chatID)
	if time.Now().After(p.expires) {
		return nil
	}
	return p
}

// deletePendingPrompt removes the "what to find?" prompt message.
func (b *Bot) deletePendingPrompt(chatID int64, msgID int) {
	b.api.Request(tgbotapi.NewDeleteMessage(chatID, msgID)) //nolint:errcheck
}

// handleCallback handles inline button presses.
func (b *Bot) handleCallback(cb *tgbotapi.CallbackQuery) {
	b.api.Request(tgbotapi.NewCallback(cb.ID, "")) //nolint:errcheck
	if cb.Data != callbackNoQuery {
		return
	}
	chatID := cb.Message.Chat.ID
	p := b.popPending(chatID)
	if p == nil {
		b.api.Request(tgbotapi.NewDeleteMessage(chatID, cb.Message.MessageID)) //nolint:errcheck
		return
	}
	b.deletePendingPrompt(chatID, p.promptMsg)
	b.enqueueVideo(p.job)
}

func (b *Bot) processVideoJob(job videoJob) {
	switch job.kind {
	case jobYouTube:
		b.send(job.chatID, "⏳ Анализирую YouTube-видео...", false)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		result, err := b.gemini.AnalyzeYouTube(ctx, job.url, job.query)
		if err != nil {
			b.send(job.chatID, fmt.Sprintf("Ошибка анализа: %v", err), false)
			return
		}
		b.saveAndReply(job.chatID, result, job.url, "video")

	case jobTelegramVideo:
		b.send(job.chatID, "⏳ Загружаю и анализирую видео...", false)
		data, err := b.downloadTelegramFile(job.fileID)
		if err != nil {
			b.send(job.chatID, fmt.Sprintf("Ошибка загрузки: %v", err), false)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		result, err := b.gemini.AnalyzeVideo(ctx, data, job.filename, job.query)
		if err != nil {
			b.send(job.chatID, fmt.Sprintf("Ошибка анализа: %v", err), false)
			return
		}
		b.saveAndReply(job.chatID, result, job.url, "video")
	}
}

func (b *Bot) SendNews(chatID int64, title, url string) {
	b.send(chatID, fmt.Sprintf("📰 *%s*\n%s", escapeMarkdown(title), url), true)
}

func (b *Bot) handle(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID

	// --- Media handling (requires Gemini) ---
	if b.gemini != nil {
		if msg.Photo != nil {
			b.flushPending(chatID)
			b.handlePhoto(msg)
			return
		}
		if msg.Video != nil {
			b.flushPending(chatID)
			b.handleVideo(msg)
			return
		}
		if msg.Document != nil && isVideoDocument(msg.Document) {
			b.flushPending(chatID)
			b.handleVideoDocument(msg)
			return
		}
		if msg.Voice != nil {
			b.flushPending(chatID)
			b.handleVoice(msg)
			return
		}
		if msg.Audio != nil {
			b.flushPending(chatID)
			b.handleAudio(msg)
			return
		}
	}

	text := msg.Text
	if text == "" {
		text = msg.Caption
	}
	if text == "" {
		return
	}

	isCommand := strings.HasPrefix(text, "/")

	// --- YouTube URL in plain message ---
	if b.gemini != nil && !isCommand {
		if ytURL := extractYouTubeURL(text); ytURL != "" {
			b.flushPending(chatID)
			b.handleYouTubeURL(chatID, ytURL)
			return
		}
	}

	// --- Command: flush pending first ---
	if isCommand {
		b.flushPending(chatID)
	}

	// --- Plain text while a video is pending → use as query ---
	if b.gemini != nil && !isCommand {
		if p := b.popPending(chatID); p != nil {
			b.deletePendingPrompt(chatID, p.promptMsg)
			p.job.query = strings.TrimSpace(text)
			b.enqueueVideo(p.job)
			return
		}
	}

	// --- Plain text → AI note ---
	if b.gemini != nil && !isCommand {
		b.handleNote(chatID, text)
		return
	}

	// --- Commands ---
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return
	}
	cmd := strings.SplitN(parts[0], "@", 2)[0]
	args := parts[1:]

	switch cmd {
	case "/start", "/help":
		b.send(chatID, b.buildHelpText(), false)
	case "/add":
		b.cmdAdd(chatID, args)
	case "/addchannel":
		b.cmdAddChannel(chatID, args)
	case "/list":
		b.cmdList(chatID)
	case "/remove":
		b.cmdRemove(chatID, args)
	case "/news":
		b.cmdNews(chatID)
	case "/analyze":
		b.cmdAnalyze(chatID, args)
	}
}

// flushPending enqueues the pending video (if any) without a query and clears the state.
func (b *Bot) flushPending(chatID int64) {
	p := b.popPending(chatID)
	if p == nil {
		return
	}
	b.deletePendingPrompt(chatID, p.promptMsg)
	b.enqueueVideo(p.job)
}

// --- Note handler ---

func (b *Bot) handleNote(chatID int64, text string) {
	b.send(chatID, "📝 Создаю заметку...", false)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result, err := b.gemini.ProcessNote(ctx, text)
	if err != nil {
		b.send(chatID, fmt.Sprintf("Ошибка: %v", err), false)
		return
	}
	b.saveAndReply(chatID, result, "", "note")
}

// --- Media handlers ---

func (b *Bot) handlePhoto(msg *tgbotapi.Message) {
	b.send(msg.Chat.ID, "⏳ Анализирую изображение...", false)

	// Take the largest photo size
	photo := msg.Photo[len(msg.Photo)-1]
	data, err := b.downloadTelegramFile(photo.FileID)
	if err != nil {
		b.send(msg.Chat.ID, fmt.Sprintf("Ошибка загрузки: %v", err), false)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	result, err := b.gemini.AnalyzeImage(ctx, data, "image/jpeg")
	if err != nil {
		b.send(msg.Chat.ID, fmt.Sprintf("Ошибка анализа: %v", err), false)
		return
	}

	b.saveAndReply(msg.Chat.ID, result, "", "image")
}

func (b *Bot) handleVoice(msg *tgbotapi.Message) {
	b.send(msg.Chat.ID, "🎙 Транскрибирую голосовое сообщение...", false)
	data, err := b.downloadTelegramFile(msg.Voice.FileID)
	if err != nil {
		b.send(msg.Chat.ID, fmt.Sprintf("Ошибка загрузки: %v", err), false)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := b.gemini.AnalyzeAudio(ctx, data, "audio/ogg", "voice.ogg")
	if err != nil {
		b.send(msg.Chat.ID, fmt.Sprintf("Ошибка анализа: %v", err), false)
		return
	}
	b.saveAndReply(msg.Chat.ID, result, "", "note")
}

func (b *Bot) handleAudio(msg *tgbotapi.Message) {
	b.send(msg.Chat.ID, "🎙 Транскрибирую аудио...", false)
	data, err := b.downloadTelegramFile(msg.Audio.FileID)
	if err != nil {
		b.send(msg.Chat.ID, fmt.Sprintf("Ошибка загрузки: %v", err), false)
		return
	}
	mimeType := msg.Audio.MimeType
	if mimeType == "" {
		mimeType = "audio/mpeg"
	}
	filename := msg.Audio.FileName
	if filename == "" {
		filename = "audio.mp3"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := b.gemini.AnalyzeAudio(ctx, data, mimeType, filename)
	if err != nil {
		b.send(msg.Chat.ID, fmt.Sprintf("Ошибка анализа: %v", err), false)
		return
	}
	b.saveAndReply(msg.Chat.ID, result, "", "note")
}

func (b *Bot) handleVideo(msg *tgbotapi.Message) {
	filename := msg.Video.FileName
	if filename == "" {
		filename = "video.mp4"
	}
	job := videoJob{kind: jobTelegramVideo, chatID: msg.Chat.ID, fileID: msg.Video.FileID, filename: filename}
	job.url = extractForwardURL(msg)
	b.setPending(job)
}

func (b *Bot) handleVideoDocument(msg *tgbotapi.Message) {
	job := videoJob{kind: jobTelegramVideo, chatID: msg.Chat.ID, fileID: msg.Document.FileID, filename: msg.Document.FileName}
	job.url = extractForwardURL(msg)
	b.setPending(job)
}

func (b *Bot) handleYouTubeURL(chatID int64, url string) {
	b.setPending(videoJob{kind: jobYouTube, chatID: chatID, url: url})
}

func (b *Bot) cmdAnalyze(chatID int64, args []string) {
	if len(args) == 0 {
		b.send(chatID, "Использование: /analyze <youtube-url>\nИли просто отправь YouTube-ссылку, фото или видео.", false)
		return
	}
	url := args[0]
	if ytURL := extractYouTubeURL(url); ytURL != "" {
		b.handleYouTubeURL(chatID, ytURL)
		return
	}
	b.send(chatID, "Ссылка не распознана как YouTube. Поддерживаются: youtube.com/watch?v=..., youtu.be/..., youtube.com/shorts/...", false)
}

// saveAndReply saves the Gemini analysis to Obsidian and replies to the user.
func (b *Bot) saveAndReply(chatID int64, content, sourceURL, mediaType string) {
	filePath, err := b.gemini.SaveToObsidian(content, sourceURL, mediaType)
	if err != nil {
		// Still show the result even if saving failed
		preview := truncate(content, 1000)
		b.send(chatID, fmt.Sprintf("Анализ готов, но не удалось сохранить в Obsidian: %v\n\n%s", err, preview), false)
		return
	}

	// Show file name (not full path for security)
	shortPath := filePath
	if idx := strings.LastIndex(filePath, "/inbox/"); idx >= 0 {
		shortPath = "inbox/" + filePath[idx+7:]
	}

	preview := truncate(content, 800)
	b.send(chatID, fmt.Sprintf("✅ Сохранено: `%s`\n\n%s", shortPath, preview), false)
}

// --- RSS/source commands ---

func (b *Bot) cmdAdd(chatID int64, args []string) {
	if len(args) < 1 {
		b.send(chatID, "Использование: /add <rss-url> [название]", false)
		return
	}
	url := args[0]
	name := ""
	if len(args) > 1 {
		name = strings.Join(args[1:], " ")
	}
	ctx := context.Background()
	inserted, err := b.db.AddSource(ctx, url, name, db.SourceRSS, chatID)
	if err != nil {
		b.send(chatID, fmt.Sprintf("Ошибка: %v", err), false)
		return
	}
	if !inserted {
		b.send(chatID, "Этот источник уже добавлен.", false)
		return
	}
	b.send(chatID, fmt.Sprintf("✅ RSS-источник добавлен:\n%s", url), false)
}

func (b *Bot) cmdAddChannel(chatID int64, args []string) {
	if len(args) < 1 {
		b.send(chatID, "Использование: /addchannel <ссылка или @username>", false)
		return
	}
	raw := args[0]
	name := ""
	if len(args) > 1 {
		name = strings.Join(args[1:], " ")
	}

	channelRef := normalizeChannelRef(raw)

	ctx := context.Background()
	inserted, err := b.db.AddSource(ctx, channelRef, name, db.SourceTelegram, chatID)
	if err != nil {
		b.send(chatID, fmt.Sprintf("Ошибка: %v", err), false)
		return
	}
	if !inserted {
		b.send(chatID, "Этот канал уже добавлен.", false)
		return
	}
	b.send(chatID, fmt.Sprintf("✅ Telegram-канал добавлен:\n%s\nЮзербот начнёт читать его при следующем обходе.", channelRef), false)
}

func (b *Bot) cmdList(chatID int64) {
	ctx := context.Background()
	sources, err := b.db.ListSources(ctx)
	if err != nil {
		b.send(chatID, fmt.Sprintf("Ошибка: %v", err), false)
		return
	}
	if len(sources) == 0 {
		b.send(chatID, "Нет добавленных источников.", false)
		return
	}
	var sb strings.Builder
	sb.WriteString("📋 *Источники:*\n\n")
	for _, s := range sources {
		name := s.Name
		if name == "" {
			name = s.URL
		}
		typeLabel := "RSS"
		if s.SourceType == db.SourceTelegram {
			typeLabel = "TG"
		}
		sb.WriteString(fmt.Sprintf("`[%d]` \\[%s\\] %s\n`%s`\n\n", s.ID, typeLabel, escapeMarkdown(name), s.URL))
	}
	b.send(chatID, sb.String(), true)
}

func (b *Bot) cmdRemove(chatID int64, args []string) {
	if len(args) < 1 {
		b.send(chatID, "Использование: /remove <id>", false)
		return
	}
	var id int64
	if _, err := fmt.Sscan(args[0], &id); err != nil {
		b.send(chatID, "ID должен быть числом. Используй /list чтобы посмотреть ID.", false)
		return
	}
	ctx := context.Background()
	if err := b.db.RemoveSource(ctx, id); err != nil {
		b.send(chatID, fmt.Sprintf("Ошибка: %v", err), false)
		return
	}
	b.send(chatID, fmt.Sprintf("✅ Источник #%d удалён.", id), false)
}

func (b *Bot) cmdNews(chatID int64) {
	ctx := context.Background()
	articles, err := b.db.LatestArticles(ctx, 10)
	if err != nil {
		b.send(chatID, fmt.Sprintf("Ошибка: %v", err), false)
		return
	}
	if len(articles) == 0 {
		b.send(chatID, "Новостей пока нет. Добавь источники через /add или /addchannel и подожди следующего обхода.", false)
		return
	}
	var sb strings.Builder
	sb.WriteString("📰 *Последние новости:*\n\n")
	for _, a := range articles {
		// Prefer AI title when available
		title := a.AITitle
		if title == "" {
			title = a.Title
		}
		if title == "" {
			title = truncate(a.Content, 80)
		}
		if a.URL != "" {
			sb.WriteString(fmt.Sprintf("• [%s](%s)\n", escapeMarkdown(title), a.URL))
		} else {
			sb.WriteString(fmt.Sprintf("• %s\n", escapeMarkdown(title)))
		}
	}
	b.send(chatID, sb.String(), true)
}

// --- Helpers ---

func (b *Bot) downloadTelegramFile(fileID string) ([]byte, error) {
	file, err := b.api.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return nil, fmt.Errorf("get file info: %w", err)
	}
	url := file.Link(b.api.Token)
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// extractForwardURL builds a t.me link if the message was forwarded from a channel.
func extractForwardURL(msg *tgbotapi.Message) string {
	if msg.ForwardFromChat != nil && msg.ForwardFromChat.Type == "channel" {
		username := msg.ForwardFromChat.UserName
		if username != "" {
			return fmt.Sprintf("https://t.me/%s/%d", username, msg.ForwardFromMessageID)
		}
		return fmt.Sprintf("https://t.me/c/%d/%d", msg.ForwardFromChat.ID, msg.ForwardFromMessageID)
	}
	return ""
}

func isVideoDocument(doc *tgbotapi.Document) bool {
	if doc == nil {
		return false
	}
	videoMimes := []string{"video/mp4", "video/quicktime", "video/x-msvideo", "video/webm", "video/x-matroska"}
	for _, m := range videoMimes {
		if doc.MimeType == m {
			return true
		}
	}
	// Also check by extension
	name := strings.ToLower(doc.FileName)
	for _, ext := range []string{".mp4", ".mov", ".avi", ".mkv", ".webm", ".3gp"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// youtubeRe matches common YouTube URL formats.
var youtubeRe = regexp.MustCompile(
	`https?://(?:(?:www|m)\.)?(?:youtube\.com/(?:watch[^\s]*|shorts/[^\s]+|embed/[^\s]+)|youtu\.be/[^\s]+)`,
)

func extractYouTubeURL(text string) string {
	return youtubeRe.FindString(text)
}

func (b *Bot) send(chatID int64, text string, markdown bool) {
	msg := tgbotapi.NewMessage(chatID, text)
	if markdown {
		msg.ParseMode = tgbotapi.ModeMarkdown
	}
	msg.DisableWebPagePreview = true
	if _, err := b.api.Send(msg); err != nil {
		log.Printf("bot: send: %v", err)
	}
}

func (b *Bot) buildHelpText() string {
	base := `*RSS-бот*

Команды:
/add <url> [название] — добавить RSS-источник
/addchannel <ссылка или @username> — добавить Telegram-канал
/list — список источников
/remove <id> — удалить источник по ID
/news — последние 10 новостей`

	if b.gemini != nil {
		base += `

*База знаний (Obsidian):*
/analyze <youtube-url> — анализ YouTube-видео
Или просто отправь:
• Любой текст — создаст структурированную заметку
• Голосовое сообщение — транскрибирует и оформит как заметку
• YouTube-ссылку — конспект видео
• Фото или скриншот — анализ изображения
• Видеофайл (до 20 МБ) — конспект видео

После видео/ссылки бот спросит, что именно найти — или нажми кнопку для стандартного конспекта.
Всё сохраняется в Obsidian как .md-файл.`
	}
	return base
}

func escapeMarkdown(s string) string {
	replacer := strings.NewReplacer("_", "\\_", "*", "\\*", "[", "\\[", "`", "\\`")
	return replacer.Replace(s)
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

func normalizeChannelRef(raw string) string {
	raw = strings.TrimSpace(raw)
	for _, prefix := range []string{"https://t.me/", "http://t.me/", "t.me/"} {
		if strings.HasPrefix(raw, prefix) {
			raw = raw[len(prefix):]
			break
		}
	}
	// Strip query params and trailing slashes
	if idx := strings.IndexAny(raw, "?#"); idx >= 0 {
		raw = raw[:idx]
	}
	raw = strings.TrimRight(raw, "/")
	// Handle s/ prefix (t.me/s/channel)
	raw = strings.TrimPrefix(raw, "s/")
	if strings.HasPrefix(raw, "joinchat/") {
		return "+" + raw[len("joinchat/"):]
	}
	raw = strings.TrimPrefix(raw, "@")
	return raw
}
