package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"huashan/internal/database"
	"huashan/internal/environment"
)

type sharedFixture struct {
	manager *Manager
	expires int64
	groups  map[string][]string
	states  map[string]string
	created []string
}

func newSharedFixture(t *testing.T) *sharedFixture {
	t.Helper()
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "test.db"), "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users VALUES('alice','alice','Alice','hash','PLAYER','ACTIVE',0)`)
	exec(`INSERT INTO users VALUES('bob','bob','Bob','hash','PLAYER','ACTIVE',0)`)
	expires := time.Now().Add(30 * time.Minute).UnixMilli()
	exec(`INSERT INTO challenge_sets(id,title,description,status,starts_at,ends_at,version,created_at,updated_at) VALUES('set','test','','PUBLISHED',0,?,1,0,0)`, time.Now().Add(2*time.Hour).UnixMilli())
	manifest := environment.Manifest{FormatVersion: 1, Version: "1", Architecture: "amd64", Images: []environment.Image{{Key: "image", File: "images/image.tar", SHA256: strings.Repeat("a", 64)}}, Services: []environment.Service{
		{Key: "web", Image: "image", Ports: []environment.Port{{Port: 53, Protocol: "udp", Public: true}}},
		{Key: "other", Image: "image", Ports: []environment.Port{{Port: 54, Protocol: "udp", Public: true}}},
		{Key: "app", Image: "image", DependsOn: []string{"db"}}, {Key: "db", Image: "image"},
	}}
	raw, _ := json.Marshal(manifest)
	for _, q := range []string{"parent", "different"} {
		exec(`INSERT INTO challenges(id,set_id,title,description,category,difficulty,position,environment_json,package_path,package_sha256) VALUES(?,'set',?,'','Web','EASY',0,?,?,?)`, q, q, string(raw), strings.Repeat("b", 64)+".tar", strings.Repeat("b", 64))
		exec(`INSERT INTO runtime_verifications VALUES(?,?,?,'engine',0)`, q, strings.Repeat("b", 64), string(raw))
	}
	for _, sub := range []struct{ id, parent, keys string }{
		{"a", "parent", `["web"]`}, {"b", "parent", `["web"]`}, {"multi", "parent", `["web","other"]`}, {"other", "parent", `["other"]`},
		{"foreign", "different", `["web"]`}, {"app", "parent", `["app"]`}, {"db", "parent", `["db"]`},
	} {
		exec(`INSERT INTO subtasks(id,challenge_id,title,content,points,service_key,flag_hash,position) VALUES(?,?,?,'',100,'','hash',0)`, sub.id, sub.parent, sub.id)
		exec(`INSERT INTO subtask_resources VALUES(?,'[]',?)`, sub.id, sub.keys)
	}
	exec(`INSERT INTO runtime_instances VALUES('old','alice','a','RUNNING',?,?)`, `[{"service":"web","port":53,"protocol":"udp","address":"play.test:30001"}]`, expires)
	f := &sharedFixture{expires: expires, groups: map[string][]string{"old": {"web"}}, states: map[string]string{}}
	m := &Manager{DB: db, Root: root, Platform: "test", PublicHost: "play.test", slots: make(chan struct{}, 1)}
	f.manager = m
	imageID := "sha256:" + strings.Repeat("1", 64)
	if err := rememberImage(filepath.Join(root, "temp"), "engine", "amd64", manifest.Images[0].SHA256, imageID); err != nil {
		t.Fatal(err)
	}
	type createdContainer struct {
		host           json.RawMessage
		service, group string
	}
	configs := map[string]createdContainer{}
	m.Engine = testEngine(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/info":
			fmt.Fprint(w, `{"ID":"engine","OSType":"linux"}`)
		case strings.HasPrefix(r.URL.Path, "/images/"):
			fmt.Fprintf(w, `{"Id":%q,"Architecture":"amd64","Os":"linux"}`, imageID)
		case r.Method == "POST" && r.URL.Path == "/networks/create":
			fmt.Fprint(w, `{"Id":"net"}`)
		case r.URL.Path == "/networks/net" && r.Method == "GET":
			fmt.Fprint(w, `{"Internal":false,"Driver":"bridge","Labels":{"huashan.platform":"test"},"Options":{"com.docker.network.bridge.gateway_mode_ipv4":"nat"}}`)
		case r.URL.Path == "/containers/create":
			var config struct {
				HostConfig json.RawMessage
				Hostname   string
				Labels     map[string]string
			}
			if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
				t.Error(err)
				return
			}
			id := "new-" + config.Hostname
			configs[id] = createdContainer{config.HostConfig, config.Hostname, config.Labels["huashan.instance"]}
			f.created = append(f.created, config.Hostname)
			f.groups[config.Labels["huashan.instance"]] = append(f.groups[config.Labels["huashan.instance"]], config.Hostname)
			fmt.Fprintf(w, `{"Id":%q}`, id)
		case r.URL.Path == "/containers/json":
			var filters map[string][]string
			if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters); err != nil {
				t.Error(err)
				return
			}
			group := ""
			for _, label := range filters["label"] {
				if strings.HasPrefix(label, "huashan.instance=") {
					group = strings.TrimPrefix(label, "huashan.instance=")
				}
			}
			containers := []map[string]string{}
			for _, key := range f.groups[group] {
				state := "running"
				if f.states[group] != "" {
					state = f.states[group]
				}
				containers = append(containers, map[string]string{"Id": group + "-" + key, "State": state})
			}
			json.NewEncoder(w).Encode(containers)
		case strings.HasSuffix(r.URL.Path, "/start"):
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/json"):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/containers/"), "/json")
			config := configs[id]
			port := "53/udp"
			if config.service == "other" {
				port = "54/udp"
			}
			json.NewEncoder(w).Encode(map[string]any{"State": map[string]any{"Running": true}, "HostConfig": config.host, "NetworkSettings": map[string]any{"Networks": map[string]any{"net": map[string]string{"NetworkID": "net"}}, "Ports": map[string]any{port: []map[string]string{{"HostIp": "0.0.0.0", "HostPort": "32001"}}}}})
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/containers/"):
			for group, keys := range f.groups {
				for _, key := range keys {
					if strings.TrimPrefix(r.URL.Path, "/containers/") == group+"-"+key {
						delete(f.groups, group)
					}
				}
			}
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/networks":
			fmt.Fprint(w, `[]`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.Error(w, "unexpected", 500)
		}
	})
	return f
}

