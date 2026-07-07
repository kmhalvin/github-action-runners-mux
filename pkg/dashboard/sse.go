package dashboard

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
)

type SSEEvent struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

type SSEHub struct {
	clients map[chan SSEEvent]bool
	mutex   sync.RWMutex
}

func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients: make(map[chan SSEEvent]bool),
	}
}

func (h *SSEHub) Broadcast(eventType string, data interface{}) {
	h.mutex.RLock()
	defer h.mutex.RUnlock()
	
	event := SSEEvent{
		Type: eventType,
		Data: data,
	}

	for client := range h.clients {
		select {
		case client <- event:
		default:
			// Client is too slow or disconnected, ignore for now.
			// The HTTP handler will eventually close it.
		}
	}
}

func (h *SSEHub) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
			return
		}

		clientChan := make(chan SSEEvent, 100)
		
		h.mutex.Lock()
		h.clients[clientChan] = true
		h.mutex.Unlock()
		
		defer func() {
			h.mutex.Lock()
			delete(h.clients, clientChan)
			close(clientChan)
			h.mutex.Unlock()
		}()

		// Send initial connected event
		fmt.Fprintf(w, "event: connected\ndata: {}\n\n")
		flusher.Flush()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-clientChan:
				dataBytes, err := json.Marshal(event.Data)
				if err != nil {
					log.Printf("SSE JSON encode error: %v", err)
					continue
				}
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, dataBytes)
				flusher.Flush()
			}
		}
	}
}
