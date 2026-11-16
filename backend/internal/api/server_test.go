package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"huashan/internal/auth"
	"huashan/internal/database"
	"huashan/internal/storage"
)

func TestAuthenticationAndUsers(t *testing.T) {
	rootPath, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := storage.Open(rootPath)
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
	admin, err := service.CreateUser(context.Background(), "", "admin", "管理员", "test-password-123", "ADMIN", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.CreateUser(context.Background(), "", "other", "Other", "test-password-123", "ADMIN", true); err != auth.ErrConflict {
		t.Fatalf("bootstrap repeated: %v", err)
	}
	handler := New(service, root, Config{DockerEndpoint: "unix:///nonexistent"})
	request := func(method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://localhost"+path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	if w := request("GET", "/api/v1/admin/users", "", nil, ""); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := request("POST", "/api/v1/auth/login", `{"username":"admin","password":"wrong"}`, nil, "")
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	w = request("POST", "/api/v1/auth/login", `{"username":"admin","password":"test-password-123"}`, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var login struct {
		Data struct {
			CSRF string `json:"csrf_token"`
		}
	}
	if err = json.Unmarshal(w.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe cookie")
	}
	cookie := cookies[0]
	if w = request("POST", "/api/v1/admin/users", `{}`, cookie, ""); w.Code != 403 {
		t.Fatal("missing csrf", w.Code)
	}
	w = request("POST", "/api/v1/admin/users", `{"username":"player","display_name":"选手","password":"player-password-123","role":"PLAYER"}`, cookie, login.Data.CSRF)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var created struct {
		Data struct {
			User auth.User `json:"user"`
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if w = request("PATCH", "/api/v1/admin/users/"+admin.ID, `{"status":"DISABLED"}`, cookie, login.Data.CSRF); w.Code != 409 {
		t.Fatal("self-disable allowed", w.Code)
	}
	w = request("POST", "/api/v1/auth/login", `{"username":"player","password":"player-password-123"}`, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	playerCookie := w.Result().Cookies()[0]
	if w = request("GET", "/api/v1/admin/users", "", playerCookie, ""); w.Code != 403 {
		t.Fatal("player got admin access", w.Code)
	}
	w = request("PATCH", "/api/v1/admin/users/"+created.Data.User.ID, `{"status":"DISABLED"}`, cookie, login.Data.CSRF)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = request("GET", "/api/v1/auth/me", "", playerCookie, ""); w.Code != 401 {
		t.Fatal("disabled session valid", w.Code)
	}
	if w = request("POST", "/api/v1/auth/logout", `{}`, cookie, login.Data.CSRF); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w = request("GET", "/api/v1/auth/me", "", cookie, ""); w.Code != 401 {
		t.Fatal("logged-out session valid", w.Code)
	}
}

func TestOriginAndLoginLimit(t *testing.T) {
	rootPath, _ := filepath.EvalSymlinks(t.TempDir())
	root, err := storage.Open(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	db, err := database.Open(root.DatabasePath(), root.PlatformID)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	h := New(auth.New(db), root, Config{})
	req := httptest.NewRequest("POST", "http://localhost/api/v1/auth/login", bytes.NewBufferString(`{}`))
	req.Header.Set("Origin", "https://frontend.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatal("origin should not block login validation", w.Code)
	}
	for i := 0; i < 11; i++ {
		req = httptest.NewRequest("POST", "http://localhost/api/v1/auth/login", bytes.NewBufferString(`{`))
		w = httptest.NewRecorder()
		h.ServeHTTP(w, req)
	}
	if w.Code != 429 {
		t.Fatal("no rate limit", w.Code)
	}
}