func TestSharedSubtaskStatusAndStart(t *testing.T) {
	f := newSharedFixture(t)
	m := f.manager
	ctx := context.Background()
	first, err := m.Status(ctx, "alice", "a")
	if err != nil || first.ID != "old" || first.ExpiresAt != f.expires {
		t.Fatalf("first status: %+v %v", first, err)
	}
	for i := 0; i < 2; i++ {
		hidden, err := m.Status(ctx, "alice", "b")
		if err != nil || hidden.State != "STOPPED" || hidden.ID != "" {
			t.Fatalf("unopened subtask became visible: %+v %v", hidden, err)
		}
	}
	if err := m.Stop(ctx, "alice", "b"); err != nil {
		t.Fatal(err)
	}
	stillRunning, err := m.Status(ctx, "alice", "a")
	if err != nil || stillRunning.ID != "old" {
		t.Fatalf("stopping unopened subtask affected source: %+v %v", stillRunning, err)
	}
	// Reuse succeeds even when both per-player slots are already occupied.
	if _, err := m.DB.Exec(`INSERT INTO runtime_instances VALUES('unrelated','alice','foreign','RUNNING','[]',?)`, f.expires); err != nil {
		t.Fatal(err)
	}
	x, err := m.Start(ctx, "alice", "b")
	if err != nil || x.ID != "old" || x.ExpiresAt != f.expires || len(f.created) != 0 {
		t.Fatalf("reuse: %+v %v created=%v", x, err, f.created)
	}
	synced, err := m.Status(ctx, "alice", "b")
	if err != nil || synced.ID != x.ID || synced.ExpiresAt != x.ExpiresAt {
		t.Fatalf("explicit reuse not persisted: %+v %v", synced, err)
	}
	restarted := &Manager{DB: m.DB, Engine: m.Engine, Root: m.Root, Platform: m.Platform, PublicHost: m.PublicHost}
	persisted, err := restarted.Status(ctx, "alice", "b")
	if err != nil || persisted.ID != x.ID || persisted.ExpiresAt != x.ExpiresAt {
		t.Fatalf("binding lost across manager restart: %+v %v", persisted, err)
	}
	var count int
	if err := m.DB.QueryRow(`SELECT count(*) FROM runtime_instances`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("shared status created a lease: %d %v", count, err)
	}
	if err := m.Stop(ctx, "alice", "b"); err != nil {
		t.Fatal(err)
	}
	if err := m.DB.QueryRow(`SELECT count(*) FROM runtime_bindings`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("bindings not removed with instance: %d %v", count, err)
	}
	for _, target := range []string{"a", "b"} {
		x, err := m.Status(ctx, "alice", target)
		if err != nil || x.State != "STOPPED" {
			t.Fatalf("stop sync %s: %+v %v", target, x, err)
		}
	}
}

