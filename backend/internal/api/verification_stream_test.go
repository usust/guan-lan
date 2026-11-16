package api

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVerificationStreamRefreshesDeadlineAndStopsHeartbeat(t *testing.T) {
	router := gin.New()
	router.GET("/verify", func(c *gin.Context) {
		send, stop := verificationStream(context.Background(), c.Writer, 10*time.Millisecond)
		defer stop()
		send("progress", "running")
		time.Sleep(150 * time.Millisecond)
		stop() // Explicit stop and deferred stop are both safe.
		send("done", "complete", map[string]string{"id": "ready-instance"})
	})
	server := httptest.NewUnstartedServer(router)
	server.Config.WriteTimeout = 50 * time.Millisecond
	server.Start()
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/verify")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	heartbeats := 0
	finished := false
	for scanner.Scan() {
		var event struct {
			Type string
			Data struct {
				ID string `json:"id"`
			}
		}
		if err = json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		if finished {
			t.Fatal("event emitted after completion")
		}
		if event.Type == "heartbeat" {
			heartbeats++
		}
		if event.Type == "done" {
			if event.Data.ID != "ready-instance" {
				t.Fatal("completion instance payload missing")
			}
			finished = true
		}
	}
	if err = scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if heartbeats == 0 || !finished {
		t.Fatalf("stream stopped: heartbeats=%d finished=%t", heartbeats, finished)
	}
}
