package api

import (
	"bytes"
	"context"
	"huashan/internal/auth"
	"huashan/internal/database"
	"huashan/internal/storage"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSystemReset(t *testing.T) {
	path, _ := filepath.EvalSymlinks(t.TempDir())
	root, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	db, err := database.Open(root.DatabasePath(), root.PlatformID)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := auth.New(db)
	for _, name := range []string{"admin", "player"} {
		role := "PLAYER"
		if name == "admin" {
			role = "ADMIN"
		}
		if _, err = service.CreateUser(context.Background(), "", name, name, "test-password-123", role, name == "admin"); err != nil {
			t.Fatal(err)
		}
	}
	_, token, csrf, err := service.Login(context.Background(), "admin", "test-password-123")
	if err != nil {
		t.Fatal(err)
	}
	_, player, pc, _ := service.Login(context.Background(), "player", "test-password-123")
	handler := New(service, root, Config{DockerEndpoint: "unix:///nonexistent"})
	call := func(session, csrf, body string) int {
		req := httptest.NewRequest("POST", "/api/v1/admin/system/reset", bytes.NewBufferString(body))
		req.AddCookie(&http.Cookie{Name: "huashan_session", Value: session})
		req.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}
	for _, tc := range []struct {
		token, csrf, body string
		code              int
	}{
		{"", "", `{"confirmation":"重置系统"}`, 401}, {player, pc, `{"confirmation":"重置系统"}`, 403}, {token, "", `{"confirmation":"重置系统"}`, 403}, {token, csrf, `{}`, 400},
	} {
		if code := call(tc.token, tc.csrf, tc.body); code != tc.code {
			t.Fatalf("got %d want %d", code, tc.code)
		}
	}
	if _, err = db.Exec(`INSERT INTO challenge_sets VALUES('set','测试','说明','DRAFT',1,2,1,1,1); INSERT INTO knowledge_points(name,parent_name) VALUES('root',NULL),('child','root');
 INSERT INTO challenges(id,set_id,title,description,category,difficulty,position,environment_json,tags_json) VALUES('question','set','题目','说明','WEB','EASY',0,'{}','["root"]');
 INSERT INTO subtasks(id,challenge_id,title,content,points,service_key,flag_hash,position) VALUES('subtask','question','子题','内容',100,'','hash',0);
 INSERT INTO submissions(id,user_id,subtask_id,correct,created_at) SELECT 'submission',id,'subtask',1,1 FROM users WHERE username='player';
 INSERT INTO solves(user_id,subtask_id,submission_id,points,solved_at) SELECT id,'subtask','submission',100,1 FROM users WHERE username='player'`); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, "uploads/attachments/test.txt")
	if err = os.WriteFile(file, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"confirmation":"重置系统"}`, `{"confirmation":"重置系统","groups":["unknown"]}`, `{"confirmation":"重置系统","groups":["content"]}`} {
		if code := call(token, csrf, body); code != 400 {
			t.Fatalf("invalid groups status %d", code)
		}
	}
	if code := call(token, csrf, `{"confirmation":"重置系统","groups":["taxonomy"]}`); code != 200 {
		t.Fatalf("taxonomy reset status %d", code)
	}
	for table, want := range map[string]int{"users": 2, "challenge_sets": 1, "knowledge_points": 0, "question_types": 0, "difficulties": 0} {
		var n int
		if err = db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != want {
			t.Fatalf("selective reset %s: got %d want %d err %v", table, n, want, err)
		}
	}
	if _, err = os.Stat(file); err != nil {
		t.Fatal("unselected attachment deleted", err)
	}

	var category, difficulty, tags string
	if err = db.QueryRow("SELECT category,difficulty,tags_json FROM challenges WHERE id='question'").Scan(&category, &difficulty, &tags); err != nil || category != "" || difficulty != "" || tags != "[]" {
		t.Fatal("question labels not cleared", err, category, difficulty, tags)
	}
	var version int
	db.QueryRow("SELECT version FROM challenge_sets WHERE id='set'").Scan(&version)
	if version != 2 {
		t.Fatal("editor version not invalidated", version)
	}
	if code := call(token, csrf, `{"confirmation":"重置系统","groups":["results"]}`); code != 200 {
		t.Fatalf("results reset status %d", code)
	}
	for table, want := range map[string]int{"submissions": 0, "solves": 0, "users": 2, "challenges": 1, "sessions": 2} {
		var n int
		if err = db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != want {
			t.Fatalf("results reset %s: got %d want %d err %v", table, n, want, err)
		}
	}
	for _, group := range []string{"branding", "logs", "backups"} {
		target := filepath.Join(path, group, "test.txt")
		if group == "branding" {
			target = filepath.Join(path, "config/platform.json")
		}
		if err = os.WriteFile(target, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
		if code := call(token, csrf, `{"confirmation":"重置系统","groups":["`+group+`"]}`); code != 200 {
			t.Fatalf("%s reset status %d", group, code)
		}
		if _, err = os.Stat(target); !os.IsNotExist(err) {
			t.Fatalf("%s file retained", group)
		}
		if _, err = os.Stat(file); err != nil {
			t.Fatal("unselected attachment deleted", err)
		}
	}
	// Removing users preserves content and classifications when those groups are not selected.
	if _, err = db.Exec("INSERT INTO question_types(name) VALUES('保留类型')"); err != nil {
		t.Fatal(err)
	}
	if code := call(token, csrf, `{"confirmation":"重置系统","groups":["users","results","runtime"]}`); code != 200 {
		t.Fatalf("users reset status %d", code)
	}
	for table, want := range map[string]int{"users": 1, "challenges": 1, "question_types": 1, "sessions": 0} {
		var n int
		if err = db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != want {
			t.Fatalf("users reset %s: got %d want %d err %v", table, n, want, err)
		}
	}
	_, token, csrf, err = service.Login(context.Background(), "admin", "test-password-123")
	if err != nil {
		t.Fatal(err)
	}

	if code := call(token, csrf, `{"confirmation":"重置系统","groups":["content","taxonomy","results","users","runtime","branding","logs","backups"]}`); code != 200 {
		t.Fatalf("reset status %d", code)
	}
	for _, table := range []string{"sessions", "challenge_sets", "knowledge_points", "question_types", "difficulties", "audit_logs"} {
		var n int
		if err = db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: count %d err %v", table, n, err)
		}
	}
	var n int
	db.QueryRow("SELECT count(*) FROM users").Scan(&n)
	if n != 1 {
		t.Fatal("unexpected users", n)
	}
	if _, err = os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("attachment retained")
	}
	if _, _, _, err = service.Login(context.Background(), "admin", "test-password-123"); err != nil {
		t.Fatal("cannot log back in", err)
	}
}
