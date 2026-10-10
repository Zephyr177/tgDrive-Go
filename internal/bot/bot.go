// Package bot controls the live Telegram bot bound to one configs row.
package bot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Zephyr177/tgDrive-Go/internal/store"
	"github.com/Zephyr177/tgDrive-Go/internal/telegram"
)

// Controller owns the currently loaded bot connection.
type Controller struct {
	store   *store.Store
	apiURL  string // optional Bot API endpoint override (TGDRIVE_BOT_API_URL)
	mu      sync.Mutex
	api     *telegram.BotAPI
	name    string
	running atomic.Bool
}

// NewController returns a controller; the keep-alive task starts enabled.
func NewController(st *store.Store, apiURL string) *Controller {
	c := &Controller{store: st, apiURL: apiURL}
	c.running.Store(true)
	return c
}

// Load reads the named config row and re-points the live bot
// (upstream GET /api/config/{name}).
func (c *Controller) Load(name string) error {
	cfg, err := c.store.BotConfigByName(name)
	if err != nil {
		return errors.New("配置文件不存在")
	}
	token := strings.TrimSpace(cfg.Token)
	if token == "" {
		return errors.New("bot token未设置")
	}
	chatID, err := strconv.ParseInt(strings.TrimSpace(cfg.Target), 10, 64)
	if err != nil {
		return errors.New("获取bot token失败")
	}
	api, err := telegram.NewBotAPI(token, chatID, c.apiURL)
	if err != nil {
		return fmt.Errorf("获取bot token失败: %w", err)
	}
	c.mu.Lock()
	c.api = api
	c.name = name
	c.mu.Unlock()
	return nil
}

// AutoLoad initializes the bot at startup, preferring default/main config names.
func (c *Controller) AutoLoad() {
	configs, err := c.store.ListBotConfigs()
	if err != nil || len(configs) == 0 {
		log.Printf("bot: no configuration available for autoload")
		return
	}
	name := configs[0].Name
	for _, cfg := range configs {
		lower := strings.ToLower(cfg.Name)
		if strings.Contains(lower, "default") || strings.Contains(lower, "main") {
			name = cfg.Name
			break
		}
	}
	if err := c.Load(name); err != nil {
		log.Printf("bot: autoload %q failed: %v", name, err)
	}
}

// current returns the loaded bot API client.
func (c *Controller) current() (*telegram.BotAPI, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.api == nil {
		return nil, errors.New("bot token未设置")
	}
	return c.api, nil
}

// SendMessage sends text through the loaded bot.
func (c *Controller) SendMessage(text string) error {
	api, err := c.current()
	if err != nil {
		return err
	}
	return api.SendMessage(text)
}

// Start enables the keep-alive task.
func (c *Controller) Start() { c.running.Store(true) }

// Stop disables the keep-alive task.
func (c *Controller) Stop() { c.running.Store(false) }

// Status returns the upstream status text.
func (c *Controller) Status() string {
	if c.running.Load() {
		return "运行中"
	}
	return "已停止"
}

// ConfigText describes the keep-alive schedule (upstream /config endpoint).
func (c *Controller) ConfigText() string {
	return fmt.Sprintf("机器人保活任务：每天凌晨 2:00 自动发送保活消息，间隔 24 小时。\n当前状态：%s", c.Status())
}

// KeepAliveText is the message sent by the scheduled task.
func KeepAliveText() string {
	return fmt.Sprintf("tgDrive 保活消息 %s", time.Now().Format("2006-01-02 15:04:05"))
}

// RunScheduler sends the keep-alive message daily at 02:00 until ctx ends,
// mirroring the upstream cron "0 0 2 * * ?".
func (c *Controller) RunScheduler(ctx context.Context) {
	for {
		now := time.Now()
		next := time.Date(now.Year(), now.Month(), now.Day(), 2, 0, 0, 0, now.Location())
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}
		if !c.running.Load() {
			continue
		}
		if err := c.SendMessage(KeepAliveText()); err != nil {
			log.Printf("keep-alive: %v", err)
		}
	}
}

// Upload implements telegram.Storage by delegating to the loaded bot.
func (c *Controller) Upload(ctx context.Context, name string, r io.Reader, size int64) (string, error) {
	api, err := c.current()
	if err != nil {
		return "", err
	}
	return api.Upload(ctx, name, r, size)
}

// Open implements telegram.Storage by delegating to the loaded bot.
func (c *Controller) Open(ctx context.Context, fileID string) (*telegram.Blob, error) {
	api, err := c.current()
	if err != nil {
		return nil, err
	}
	return api.Open(ctx, fileID)
}

// Delete implements telegram.Storage by delegating to the loaded bot.
func (c *Controller) Delete(ctx context.Context, fileID string) error {
	api, err := c.current()
	if err != nil {
		return err
	}
	return api.Delete(ctx, fileID)
}
