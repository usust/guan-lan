package runtime_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"huashan/internal/competition"
	"huashan/internal/database"
	"huashan/internal/environment"
	runner "huashan/internal/runtime"
)

// Opt-in: uses only a caller-selected local archive and newly labeled resources.
// HUASHAN_RUNTIME_PACKAGE=/absolute/environment.tar go test ./internal/runtime -run TestDockerLifecycle -v
func TestDockerLifecycle(t *testing.T) {
	packagePath := os.Getenv("HUASHAN_RUNTIME_PACKAGE")
	if packagePath == "" {
		t.Skip("set HUASHAN_RUNTIME_PACKAGE for real Docker lifecycle test")
	}
	root := t.TempDir()
	for _, dir := range []string{"uploads/packages", "temp"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := environment.Inspect(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("%064d.tar", 1)
	if err = os.WriteFile(filepath.Join(root, "uploads/packages", name), data, 0600); err != nil {
		t.Fatal(err)
	}
	platform := fmt.Sprintf("runtime-test-%d", time.Now().UnixNano())
	db, err := database.Open(filepath.Join(root, "test.db"), platform)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, user := range []string{"admin", "alice", "bob"} {
		role := "PLAYER"
		if user == "admin" {
			role = "ADMIN"
		}
		if _, err = db.Exec("INSERT INTO users VALUES(?,?,?,'hash',?,'ACTIVE',0)", user, user, user, role); err != nil {
			t.Fatal(err)
		}
	}
	manager, err := runner.NewManager(db, root, platform, os.Getenv("DOCKER_HOST"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	service := &competition.Service{DB: db}
	x := competition.Set{Title: "Docker integration", StartsAt: time.Now().Add(-time.Minute).UnixMilli(), EndsAt: time.Now().Add(time.Hour).UnixMilli(), Questions: []competition.Question{{Title: "Web", Difficulty: "EASY", Subtasks: []competition.Subtask{{Title: "Flag", Points: 100, Flag: "flag{integration}"}}}}}
	id, err := service.Save(ctx, "admin", x)
	if err != nil {
		t.Fatal(err)
	}
	x, err = service.Get(ctx, id, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	q := x.Questions[0].ID
	if err = service.AttachPackage(ctx, "admin", id, q, x.Version, manifest, name, fmt.Sprintf("%x", sha256.Sum256(data))); err != nil {
		t.Fatal(err)
	}
	x, err = service.Get(ctx, id, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.Verify(ctx, q, x.Version); err != nil {
		t.Fatal("real verification", err)
	}
	x, err = service.Get(ctx, id, "admin", true)
	if err != nil || !x.Questions[0].RuntimeReady {
		t.Fatal("verification not persisted", err)
	}
	service.RuntimeEngineID, err = manager.Engine.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Transition(ctx, "admin", id, "PUBLISHED", x.Version); err != nil {
		t.Fatal("verified publication", err)
	}
	t.Cleanup(func() {
		for _, user := range []string{"alice", "bob"} {
			if err := manager.Stop(context.Background(), user, q); err != nil {
				t.Error("cleanup", err)
			}
		}
	})
	alice, err := manager.Start(ctx, "alice", q)
	if err != nil {
		t.Fatal("start alice", err)
	}
	repeat, err := manager.Start(ctx, "alice", q)
	if err != nil || repeat.ID != alice.ID {
		t.Fatal("idempotency", err)
	}
	bob, err := manager.Start(ctx, "bob", q)
	if err != nil {
		t.Fatal("start bob", err)
	}
	if alice.ID == bob.ID || len(alice.Endpoints) == 0 || len(bob.Endpoints) == 0 || alice.Endpoints[0].Address == bob.Endpoints[0].Address {
		t.Fatal("not isolated", alice, bob)
	}
	for _, x := range []runner.Instance{alice, bob} {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get("http://" + x.Endpoints[0].Address + "/")
		if err != nil {
			t.Fatal("real endpoint", err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatal("endpoint HTTP", resp.StatusCode)
		}
	}
	if err = manager.Stop(ctx, "alice", q); err != nil {
		t.Fatal(err)
	}
	stopped, err := manager.Status(ctx, "alice", q)
	if err != nil || stopped.State != "STOPPED" {
		t.Fatal("stop", stopped, err)
	}
	if _, err = db.Exec("UPDATE runtime_instances SET expires_at=0 WHERE id=?", bob.ID); err != nil {
		t.Fatal(err)
	}
	if err = manager.Reconcile(ctx); err != nil {
		t.Fatal("expiry", err)
	}
	stopped, err = manager.Status(ctx, "bob", q)
	if err != nil || stopped.State != "STOPPED" {
		t.Fatal("expired resources remain", stopped, err)
	}
	t.Log("real Docker: imported, verified, published, started two independent environments, HTTP reached both, stopped and expired without leftovers")
}
