package userbot

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"

	"rss-reader/internal/db"
	"rss-reader/internal/notify"
	"rss-reader/internal/processor"
	"rss-reader/internal/s3"
)

type Config struct {
	APIID       int
	APIHash     string
	Phone       string
	Password    string // 2FA password, optional
	SessionPath string
	Interval    time.Duration
	BotToken    string // for notifications
	NotifyChat  int64
}

type Userbot struct {
	cfg  Config
	db   *db.DB
	s3   *s3.Client
	proc *processor.Processor
}

func New(cfg Config, database *db.DB, s3client *s3.Client, proc *processor.Processor) *Userbot {
	return &Userbot{cfg: cfg, db: database, s3: s3client, proc: proc}
}

func (u *Userbot) Run(ctx context.Context) error {
	sessionStorage := &session.FileStorage{Path: u.cfg.SessionPath}

	client := telegram.NewClient(u.cfg.APIID, u.cfg.APIHash, telegram.Options{
		SessionStorage: sessionStorage,
	})

	return client.Run(ctx, func(ctx context.Context) error {
		if err := u.authenticate(ctx, client); err != nil {
			return fmt.Errorf("auth: %w", err)
		}

		log.Println("userbot: authenticated")

		api := client.API()
		ticker := time.NewTicker(u.cfg.Interval)
		defer ticker.Stop()

		u.poll(ctx, api)

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				u.poll(ctx, api)
			}
		}
	})
}

func (u *Userbot) authenticate(ctx context.Context, client *telegram.Client) error {
	flow := auth.NewFlow(
		termAuth{phone: u.cfg.Phone, password: u.cfg.Password},
		auth.SendCodeOptions{},
	)
	return client.Auth().IfNecessary(ctx, flow)
}

const channelConcurrency = 3 // max parallel channel requests to Telegram API

func (u *Userbot) poll(ctx context.Context, api *tg.Client) {
	sources, err := u.db.ListSourcesByType(ctx, db.SourceTelegram)
	if err != nil {
		log.Printf("userbot: list sources: %v", err)
		return
	}

	sem := make(chan struct{}, channelConcurrency)
	var wg sync.WaitGroup
	for _, src := range sources {
		wg.Add(1)
		sem <- struct{}{}
		go func(src db.Source) {
			defer wg.Done()
			defer func() { <-sem }()
			if err := u.processChannel(ctx, api, src); err != nil {
				log.Printf("userbot: channel %s: %v", src.URL, err)
			}
		}(src)
	}
	wg.Wait()
}

func (u *Userbot) processChannel(ctx context.Context, api *tg.Client, src db.Source) error {
	channelRef := src.URL
	if strings.HasPrefix(channelRef, "+") {
		return u.processInviteChannel(ctx, api, src, channelRef[1:])
	}
	return u.processPublicChannel(ctx, api, src, channelRef)
}

func (u *Userbot) processPublicChannel(ctx context.Context, api *tg.Client, src db.Source, username string) error {
	resolved, err := api.ContactsResolveUsername(ctx, username)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", username, err)
	}

	for _, chat := range resolved.Chats {
		channel, ok := chat.(*tg.Channel)
		if !ok {
			continue
		}
		inputPeer := &tg.InputPeerChannel{
			ChannelID:  channel.ID,
			AccessHash: channel.AccessHash,
		}
		if channel.Left {
			if _, err := api.ChannelsJoinChannel(ctx, &tg.InputChannel{
				ChannelID:  channel.ID,
				AccessHash: channel.AccessHash,
			}); err != nil {
				log.Printf("userbot: join %s: %v (will still try to read)", username, err)
			}
		}
		return u.readHistory(ctx, api, src, inputPeer, channel.Username, channel.ID)
	}

	return fmt.Errorf("no channel found for %s", username)
}

func (u *Userbot) processInviteChannel(ctx context.Context, api *tg.Client, src db.Source, hash string) error {
	invite, err := api.MessagesCheckChatInvite(ctx, hash)
	if err != nil {
		return fmt.Errorf("check invite %s: %w", hash, err)
	}

	switch inv := invite.(type) {
	case *tg.ChatInviteAlready:
		channel, ok := inv.Chat.(*tg.Channel)
		if !ok {
			return fmt.Errorf("invite chat is not a channel")
		}
		inputPeer := &tg.InputPeerChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}
		return u.readHistory(ctx, api, src, inputPeer, channel.Username, channel.ID)

	case *tg.ChatInvite:
		if inv.RequestNeeded {
			log.Printf("userbot: channel +%s requires admin approval, skipping", hash)
			return nil
		}
		updates, err := api.MessagesImportChatInvite(ctx, hash)
		if err != nil {
			return fmt.Errorf("import invite: %w", err)
		}
		return u.extractChannelFromUpdates(ctx, api, src, updates)

	case *tg.ChatInvitePeek:
		channel, ok := inv.Chat.(*tg.Channel)
		if !ok {
			return fmt.Errorf("peek: chat is not a channel")
		}
		inputPeer := &tg.InputPeerChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}
		return u.readHistory(ctx, api, src, inputPeer, channel.Username, channel.ID)

	default:
		return fmt.Errorf("unexpected invite type: %T", invite)
	}
}