func TestSharedStatusRequiresCompleteHealthyGroup(t *testing.T) {
	for _, scenario := range []string{"partial", "different user", "different parent", "expired", "dead", "split groups", "dependency missing"} {
		t.Run(scenario, func(t *testing.T) {
			f := newSharedFixture(t)
			m := f.manager
			user, target := "alice", "multi"
			switch scenario {
			case "different user":
				user, target = "bob", "b"
			case "different parent":
				target = "foreign"
			case "expired":
				target = "b"
				if _, err := m.DB.Exec(`UPDATE runtime_instances SET expires_at=0`); err != nil {
					t.Fatal(err)
				}
			case "dead":
				target = "b"
				f.states["old"] = "exited"
			case "split groups":
				f.groups["second"] = []string{"other"}
				if _, err := m.DB.Exec(`INSERT INTO runtime_instances VALUES('second','alice','other','RUNNING','[]',?)`, f.expires+10000); err != nil {
					t.Fatal(err)
				}
			case "dependency missing":
				target = "app"
				if _, err := m.DB.Exec(`UPDATE runtime_instances SET challenge_id='db'`); err != nil {
					t.Fatal(err)
				}
				f.groups["old"] = []string{"db"}
			}
			x, err := m.reusableInstance(context.Background(), user, target)
			if err != nil || x.State != "STOPPED" || x.ID != "" {
				t.Fatalf("incorrect reuse: %+v %v", x, err)
			}
		})
	}
}

func TestPartialOverlapStartsEntireNewGroup(t *testing.T) {
	f := newSharedFixture(t)
	m := f.manager
	ctx := context.Background()
	x, err := m.Start(ctx, "alice", "multi")
	if err != nil {
		t.Fatal(err)
	}
	if x.ID == "old" || len(f.created) != 2 || f.created[0] != "web" || f.created[1] != "other" || len(x.Endpoints) != 2 {
		t.Fatalf("partial reuse: %+v created=%v", x, f.created)
	}
	if x.ExpiresAt <= f.expires {
		t.Fatal("new group inherited old expiry")
	}
	var expires int64
	if err := m.DB.QueryRow(`SELECT expires_at FROM runtime_instances WHERE id=?`, x.ID).Scan(&expires); err != nil || expires != x.ExpiresAt {
		t.Fatalf("group expiry mismatch: %d %v", expires, err)
	}
	// A subset may share this group, but only its own endpoints are exposed.
	hidden, err := m.Status(ctx, "alice", "other")
	if err != nil || hidden.State != "STOPPED" {
		t.Fatalf("subset visible without start: %+v %v", hidden, err)
	}
	subset, err := m.Start(ctx, "alice", "other")
	if err != nil || subset.ID != x.ID || subset.ExpiresAt != x.ExpiresAt || len(subset.Endpoints) != 1 || subset.Endpoints[0].Service != "other" {
		t.Fatalf("subset sync: %+v %v", subset, err)
	}
	old, err := m.Status(ctx, "alice", "a")
	if err != nil || old.ID != "old" || old.ExpiresAt != f.expires {
		t.Fatalf("old group replaced: %+v %v", old, err)
	}
	again, err := m.Start(ctx, "alice", "multi")
	if err != nil || again.ID != x.ID || len(f.created) != 2 {
		t.Fatalf("repeat created more containers: %+v %v", again, err)
	}
}
