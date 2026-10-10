package telegram

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// botAPIAttempts is the upstream SendDocument retry count (3 tries, 1s/2s backoff).
const botAPIAttempts = 3

// BotAPI stores files in a Telegram chat via the Bot API. apiURL optionally
// overrides the endpoint (e.g. a local Bot API server); "" keeps the official
// endpoint. Large files are chunked by the caller below the 20 MB limit.
type BotAPI struct {
	api    *tgbotapi.BotAPI
	chatID int64
	http   *http.Client
}

// NewBotAPI connects to the Bot API. apiURL overrides the endpoint template
// ("%s" placeholders) or base URL; "" keeps the official endpoint.
func NewBotAPI(token string, chatID int64, apiURL string) (*BotAPI, error) {
	var (
		api *tgbotapi.BotAPI
		err error
	)
	if apiURL == "" {
		api, err = tgbotapi.NewBotAPI(token)
	} else {
		tmpl := apiURL
		if !strings.Contains(tmpl, "%s") {
			tmpl = strings.TrimSuffix(tmpl, "/") + "/bot%s/%s"
		}
		api, err = tgbotapi.NewBotAPIWithAPIEndpoint(token, tmpl)
	}
	if err != nil {
		return nil, fmt.Errorf("connect bot api: %w", err)
	}
	return &BotAPI{api: api, chatID: chatID, http: http.DefaultClient}, nil
}

// SendMessage sends a plain text message to the configured chat.
func (b *BotAPI) SendMessage(text string) error {
	_, err := b.api.Send(tgbotapi.NewMessage(b.chatID, text))
	return err
}

// Upload sends the file as a document and returns its Telegram file id.
// The body is buffered once and retried with exponential backoff.
func (b *BotAPI) Upload(_ context.Context, name string, r io.Reader, _ int64) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read upload data: %w", err)
	}
	var lastErr error
	for attempt := range botAPIAttempts {
		if attempt > 0 {
			time.Sleep(time.Duration(1<<(attempt-1)) * time.Second)
		}
		msg, err := b.api.Send(tgbotapi.NewDocument(b.chatID,
			tgbotapi.FileReader{Name: name, Reader: bytes.NewReader(data)}))
		if err != nil {
			lastErr = err
			continue
		}
		if msg.Document == nil {
			lastErr = fmt.Errorf("response has no document")
			continue
		}
		return msg.Document.FileID, nil
	}
	return "", fmt.Errorf("upload to telegram: %w", lastErr)
}

// Open streams a stored document by file id.
func (b *BotAPI) Open(ctx context.Context, fileID string) (*Blob, error) {
	file, err := b.api.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return nil, fmt.Errorf("get telegram file: %w", err)
	}
	url, err := b.api.GetFileDirectURL(file.FilePath)
	if err != nil {
		return nil, fmt.Errorf("get telegram file url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch telegram file: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("fetch telegram file: status %s", resp.Status)
	}
	return &Blob{Reader: resp.Body, Name: path.Base(file.FilePath), Size: int64(file.FileSize)}, nil
}

// Delete is a no-op: the Bot API cannot remove chat documents.
func (b *BotAPI) Delete(context.Context, string) error { return nil }