func (u *Userbot) extractChannelFromUpdates(ctx context.Context, api *tg.Client, src db.Source, updates tg.UpdatesClass) error {
	upd, ok := updates.(*tg.Updates)
	if !ok {
		return fmt.Errorf("no channel in updates")
	}
	for _, chat := range upd.Chats {
		if channel, ok := chat.(*tg.Channel); ok {
			inputPeer := &tg.InputPeerChannel{ChannelID: channel.ID, AccessHash: channel.AccessHash}
			return u.readHistory(ctx, api, src, inputPeer, channel.Username, channel.ID)
		}
	}
	return fmt.Errorf("no channel in updates")
}

// readHistory fetches messages newer than src.LastTgMsgID, deduplicates albums,
// processes each post and updates the watermark.
func (u *Userbot) readHistory(ctx context.Context, api *tg.Client, src db.Source, peer *tg.InputPeerChannel, channelName string, channelID int64) error {
	limit := 50
	if src.LastTgMsgID > 0 {
		limit = 100
	}

	history, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:  peer,
		Limit: limit,
		MinID: int(src.LastTgMsgID), // only messages newer than the watermark
	})
	if err != nil {
		return fmt.Errorf("get history: %w", err)
	}

	var rawMsgs []tg.MessageClass
	switch h := history.(type) {
	case *tg.MessagesMessages:
		rawMsgs = h.Messages
	case *tg.MessagesMessagesSlice:
		rawMsgs = h.Messages
	case *tg.MessagesChannelMessages:
		rawMsgs = h.Messages
	default:
		return fmt.Errorf("unexpected history type: %T", history)
	}

	// Collect concrete messages and sort ascending (oldest first).
	var msgs []*tg.Message
	for _, m := range rawMsgs {
		if msg, ok := m.(*tg.Message); ok {
			msgs = append(msgs, msg)
		}
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })

	// Deduplicate album members: for each GroupedID keep only the message
	// that has text. If none have text, keep the first one.
	seenGroup := make(map[int64]bool)
	var dedupedMsgs []*tg.Message
	for _, msg := range msgs {
		if msg.GroupedID == 0 {
			dedupedMsgs = append(dedupedMsgs, msg)
			continue
		}
		if seenGroup[msg.GroupedID] {
			continue // already have a representative for this album
		}
		if msg.Message != "" {
			// This message has the caption — use it as representative.
			seenGroup[msg.GroupedID] = true
			dedupedMsgs = append(dedupedMsgs, msg)
		}
		// No text yet — defer: we'll pick this up when we see the one with text,
		// or fall back to the first after the loop.
	}
	// Second pass: add first-seen for groups that had no captioned message.
	for _, msg := range msgs {
		if msg.GroupedID != 0 && !seenGroup[msg.GroupedID] {
			seenGroup[msg.GroupedID] = true
			dedupedMsgs = append(dedupedMsgs, msg)
		}
	}
	sort.Slice(dedupedMsgs, func(i, j int) bool { return dedupedMsgs[i].ID < dedupedMsgs[j].ID })

	var maxMsgID int64 = src.LastTgMsgID
	newCount := 0

	for _, msg := range dedupedMsgs {
		if int64(msg.ID) > maxMsgID {
			maxMsgID = int64(msg.ID)
		}

		externalID := fmt.Sprintf("%d", msg.ID)

		// Build post URL. Private channels have no username → use numeric t.me/c/ format.
		var articleURL string
		if channelName != "" {
			articleURL = fmt.Sprintf("https://t.me/%s/%d", channelName, msg.ID)
		} else if channelID != 0 {
			articleURL = fmt.Sprintf("https://t.me/c/%d/%d", channelID, msg.ID)
		}

		imageURL := ""
		if u.s3 != nil {
			imageURL = u.downloadPhoto(ctx, api, msg, src.ID, externalID)
		}

		var pubDate *time.Time
		if msg.Date != 0 {
			t := time.Unix(int64(msg.Date), 0)
			pubDate = &t
		}

		title := extractTitle(msg.Message)
		id, result, err := u.proc.Process(ctx, src.ID, externalID, title, msg.Message, articleURL, imageURL, pubDate)
		if err != nil {
			log.Printf("userbot: process article: %v", err)
			continue
		}

		switch result {
		case processor.ResultNew:
			newCount++
			notify.SendTelegram(u.cfg.BotToken, u.cfg.NotifyChat, title, articleURL)
		case processor.ResultReplaced:
			log.Printf("userbot: article %d updated with richer content from %s", id, src.URL)
		}
	}

	if maxMsgID > src.LastTgMsgID {
		if err := u.db.UpdateSourceLastMsgID(ctx, src.ID, maxMsgID); err != nil {
			log.Printf("userbot: update last_msg_id for source %d: %v", src.ID, err)
		}
	}

	log.Printf("userbot: %s — %d messages fetched, %d new", src.URL, len(dedupedMsgs), newCount)
	return nil
}

