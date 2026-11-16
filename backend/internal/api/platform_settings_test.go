package api

import (
	"bytes"
	"context"
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

func TestPlatformSettings(t *testing.T) {
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
	service := auth.New(db)
	if _, err := service.CreateUser(context.Background(), "", "admin", "管理员", "test-password-123", "ADMIN", true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateUser(context.Background(), "", "player", "选手", "test-password-123", "PLAYER", false); err != nil {
		t.Fatal(err)
	}
	_, token, csrf, err := service.Login(context.Background(), "admin", "test-password-123")
	if err != nil {
		t.Fatal(err)
	}
	_, player, _, err := service.Login(context.Background(), "player", "test-password-123")
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{DockerEndpoint: "unix:///nonexistent", ConfigFile: "/deployment/config.ini", Listen: "127.0.0.1:8080"}
	handler := New(service, root, cfg)
	call := func(method, path, body, session, csrf string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		if session != "" {
			req.AddCookie(&http.Cookie{Name: "huashan_session", Value: session})
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	t.Run("difficulty settings", func(t *testing.T) {
		if w := call("GET", "/difficulties", "", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "简单") {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, tc := range []struct {
			session, csrf string
			code          int
		}{{"", "", 401}, {player, "", 403}, {token, "", 403}} {
			if w := call("PUT", "/admin/difficulty-settings", `{"EASY":"入门","MEDIUM":"进阶","HARD":"挑战"}`, tc.session, tc.csrf); w.Code != tc.code {
				t.Fatal(w.Code, w.Body.String())
			}
		}
		for _, body := range []string{`{}`, `{"EASY":"重复","MEDIUM":"重复","HARD":"困难"}`, `{"EASY":"简单","MEDIUM":"中等","HARD":"过长过长过长过长过长过长过长过长过长"}`} {
			if w := call("PUT", "/admin/difficulty-settings", body, token, csrf); w.Code != 400 {
				t.Fatal(w.Code, w.Body.String())
			}
		}
		if w := call("PUT", "/admin/difficulty-settings", `{"EASY":" 入门 ","MEDIUM":"进阶","HARD":"挑战"}`, token, csrf); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		// A new handler must read the saved settings from the database.
		req := httptest.NewRequest("GET", "/api/v1/difficulties", nil)
		w := httptest.NewRecorder()
		New(service, root, cfg).ServeHTTP(w, req)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"EASY":"入门"`) {
			t.Fatal(w.Code, w.Body.String())
		}
		if w := call("PUT", "/admin/difficulty-settings", `{"CUSTOM_TEST":"专家"}`, token, csrf); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if w := call("GET", "/difficulties", "", "", ""); w.Code != 200 || strings.Contains(w.Body.String(), `"EASY"`) || !strings.Contains(w.Body.String(), "专家") {
			t.Fatal(w.Code, w.Body.String())
		}
		if _, err := db.Exec(`INSERT INTO challenge_sets VALUES('difficulty-test','test','','DRAFT',0,1,1,0,0)`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO challenges(id,set_id,title,description,category,difficulty,position,environment_json) VALUES('difficulty-q','difficulty-test','test','','WEB','CUSTOM_TEST',0,'null')`); err != nil {
			t.Fatal(err)
		}
		if w := call("PUT", "/admin/difficulty-settings", `{"EASY":"简单"}`, token, csrf); w.Code != 409 {
			t.Fatal(w.Code, w.Body.String())
		}
		if w := call("PUT", "/admin/difficulty-settings", `{"CUSTOM_TEST":"大师"}`, token, csrf); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if w := call("GET", "/difficulties", "", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "大师") {
			t.Fatal(w.Code, w.Body.String())
		}

	})
	if w := call("GET", "/platform", "", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "华山平台") || strings.Contains(w.Body.String(), root.Path) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		session string
		code    int
	}{{"", 401}, {player, 403}} {
		if w := call("GET", "/admin/platform-settings", "", tc.session, ""); w.Code != tc.code {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := call("PUT", "/admin/platform-settings", `{"name":"新平台"}`, token, ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	for _, body := range []string{`{"name":" "}`, `{"name":"平台","icon":"data:image/svg+xml;base64,PHN2Zz4="}`, `{"name":"平台","icon":"data:image/png;base64,aGVsbG8="}`} {
		if w := call("PUT", "/admin/platform-settings", body, token, csrf); w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := call("PUT", "/admin/platform-settings", `{"name":"新平台","description":"训练空间","icon":""}`, token, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	handler = New(service, root, cfg)
	w := call("GET", "/platform", "", "", "")
	var response struct {
		Data platformSettings `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Data.Name != "新平台" || response.Data.Description != "训练空间" {
		t.Fatal(w.Body.String(), err)
	}
	w = call("GET", "/admin/platform-settings", "", token, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), filepath.Join(root.Path, "config")) || !strings.Contains(w.Body.String(), cfg.ConfigFile) {
		t.Fatal(w.Code, w.Body.String())
	}
}
