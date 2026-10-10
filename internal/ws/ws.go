// Package ws implements the /ws/upload-progress WebSocket endpoint and acts as
// the upload progress reporter for the files service.
package ws

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// idleTimeout matches the upstream WebSocket container timeout.
const idleTimeout = 5 * time.Minute

// Hub broadcasts upload progress messages to every connected client.
type Hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
}

type client struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{clients: map[*client]struct{}{}}
}

// Handler serves the /ws/upload-progress endpoint (origins allowed as upstream).
func (h *Hub) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: []string{"*"},
		})
		if err != nil {
			return
		}
		conn.SetReadLimit(8192)
		c := &client{conn: conn}
		h.add(c)
		defer func() {
			h.remove(c)
			conn.Close(websocket.StatusNormalClosure, "")
		}()
		for {
			ctx, cancel := context.WithTimeout(r.Context(), idleTimeout)
			typ, data, err := conn.Read(ctx)
			cancel()
			if err != nil {
				return
			}
			if typ != websocket.MessageText {
				continue
			}
			var msg struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			if msg.Type == "ping" {
				c.sendJSON(map[string]string{
					"type":      "pong",
					"timestamp": strconv.FormatInt(time.Now().UnixMilli(), 10),
				})
			}
		}
	}
}

func (h *Hub) add(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

// Broadcast sends a JSON message to every connected client.
func (h *Hub) Broadcast(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	h.mu.Lock()
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		c.sendRaw(data)
	}
}

func (c *client) sendJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	c.sendRaw(data)
}

func (c *client) sendRaw(data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		log.Printf("websocket write: %v", err)
	}
}

// UploadProgressMessage is broadcast during uploads.
type UploadProgressMessage struct {
	Type         string  `json:"type"`
	FileName     string  `json:"fileName"`
	Percentage   float64 `json:"percentage"`
	CurrentChunk int     `json:"currentChunk"`
	TotalChunks  int     `json:"totalChunks"`
}

// UploadCompleteMessage is broadcast when an upload finishes.
type UploadCompleteMessage struct {
	Type     string `json:"type"`
	FileName string `json:"fileName"`
}

// UploadErrorMessage is broadcast when an upload fails.
type UploadErrorMessage struct {
	Type     string `json:"type"`
	FileName string `json:"fileName"`
	Error    string `json:"error"`
}

// UploadProgress implements files.Reporter.
func (h *Hub) UploadProgress(name string, percentage float64, currentChunk, totalChunks int) {
	h.Broadcast(UploadProgressMessage{"upload_progress", name, percentage, currentChunk, totalChunks})
}

// UploadComplete implements files.Reporter.
func (h *Hub) UploadComplete(name string) {
	h.Broadcast(UploadCompleteMessage{"upload_complete", name})
}

// UploadError implements files.Reporter.
func (h *Hub) UploadError(name, err string) {
	h.Broadcast(UploadErrorMessage{"upload_error", name, err})
}