func (u *Userbot) downloadPhoto(ctx context.Context, api *tg.Client, msg *tg.Message, sourceID int64, externalID string) string {
	if msg.Media == nil {
		return ""
	}
	mediaPhoto, ok := msg.Media.(*tg.MessageMediaPhoto)
	if !ok {
		return ""
	}
	photo, ok := mediaPhoto.Photo.(*tg.Photo)
	if !ok {
		return ""
	}

	var bestSize *tg.PhotoSize
	var bestPixels int
	for _, size := range photo.Sizes {
		if s, ok := size.(*tg.PhotoSize); ok {
			pixels := s.W * s.H
			if pixels > bestPixels {
				bestPixels = pixels
				bestSize = s
			}
		}
	}
	if bestSize == nil {
		return ""
	}

	location := &tg.InputPhotoFileLocation{
		ID:            photo.ID,
		AccessHash:    photo.AccessHash,
		FileReference: photo.FileReference,
		ThumbSize:     bestSize.Type,
	}

	d := downloader.NewDownloader()
	var data []byte
	w := &bytesWriter{data: &data}
	_, err := d.Download(api, location).Stream(ctx, w)
	if err != nil {
		log.Printf("userbot: download photo: %v", err)
		return ""
	}

	key := fmt.Sprintf("images/%d/%s.jpg", sourceID, externalID)
	url, err := u.s3.Upload(ctx, key, data, "image/jpeg")
	if err != nil {
		log.Printf("userbot: upload to s3: %v", err)
		return ""
	}
	return url
}

type bytesWriter struct {
	data *[]byte
}

func (w *bytesWriter) Write(p []byte) (int, error) {
	*w.data = append(*w.data, p...)
	return len(p), nil
}

func extractTitle(text string) string {
	if text == "" {
		return "(без текста)"
	}
	// Try to find a meaningful first line (skip empty lines, emoji-only lines, short garbage)
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Strip markdown bold/italic markers for cleaner titles
		line = strings.ReplaceAll(line, "**", "")
		line = strings.ReplaceAll(line, "__", "")
		line = strings.TrimSpace(line)
		// Skip lines that are too short to be meaningful titles (likely emoji or formatting)
		runes := []rune(line)
		if len(runes) < 4 {
			continue
		}
		if len(runes) > 100 {
			return string(runes[:100]) + "..."
		}
		return line
	}
	// Fallback: truncate full text
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > 100 {
		return string(runes[:100]) + "..."
	}
	if len(runes) == 0 {
		return "(без текста)"
	}
	return string(runes)
}

// termAuth implements auth.UserAuthenticator for terminal-based auth.
type termAuth struct {
	phone    string
	password string
}

func (a termAuth) Phone(_ context.Context) (string, error) {
	if a.phone != "" {
		return a.phone, nil
	}
	fmt.Print("Enter phone number: ")
	return readLine()
}

func (a termAuth) Password(_ context.Context) (string, error) {
	if a.password != "" {
		return a.password, nil
	}
	fmt.Print("Enter 2FA password (or press enter if none): ")
	return readLine()
}

func (a termAuth) AcceptTermsOfService(_ context.Context, _ tg.HelpTermsOfService) error {
	return nil
}

func (a termAuth) SignUp(_ context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, fmt.Errorf("sign up not supported")
}

func (a termAuth) Code(_ context.Context, _ *tg.AuthSentCode) (string, error) {
	fmt.Print("Enter auth code: ")
	return readLine()
}

func readLine() (string, error) {
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text()), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("EOF")
}
