package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Heartbeats keep long imports/startups observable even when no step completes.
// Serialize writes and join the worker before the handler returns.
func verificationStream(ctx context.Context, writer http.ResponseWriter, interval time.Duration) (func(string, string, ...any), func()) {
	var mu sync.Mutex
	send := func(kind, message string, data ...any) {
		mu.Lock()
		defer mu.Unlock()
		_ = http.NewResponseController(writer).SetWriteDeadline(time.Now().Add(2 * time.Minute))
		event := map[string]any{"type": kind, "message": message}
		if len(data) > 0 {
			event["data"] = data[0]
		}
		_ = json.NewEncoder(writer).Encode(event)
		if f, ok := writer.(http.Flusher); ok {
			f.Flush()
		}
	}
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				send("heartbeat", "验收仍在运行")
			}
		}
	}()
	var once sync.Once
	return send, func() { once.Do(func() { close(stop); <-done }) }
}
