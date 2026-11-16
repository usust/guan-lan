package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"huashan/internal/auth"
	"huashan/internal/database"
	"huashan/internal/storage"
)

func TestRegistrationPersistsLocally(t *testing.T) {
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	db, err := database.Open(root.DatabasePath(), root.PlatformID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	handler := New(auth.New(db), root, Config{DockerEndpoint: "unix:///nonexistent"})
	request := func(route, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://localhost/api/v1/auth/"+route, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	body := `{"username":" New.Player ","password":"local-password-123","role":"ADMIN"}`
	w := request("register", body)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "password") {
		t.Fatal("registration leaked credentials or created a session")
	}
	var result struct {
		Data struct {
			User auth.User `json:"user"`
		} `json:"data"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	u := result.Data.User
	if u.Username != "new.player" || u.DisplayName != "new.player" || u.Role != "PLAYER" || u.Status != "ACTIVE" {
		t.Fatalf("unexpected user: %+v", u)
	}
	var hash string
	if err = db.QueryRow("SELECT password_hash FROM users WHERE id=?", u.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") || !auth.VerifyPassword(hash, "local-password-123") {
		t.Fatal("password not securely stored")
	}
	var audits int
	if err = db.QueryRow("SELECT count(*) FROM audit_logs WHERE target_id=? AND action='user.create'", u.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("missing audit", err, audits)
	}
	if w = request("register", body); w.Code != 409 || !strings.Contains(w.Body.String(), "USERNAME_TAKEN") {
		t.Fatal("duplicate allowed", w.Code, w.Body.String())
	}
	for _, invalid := range []string{
		`{`, `{}`, `{"username":"ab","password":"local-password-123"}`, `{"username":"valid","password":"short"}`,
		`{"username":"-invalid","password":"local-password-123"}`,
	} {
		if w = request("register", invalid); w.Code != 400 {
			t.Fatal("invalid registration accepted", w.Code, w.Body.String())
		}
	}
	// Reopen the on-disk database and reconstruct the server, rather than merely reusing memory.
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = database.Open(root.DatabasePath(), root.PlatformID)
	if err != nil {
		t.Fatal(err)
	}
	handler = New(auth.New(db), root, Config{DockerEndpoint: "unix:///nonexistent"})
	w = request("login", `{"username":"NEW.PLAYER","password":"local-password-123"}`)
	if w.Code != 200 {
		t.Fatal("login after restart failed", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing login cookie")
	}
	req := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/admin/users", nil)
	req.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("registered user has administrator access", w.Code)
	}
}

func TestRegistrationOriginAndRateLimit(t *testing.T) {
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
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
	handler := New(auth.New(db), root, Config{DockerEndpoint: "unix:///nonexistent"})
	req := httptest.NewRequest("POST", "http://localhost/api/v1/auth/register", bytes.NewBufferString(`{}`))
	req.Header.Set("Origin", "https://frontend.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.RemoteAddr = "192.0.2.2:1234"
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatal("origin should not block registration validation", w.Code)
	}
	for i := 0; i < 11; i++ {
		req = httptest.NewRequest("POST", "http://localhost/api/v1/auth/register", bytes.NewBufferString(`{`))
		w = httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		want := 400
		if i == 10 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("attempt %d: got %d, want %d", i, w.Code, want)
		}
	}
	if w.Header().Get("Retry-After") != "60" {
		t.Fatal("missing retry header")
	}
	req = httptest.NewRequest("POST", "http://localhost/api/v1/auth/login", bytes.NewBufferString(`{`))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatal("registration exhausted login rate limit", w.Code)
	}
}
