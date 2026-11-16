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
	"time"

	"huashan/internal/auth"
	"huashan/internal/competition"
	"huashan/internal/database"
	"huashan/internal/storage"
)

func TestTaxonomyManagementHTTP(t *testing.T) {
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
	a := auth.New(db)
	ctx := context.Background()
	for _, role := range []string{"ADMIN", "PLAYER"} {
		if _, err = a.CreateUser(ctx, "", strings.ToLower(role), role, "test-password-123", role, role == "ADMIN"); err != nil {
			t.Fatal(err)
		}
	}
	handler := New(a, root, Config{DockerEndpoint: "unix:///missing"})
	call := func(method, path string, body any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "/api/v1"+path, bytes.NewReader(b))
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
	login := func(name string) (*http.Cookie, string) {
		w := call("POST", "/auth/login", map[string]string{"username": name, "password": "test-password-123"}, nil, "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var data struct {
			Data struct {
				CSRF string    `json:"csrf_token"`
				User auth.User `json:"user"`
			}
		}
		json.Unmarshal(w.Body.Bytes(), &data)
		return w.Result().Cookies()[0], data.Data.CSRF
	}
	admin, csrf := login("admin")
	if w := call("POST", "/admin/question-types", map[string]string{"name": "注入漏洞", "parent_name": "WEB"}, admin, csrf); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", "/admin/question-types", map[string]string{"name": "SQL注入", "parent_name": "注入漏洞"}, admin, csrf); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", "/admin/question-types", map[string]string{"name": "错误", "parent_name": "不存在"}, admin, csrf); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("PUT", "/admin/question-types/WEB/parent", map[string]string{"parent_name": "SQL注入"}, admin, csrf); w.Code != 400 {
		t.Fatal("cycle accepted", w.Code, w.Body.String())
	}
	if w := call("DELETE", "/admin/question-types/注入漏洞", nil, admin, csrf); w.Code != 409 {
		t.Fatal("deleted parent", w.Code, w.Body.String())
	}
	if w := call("PUT", "/admin/question-types/SQL注入/parent", map[string]string{"parent_name": "WEB"}, admin, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("GET", "/admin/question-types", nil, admin, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"parent_name":"WEB"`) {
		t.Fatal("missing hierarchy", w.Body.String())
	}

	player, playerCSRF := login("player")
	for _, tc := range []struct {
		cookie *http.Cookie
		token  string
		code   int
	}{{nil, "", 401}, {player, playerCSRF, 403}, {admin, "", 403}} {
		if w := call("POST", "/admin/knowledge-points", map[string]string{"name": "身份认证"}, tc.cookie, tc.token); w.Code != tc.code {
			t.Fatal("knowledge permission", w.Code, w.Body.String())
		}
	}
	for _, p := range [][2]string{{"身份认证", ""}, {"会话管理", "身份认证"}} {
		if w := call("POST", "/admin/knowledge-points", map[string]string{"name": p[0], "parent_name": p[1], "owner_type": "WEB"}, admin, csrf); w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if w := call("POST", "/admin/knowledge-points", map[string]string{"name": "未归属新知识点"}, admin, csrf); w.Code != 400 {
		t.Fatal("accepted unowned new knowledge", w.Code, w.Body.String())
	}
	if w := call("PUT", "/admin/knowledge-points/身份认证/types", map[string]any{"types": []string{"WEB", "逆向"}}, admin, csrf); w.Code != 400 {
		t.Fatal("accepted multi ownership", w.Code, w.Body.String())
	}
	if w := call("POST", "/admin/knowledge-points", map[string]string{"name": "二进制考点", "owner_type": "MISC"}, admin, csrf); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("DELETE", "/admin/question-types/MISC", nil, admin, csrf); w.Code != 409 {
		t.Fatal("deleted owning type", w.Code, w.Body.String())
	}
	if w := call("PUT", "/admin/knowledge-points/身份认证/types", map[string]any{"types": []string{"WEB"}}, admin, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("PUT", "/admin/knowledge-points/身份认证/parent", map[string]string{"parent_name": "会话管理"}, admin, csrf); w.Code != 400 {
		t.Fatal("knowledge cycle", w.Code, w.Body.String())
	}
	if w := call("GET", "/admin/knowledge-points", nil, player, ""); w.Code != 403 {
		t.Fatal("player knowledge access", w.Code)
	}
	if w := call("GET", "/admin/knowledge-points", nil, admin, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"parent_name":"身份认证"`) || !strings.Contains(w.Body.String(), `"types":["WEB"]`) {
		t.Fatal("knowledge hierarchy", w.Code, w.Body.String())
	}

	change := map[string]string{"source": "PWN", "target": "二进制安全", "mode": "rename"}
	for _, tc := range []struct {
		cookie *http.Cookie
		token  string
		code   int
	}{{nil, "", 401}, {player, playerCSRF, 403}, {admin, "", 403}} {
		for _, path := range []string{"/admin/taxonomy/type/change", "/admin/questions/reclassify"} {
			if w := call("POST", path, change, tc.cookie, tc.token); w.Code != tc.code {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
	}
	if w := call("POST", "/admin/taxonomy/type/change", change, admin, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var actor string
	if err = db.QueryRow("SELECT id FROM users WHERE username='admin'").Scan(&actor); err != nil {
		t.Fatal(err)
	}
	service := competition.Service{DB: db}
	id, err := service.Save(ctx, actor, competition.Set{Library: true, Title: "分类测试", EndsAt: time.Now().Add(time.Hour).UnixMilli(), Questions: []competition.Question{{Title: "题目", Category: "WEB", Difficulty: "EASY", Tags: []string{"JWT"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Save(ctx, actor, competition.Set{Title: "竞赛", EndsAt: time.Now().Add(time.Hour).UnixMilli(), Questions: []competition.Question{{Title: "副本", Category: "WEB", Difficulty: "EASY", Tags: []string{"jwt"}}}}); err != nil {
		t.Fatal(err)
	}
	initial := call("GET", "/admin/question-types", nil, admin, "")
	var grouped struct {
		Data struct {
			Tags []struct {
				Usage int `json:"usage_count"`
			} `json:"tag_items"`
		}
	}
	if err = json.Unmarshal(initial.Body.Bytes(), &grouped); err != nil || len(grouped.Data.Tags) != 1 || grouped.Data.Tags[0].Usage != 2 {
		t.Fatal("case variants not grouped", initial.Body.String(), err)
	}
	x, err := service.Get(ctx, id, actor, true)
	if err != nil {
		t.Fatal(err)
	}
	if w := call("PUT", "/admin/questions/"+id+"/knowledge", map[string]any{"version": x.Version, "tags": []string{"JWT"}}, player, playerCSRF); w.Code != 403 {
		t.Fatal("player metadata update", w.Code)
	}
	if w := call("PUT", "/admin/questions/"+id+"/knowledge", map[string]any{"version": x.Version, "tags": []string{"JWT"}}, admin, ""); w.Code != 403 {
		t.Fatal("missing csrf", w.Code)
	}
	if w := call("PUT", "/admin/questions/"+id+"/knowledge", map[string]any{"version": x.Version, "tags": []string{"JWT"}}, admin, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	x, err = service.Get(ctx, id, actor, true)
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"category": "二进制安全", "items": []map[string]any{{"id": id, "version": x.Version}}}
	if w := call("POST", "/admin/questions/reclassify", body, admin, csrf); w.Code != 200 || !strings.Contains(w.Body.String(), `"updated":1`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", "/admin/questions/reclassify", body, admin, csrf); w.Code != 409 {
		t.Fatal("stale batch", w.Code, w.Body.String())
	}
	change = map[string]string{"source": "JWT", "target": "令牌伪造", "mode": "rename"}
	if w := call("POST", "/admin/taxonomy/tag/change", change, admin, csrf); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := call("GET", "/admin/question-types", nil, admin, "")
	var response struct {
		Data struct {
			Tags []struct {
				Name    string `json:"name"`
				Library int    `json:"question_count"`
				Usage   int    `json:"usage_count"`
			} `json:"tag_items"`
		}
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.Data.Tags) != 1 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	tag := response.Data.Tags[0]
	if tag.Name != "令牌伪造" || tag.Library != 1 || tag.Usage != 2 {
		t.Fatal(tag)
	}
	change["extra"] = "forbidden"
	if w := call("POST", "/admin/taxonomy/tag/change", change, admin, csrf); w.Code != 400 {
		t.Fatal("unknown field accepted", w.Code)
	}
}
