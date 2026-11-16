package runtime

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestProbeUnixSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "hs-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "d.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skip(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Version":"27.0.0","Os":"linux","Arch":"arm64"}`))
	})}
	go server.Serve(listener)
	defer server.Close()
	result := Probe(context.Background(), "unix://"+socket)
	if !result.Available || result.Architecture != "arm64" || result.OS != "linux" {
		t.Fatalf("unexpected probe: %+v", result)
	}
}
func TestProbeFailures(t *testing.T) {
	for _, endpoint := range []string{"tcp://localhost:2375", "unix:///nonexistent-huashan.sock"} {
		result := Probe(context.Background(), endpoint)
		if result.Available || result.Error == "" {
			t.Fatalf("unexpected success: %+v", result)
		}
	}
}

func TestResourceUsage(t *testing.T) {
	dir, err := os.MkdirTemp("", "hs-res-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "d.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Skip(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/info":
			w.Write([]byte(`{"Containers":3,"ContainersRunning":2,"MemTotal":4096,"NCPU":32}`))
		case "/containers/json":
			w.Write([]byte(`[{"Id":"a"},{"Id":"b"}]`))
		case "/containers/a/stats", "/containers/b/stats":
			w.Write([]byte(`{"memory_stats":{"usage":512}}`))
		default:
			http.NotFound(w, r)
		}
	})}
	go server.Serve(listener)
	defer server.Close()
	result := ResourceUsage(context.Background(), "unix://"+socket)
	if !result.Available || result.Containers != 3 || result.Running != 2 || result.MemoryUsed != 1024 || result.MemoryTotal != 4096 || result.CPUs != 32 {
		t.Fatalf("unexpected stats: %+v", result)
	}
}
