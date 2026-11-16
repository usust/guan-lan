package api

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
	"huashan/internal/auth"
	"huashan/internal/competition"
	"huashan/internal/database"
	"huashan/internal/environment"
	"huashan/internal/storage"
)

func TestCompetitionHTTPWorkflow(t *testing.T) {
	p, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := storage.Open(p)
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
	if _, err = a.CreateUser(context.Background(), "", "admin", "Admin", "test-password-123", "ADMIN", true); err != nil {
		t.Fatal(err)
	}
	if _, err = a.CreateUser(context.Background(), "", "player", "Player", "test-password-123", "PLAYER", false); err != nil {
		t.Fatal(err)
	}
	h := New(a, root, Config{DockerEndpoint: "unix:///missing"})
	call := func(method, path string, body []byte, cookie *http.Cookie, csrf string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://localhost/api/v1"+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	login := func(name string) (*http.Cookie, string) {
		w := call("POST", "/auth/login", []byte(`{"username":"`+name+`","password":"test-password-123"}`), nil, "", nil)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var data struct {
			Data struct {
				CSRF string `json:"csrf_token"`
			}
		}
		json.Unmarshal(w.Body.Bytes(), &data)
		return w.Result().Cookies()[0], data.Data.CSRF
	}
	admin, adminCSRF := login("admin")
	player, playerCSRF := login("player")
	if w := call("POST", "/admin/question-types", []byte(`{"name":"云安全"}`), player, playerCSRF, nil); w.Code != 403 {
		t.Fatal("player managed taxonomy", w.Code)
	}
	for _, tc := range []struct {
		name string
		want int
	}{{"云安全", 201}, {"云安全", 409}, {"web", 409}, {" ", 400}} {
		b, _ := json.Marshal(map[string]string{"name": tc.name})
		if w := call("POST", "/admin/question-types", b, admin, adminCSRF, nil); w.Code != tc.want {
			t.Fatalf("create type %s: %d %s", tc.name, w.Code, w.Body.String())
		}
	}
	if w := call("DELETE", "/admin/question-types/云安全", nil, admin, adminCSRF, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("GET", "/admin/question-types", nil, admin, "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "WEB") {
		t.Fatal(w.Code, w.Body.String())
	}

	x := competition.Set{Title: "API 训练", StartsAt: time.Now().Add(-time.Hour).UnixMilli(), EndsAt: time.Now().Add(time.Hour).UnixMilli(), Questions: []competition.Question{{Title: "Web", Category: "WEB", Tags: []string{"JWT"}, Difficulty: "EASY", Subtasks: []competition.Subtask{{Title: "第一步", Points: 100, Flag: "flag{http}"}}}}}
	body, _ := json.Marshal(x)
	for _, tc := range []struct {
		cookie *http.Cookie
		csrf   string
		want   int
	}{{nil, "", 401}, {player, playerCSRF, 403}, {admin, "", 403}} {
		if w := call("POST", "/admin/sets", body, tc.cookie, tc.csrf, nil); w.Code != tc.want {
			t.Fatal("permission", w.Code, w.Body.String())
		}
	}
	decode := func(w *httptest.ResponseRecorder) competition.Set {
		t.Helper()
		if w.Code < 200 || w.Code >= 300 {
			t.Fatal(w.Code, w.Body.String())
		}
		var envelope struct {
			Data struct {
				Set competition.Set `json:"set"`
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data.Set
	}
	x = decode(call("POST", "/admin/sets", body, admin, adminCSRF, nil))
	if w := call("DELETE", "/admin/question-types/WEB", nil, admin, adminCSRF, nil); w.Code != 409 {
		t.Fatal("deleted referenced type", w.Code, w.Body.String())
	}
	if len(x.Questions[0].Tags) != 1 || x.Questions[0].Tags[0] != "JWT" {
		t.Fatal("tags missing from API response")
	}

	if w := call("GET", "/sets/"+x.ID, nil, player, "", nil); w.Code != 404 {
		t.Fatal("draft accessible", w.Code)
	}
	// Upload a structurally valid two-service environment sharing port 80.
	// Exceed both the default 16 KiB and ordinary admin 2 MiB request limits.
	image := bytes.Repeat([]byte("image archive test fixture"), 100000)
	m := environment.Manifest{FormatVersion: 1, Version: "1", Architecture: "amd64", Images: []environment.Image{{Key: "base", File: "images/base.tar", SHA256: fmt.Sprintf("%x", sha256.Sum256(image))}}, Services: []environment.Service{{Key: "web", Image: "base", MemoryMB: 128, CPU: 1, Ports: []environment.Port{{Port: 80, Protocol: "tcp", Public: true}}}, {Key: "admin", Image: "base", MemoryMB: 128, CPU: 1, Ports: []environment.Port{{Port: 80, Protocol: "tcp", Public: true}}}}}
	manifest, _ := yaml.Marshal(m)
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	for _, file := range []struct {
		name string
		data []byte
	}{{"manifest.yaml", manifest}, {"images/base.tar", image}} {
		tw.WriteHeader(&tar.Header{Name: file.name, Mode: 0600, Size: int64(len(file.data))})
		tw.Write(file.data)
	}
	tw.Close()

	t.Run("stage package before creating a question", func(t *testing.T) {
		headers := map[string]string{"Content-Type": "application/x-tar"}
		w := call("POST", "/admin/packages", archive.Bytes(), admin, adminCSRF, headers)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var response struct {
			Data struct {
				Token       string                `json:"package_token"`
				Environment *environment.Manifest `json:"environment"`
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Data.Token == "" || response.Data.Environment == nil {
			t.Fatal("missing staged package")
		}
		draft := competition.Set{Title: "暂存包题目", StartsAt: x.StartsAt, EndsAt: x.EndsAt, Questions: []competition.Question{{
			Title: "未保存时上传", Difficulty: "EASY", Environment: response.Data.Environment, PackageToken: response.Data.Token,
			Subtasks: []competition.Subtask{{Title: "小题", Points: 100, Flag: "flag{staged}"}},
		}}}
		body, _ := json.Marshal(draft)
		saved := decode(call("POST", "/admin/sets", body, admin, adminCSRF, nil))
		if !saved.Questions[0].PackageValidated {
			t.Fatal("staged package was not attached on save")
		}
		if w := call("POST", "/admin/sets", body, admin, adminCSRF, nil); w.Code != 400 {
			t.Fatal("consumed token reused", w.Code, w.Body.String())
		}
		if w := call("DELETE", "/admin/sets/"+saved.ID+"?version="+strconv.Itoa(saved.Version), nil, admin, adminCSRF, nil); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	})

	t.Run("player legacy attachments become durable binary downloads", func(t *testing.T) {
		content := bytes.Repeat([]byte{0, 1, 127, 255}, 100000)
		legacy := competition.Attachment{Name: "旧附件 测试.zip", Data: base64.StdEncoding.EncodeToString(content)}
		draft := competition.Set{Title: "旧附件", StartsAt: x.StartsAt, EndsAt: x.EndsAt, Questions: []competition.Question{{Title: "题目", Difficulty: "EASY", Attachments: []competition.Attachment{legacy}, Subtasks: []competition.Subtask{{Title: "小题", Points: 10, Flag: "flag{legacy}", Attachments: []competition.Attachment{legacy}}}}}}
		b, _ := json.Marshal(draft)
		saved := decode(call("POST", "/admin/sets", b, admin, adminCSRF, nil))
		if reply := call("GET", "/sets/"+saved.ID, nil, player, "", nil); reply.Code != 404 {
			t.Fatal("draft exposed")
		}
		status, _ := json.Marshal(map[string]any{"status": "PUBLISHED", "version": saved.Version})
		saved = decode(call("POST", "/admin/sets/"+saved.ID+"/status", status, admin, adminCSRF, nil))
		// Simulate existing data created before file-backed attachments.
		inline, _ := json.Marshal([]competition.Attachment{legacy})
		if _, err := db.Exec("UPDATE challenges SET attachments_json=? WHERE id=?", string(inline), saved.Questions[0].ID); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("UPDATE subtask_resources SET attachments_json=? WHERE subtask_id=?", string(inline), saved.Questions[0].Subtasks[0].ID); err != nil {
			t.Fatal(err)
		}
		read := call("GET", "/sets/"+saved.ID, nil, player, "", nil)
		converted := decode(read)
		if read.Body.Len() > 10000 {
			t.Fatal("inline bytes reached player")
		}
		for _, a := range []competition.Attachment{converted.Questions[0].Attachments[0], converted.Questions[0].Subtasks[0].Attachments[0]} {
			if a.Data != "" || a.FileID == "" || a.Size != int64(len(content)) {
				t.Fatal("legacy not converted", a.Name)
			}
			reply := call("GET", "/attachments/"+a.FileID, nil, player, "", nil)
			if reply.Code != 200 || !bytes.Equal(reply.Body.Bytes(), content) {
				t.Fatal("legacy download failed", reply.Code)
			}
		}
		again := decode(call("GET", "/sets/"+saved.ID, nil, player, "", nil))
		if again.Questions[0].Attachments[0].FileID != converted.Questions[0].Attachments[0].FileID {
			t.Fatal("conversion not durable")
		}
	})
	t.Run("large attachments use resumable binary storage and authorized range downloads", func(t *testing.T) {
		decodeUpload := func(w *httptest.ResponseRecorder) attachmentUpload {
			t.Helper()
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			var reply struct{ Data attachmentUpload }
			if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			return reply.Data
		}
		create := func(name string, size int64, fingerprint string) []byte {
			b, _ := json.Marshal(ginUploadInput(name, size, fingerprint))
			return b
		}
		fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte("large attachment")))
		// A 450 MiB declaration is accepted without a browser-sized JSON payload or preallocation.
		large := decodeUpload(call("POST", "/admin/attachment-uploads", create("450MiB.zip", 450<<20, fingerprint), admin, adminCSRF, nil))
		if large.Size != 450<<20 || large.Offset != 0 {
			t.Fatal("large file rejected")
		}
		if stat, err := os.Stat(filepath.Join(root.Path, "temp", "attachment-"+large.ID)); err != nil || stat.Size() != 0 {
			t.Fatal("unexpected preallocation", err)
		}
		if w := call("POST", "/admin/attachment-uploads", create("test.zip", 9<<20, fingerprint), player, playerCSRF, nil); w.Code != 403 {
			t.Fatal("player can upload", w.Code)
		}
		content := bytes.Repeat([]byte("x"), (8<<20)+17)
		fingerprint = fmt.Sprintf("%x", sha256.Sum256(content))
		body := create("测试附件.zip", int64(len(content)), fingerprint)
		upload := decodeUpload(call("POST", "/admin/attachment-uploads", body, admin, adminCSRF, nil))
		url := "/admin/attachment-uploads/" + upload.ID
		put := func(offset int, chunk []byte, hash string) *httptest.ResponseRecorder {
			return call("PUT", url, chunk, admin, adminCSRF, map[string]string{"Content-Type": "application/octet-stream", "Upload-Offset": strconv.Itoa(offset), "X-Chunk-SHA256": hash})
		}
		if w := call("PUT", url, content[:10], admin, "", nil); w.Code != 403 {
			t.Fatal("missing CSRF", w.Code)
		}
		if w := put(0, content[:10], strings.Repeat("0", 64)); w.Code != 400 {
			t.Fatal("bad hash accepted", w.Code)
		}
		first := content[:8<<20]
		upload = decodeUpload(put(0, first, fmt.Sprintf("%x", sha256.Sum256(first))))
		if upload.Offset != 8<<20 {
			t.Fatal("incorrect offset")
		}
		if w := call("POST", url+"/complete", nil, admin, adminCSRF, nil); w.Code != 409 {
			t.Fatal("incomplete finalized", w.Code)
		}
		// Restart the HTTP server and rediscover the committed checkpoint.
		h = New(a, root, Config{DockerEndpoint: "unix:///missing"})
		resumed := decodeUpload(call("POST", "/admin/attachment-uploads", body, admin, adminCSRF, nil))
		if resumed.ID != upload.ID || resumed.Offset != 8<<20 || len(resumed.Chunks) != 1 {
			t.Fatal("lost checkpoint")
		}
		if w := put(0, first, fmt.Sprintf("%x", sha256.Sum256(first))); w.Code != 409 {
			t.Fatal("stale offset accepted", w.Code)
		}
		last := content[8<<20:]
		decodeUpload(put(8<<20, last, fmt.Sprintf("%x", sha256.Sum256(last))))
		completed := decodeUpload(call("POST", url+"/complete", nil, admin, adminCSRF, nil))
		var attachment competition.Attachment
		if err := json.Unmarshal(completed.Result, &attachment); err != nil {
			t.Fatal(err)
		}
		if attachment.Data != "" || attachment.FileID != upload.ID || attachment.Size != int64(len(content)) {
			t.Fatal("not a metadata reference")
		}
		again := decodeUpload(call("POST", url+"/complete", nil, admin, adminCSRF, nil))
		if !bytes.Equal(again.Result, completed.Result) {
			t.Fatal("completion not idempotent")
		}
		download := "/attachments/" + attachment.FileID
		if w := call("GET", download, nil, nil, "", nil); w.Code != 401 {
			t.Fatal("anonymous download", w.Code)
		}
		if w := call("GET", download, nil, player, "", nil); w.Code != 404 {
			t.Fatal("unattached file exposed", w.Code)
		}
		draft := competition.Set{Title: "附件测试", StartsAt: x.StartsAt, EndsAt: x.EndsAt, Questions: []competition.Question{{Title: "大附件", Difficulty: "EASY", Attachments: []competition.Attachment{attachment}, Subtasks: []competition.Subtask{{Title: "小题", Points: 10, Flag: "flag{attachment}", Attachments: []competition.Attachment{attachment}}}}}}
		b, _ := json.Marshal(draft)
		if len(b) > 4096 {
			t.Fatal("attachment embedded in JSON")
		}
		saved := decode(call("POST", "/admin/sets", b, admin, adminCSRF, nil))
		if saved.Questions[0].Attachments[0].FileID != upload.ID {
			t.Fatal("reference not saved")
		}
		if w := call("GET", download, nil, player, "", nil); w.Code != 404 {
			t.Fatal("draft attachment exposed", w.Code)
		}
		// Keep only the subtask reference to exercise that download permission path.
		saved.Questions[0].Attachments = nil
		update, _ := json.Marshal(saved)
		saved = decode(call("PUT", "/admin/sets/"+saved.ID, update, admin, adminCSRF, nil))
		status, _ := json.Marshal(map[string]any{"status": "PUBLISHED", "version": saved.Version})
		saved = decode(call("POST", "/admin/sets/"+saved.ID+"/status", status, admin, adminCSRF, nil))
		w := call("GET", download, nil, player, "", map[string]string{"Range": "bytes=3-10"})
		if w.Code != 206 || !bytes.Equal(w.Body.Bytes(), content[3:11]) {
			t.Fatal("range download", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
			t.Fatal("unsafe disposition")
		}

		// Concurrent player downloads must leave the single DB connection free
		// after authorization and return identical bytes.
		results := make(chan error, 48)
		for i := 0; i < cap(results); i++ {
			go func() {
				reply := call("GET", download, nil, player, "", map[string]string{"Range": "bytes=3-10"})
				if reply.Code != 206 || !bytes.Equal(reply.Body.Bytes(), content[3:11]) {
					results <- fmt.Errorf("concurrent download: status=%d", reply.Code)
					return
				}
				results <- nil
			}()
		}
		deadline := time.NewTimer(10 * time.Second)
		defer deadline.Stop()
		for i := 0; i < cap(results); i++ {
			select {
			case err := <-results:
				if err != nil {
					t.Fatal(err)
				}
			case <-deadline.C:
				t.Fatal("concurrent downloads blocked")
			}
		}

		// Exercise the reported 450 MiB size end to end, using a bounded 8 MiB buffer.
		largeURL := "/admin/attachment-uploads/" + large.ID
		buffer := bytes.Repeat([]byte("L"), int(packageChunkBytes))
		for offset := int64(0); offset < large.Size; {
			n := packageChunkBytes
			if n > large.Size-offset {
				n = large.Size - offset
			}
			chunk := buffer[:int(n)]
			checkpoint := decodeUpload(call("PUT", largeURL, chunk, admin, adminCSRF, map[string]string{
				"Content-Type":   "application/octet-stream",
				"Upload-Offset":  strconv.FormatInt(offset, 10),
				"X-Chunk-SHA256": fmt.Sprintf("%x", sha256.Sum256(chunk)),
			}))
			offset += n
			if checkpoint.Offset != offset {
				t.Fatal("large-file checkpoint mismatch")
			}
		}
		decodeUpload(call("POST", largeURL+"/complete", nil, admin, adminCSRF, nil))
		if stat, err := os.Stat(filepath.Join(root.Path, "uploads/attachments", large.ID)); err != nil || stat.Size() != large.Size {
			t.Fatal("450 MiB file was not finalized", err)
		}
		tail := call("GET", "/attachments/"+large.ID, nil, admin, "", map[string]string{"Range": "bytes=-16"})
		if tail.Code != 206 || !bytes.Equal(tail.Body.Bytes(), bytes.Repeat([]byte("L"), 16)) {
			t.Fatal("450 MiB range download failed", tail.Code)
		}
		// Nonexistent references cannot be persisted as valid attachments.
		draft.Questions[0].Attachments[0].FileID = strings.Repeat("f", 64)
		b, _ = json.Marshal(draft)
		if w := call("POST", "/admin/sets", b, admin, adminCSRF, nil); w.Code != 400 {
			t.Fatal("nonexistent reference accepted", w.Code)
		}
		if err := os.Remove(filepath.Join(root.Path, "uploads/attachments", upload.ID)); err != nil {
			t.Fatal(err)
		}
		missing := call("GET", download, nil, player, "", nil)
		if missing.Code != 404 || !strings.Contains(missing.Body.String(), "NOT_FOUND") {
			t.Fatal("missing attachment should return structured error", missing.Code, missing.Body.String())
		}

	})
	t.Run("resumable upload survives interruption and server restart", func(t *testing.T) {
		fingerprint := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
		createBody, _ := json.Marshal(ginUploadInput("large.tar", int64(archive.Len()), fingerprint))
		decodeUpload := func(w *httptest.ResponseRecorder) packageUpload {
			t.Helper()
			if w.Code != 200 && w.Code != 202 {
				t.Fatal(w.Code, w.Body.String())
			}
			var reply struct{ Data packageUpload }
			if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
				t.Fatal(err)
			}
			return reply.Data
		}
		if w := call("POST", "/admin/package-uploads", createBody, player, playerCSRF, nil); w.Code != 403 {
			t.Fatal("player allowed", w.Code)
		}
		upload := decodeUpload(call("POST", "/admin/package-uploads", createBody, admin, adminCSRF, nil))
		url := "/admin/package-uploads/" + upload.ID
		split := 1 << 20
		first := archive.Bytes()[:split]
		headers := map[string]string{"Content-Type": "application/octet-stream", "Upload-Offset": "0", "X-Chunk-SHA256": fmt.Sprintf("%x", sha256.Sum256(first))}
		upload = decodeUpload(call("PUT", url, first, admin, adminCSRF, headers))
		if upload.Offset != int64(split) {
			t.Fatal("wrong offset", upload.Offset)
		}
		if w := call("PUT", url, first, admin, adminCSRF, headers); w.Code != 409 {
			t.Fatal("duplicate chunk appended", w.Code)
		}
		if w := call("POST", url+"/complete", nil, admin, adminCSRF, nil); w.Code != 409 {
			t.Fatal("incomplete upload finalized", w.Code)
		}
		// Simulate a process dying after writing bytes but before committing its DB offset.
		path := filepath.Join(root.Path, "temp", "resume-"+upload.ID)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte("uncommitted tail"))
		f.Close()
		h = New(a, root, Config{DockerEndpoint: "unix:///missing"})
		resumed := decodeUpload(call("POST", "/admin/package-uploads", createBody, admin, adminCSRF, nil))
		if resumed.ID != upload.ID || resumed.Offset != int64(split) {
			t.Fatal("resume lost", resumed)
		}
		if _, err := a.CreateUser(context.Background(), "", "other-admin", "Other", "test-password-123", "ADMIN", false); err != nil {
			t.Fatal(err)
		}
		other, otherCSRF := login("other-admin")
		for _, method := range []string{"GET", "PUT", "POST"} {
			target := url
			if method == "POST" {
				target += "/complete"
			}
			if w := call(method, target, nil, other, otherCSRF, nil); w.Code != 404 {
				t.Fatal("other owner accessible", method, w.Code)
			}
		}
		rest := archive.Bytes()[split:]
		headers["Upload-Offset"] = strconv.Itoa(split)
		if w := call("PUT", url, rest, admin, adminCSRF, headers); w.Code != 400 {
			t.Fatal("bad hash accepted", w.Code)
		}
		unchanged := decodeUpload(call("GET", url, nil, admin, "", nil))
		if unchanged.Offset != int64(split) {
			t.Fatal("failed chunk advanced offset")
		}
		headers["X-Chunk-SHA256"] = fmt.Sprintf("%x", sha256.Sum256(rest))
		upload = decodeUpload(call("PUT", url, rest, admin, adminCSRF, headers))
		if upload.Offset != int64(archive.Len()) {
			t.Fatal("upload incomplete")
		}
		upload = decodeUpload(call("POST", url+"/complete", nil, admin, adminCSRF, nil))
		deadline := time.Now().Add(5 * time.Second)
		for len(upload.Result) == 0 && time.Now().Before(deadline) {
			if upload.State == "error" {
				t.Fatal(upload.Error)
			}
			time.Sleep(10 * time.Millisecond)
			upload = decodeUpload(call("GET", url, nil, admin, "", nil))
		}
		if len(upload.Result) == 0 {
			t.Fatal("validation did not complete")
		}
		recovered := decodeUpload(call("POST", "/admin/package-uploads", createBody, admin, adminCSRF, nil))
		if recovered.ID != upload.ID || string(recovered.Result) != string(upload.Result) {
			t.Fatal("completed but unsaved package was not recovered")
		}
		again := decodeUpload(call("POST", url+"/complete", nil, admin, adminCSRF, nil))
		if string(again.Result) != string(upload.Result) {
			t.Fatal("completion not idempotent")
		}
		var result struct {
			Token       string                `json:"package_token"`
			Environment *environment.Manifest `json:"environment"`
		}
		if err := json.Unmarshal(upload.Result, &result); err != nil {
			t.Fatal(err)
		}
		draft := competition.Set{Title: "断点续传", StartsAt: x.StartsAt, EndsAt: x.EndsAt, Questions: []competition.Question{{Title: "Web", Difficulty: "EASY", Environment: result.Environment, PackageToken: result.Token, Subtasks: []competition.Subtask{{Title: "Flag", Points: 100, Flag: "flag{resume}"}}}}}
		body, _ := json.Marshal(draft)
		saved := decode(call("POST", "/admin/sets", body, admin, adminCSRF, nil))
		if !saved.Questions[0].PackageValidated {
			t.Fatal("package not attached")
		}
		if w := call("DELETE", "/admin/sets/"+saved.ID+"?version="+strconv.Itoa(saved.Version), nil, admin, adminCSRF, nil); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		// Advertising a size above the former 512 MiB limit must not be rejected.
		large, _ := json.Marshal(ginUploadInput("huge.tar", 20<<30, fingerprint))
		largeUpload := decodeUpload(call("POST", "/admin/package-uploads", large, admin, adminCSRF, nil))
		if largeUpload.Size != 20<<30 {
			t.Fatal("large upload rejected")
		}
	})
	uploadURL := "/admin/sets/" + x.ID + "/questions/" + x.Questions[0].ID + "/package"
	headers := map[string]string{"Content-Type": "application/x-tar", "X-Content-Version": strconv.Itoa(x.Version)}
	if w := call("POST", uploadURL, archive.Bytes(), player, playerCSRF, headers); w.Code != 403 {
		t.Fatal("player upload", w.Code)
	}
	if w := call("POST", uploadURL, []byte("not tar"), admin, adminCSRF, headers); w.Code != 400 {
		t.Fatal("invalid upload", w.Code, w.Body.String())
	}
	files, _ := os.ReadDir(filepath.Join(root.Path, "uploads/packages"))
	if len(files) != 0 {
		t.Fatal("invalid upload retained")
	}
	stale := x.Version
	x = decode(call("POST", uploadURL, archive.Bytes(), admin, adminCSRF, headers))
	if x.Version != stale+1 || !x.Questions[0].PackageValidated || len(x.Questions[0].Environment.Services) != 2 {
		t.Fatal("package not attached", x)
	}
	if w := call("POST", uploadURL, archive.Bytes(), admin, adminCSRF, headers); w.Code != 409 {
		t.Fatal("stale upload accepted", w.Code)
	}
	files, _ = os.ReadDir(filepath.Join(root.Path, "uploads/packages"))
	if len(files) != 1 {
		t.Fatal("upload orphan", len(files))
	}
	statusBody := []byte(fmt.Sprintf(`{"status":"PUBLISHED","version":%d}`, x.Version))
	verifyURL := "/admin/sets/" + x.ID + "/questions/" + x.Questions[0].ID + "/verify"
	verifyBody := []byte(fmt.Sprintf(`{"version":%d}`, x.Version))
	for _, tc := range []struct {
		cookie *http.Cookie
		csrf   string
		want   int
	}{{nil, "", 401}, {player, playerCSRF, 403}, {admin, "", 403}} {
		if w := call("POST", verifyURL, verifyBody, tc.cookie, tc.csrf, nil); w.Code != tc.want {
			t.Fatal("verification permission", w.Code)
		}
	}
	if w := call("POST", "/admin/sets/wrong-set/questions/"+x.Questions[0].ID+"/verify", verifyBody, admin, adminCSRF, nil); w.Code != 404 {
		t.Fatal("cross-set verification", w.Code)
	}
	if w := call("POST", verifyURL, verifyBody, admin, adminCSRF, nil); w.Code != 409 {
		t.Fatal("unreachable Engine accepted verification", w.Code)
	}
	if w := call("POST", "/admin/sets/"+x.ID+"/status", statusBody, admin, adminCSRF, nil); w.Code != 400 {
		t.Fatal("runtime-unverified set published", w.Code)
	}
	// Remove environment, then publish the static question.
	x.Questions[0].Environment = nil
	body, _ = json.Marshal(x)
	x = decode(call("PUT", "/admin/sets/"+x.ID, body, admin, adminCSRF, nil))
	files, _ = os.ReadDir(filepath.Join(root.Path, "uploads/packages"))
	if len(files) != 0 {
		t.Fatal("old package not removed")
	}
	statusBody = []byte(fmt.Sprintf(`{"status":"PUBLISHED","version":%d}`, x.Version))
	x = decode(call("POST", "/admin/sets/"+x.ID+"/status", statusBody, admin, adminCSRF, nil))
	w := call("GET", "/sets/"+x.ID, nil, player, "", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "flag{http}") || strings.Contains(w.Body.String(), "flag_hash") {
		t.Fatal("public response", w.Code, w.Body.String())
	}
	submitURL := "/subtasks/" + x.Questions[0].Subtasks[0].ID + "/submissions"
	if w = call("POST", submitURL, []byte(`{"flag":"flag{http}"}`), admin, adminCSRF, nil); w.Code != 403 {
		t.Fatal("admin score allowed", w.Code)
	}
	if w = call("POST", submitURL, []byte(`{"flag":"flag{http}"}`), player, playerCSRF, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"awarded_points":100`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call("POST", submitURL, []byte(`{"flag":"flag{http}"}`), player, playerCSRF, nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"awarded_points":0`) {
		t.Fatal("repeat score", w.Code, w.Body.String())
	}
	if w = call("GET", "/sets/"+x.ID+"/leaderboard", nil, player, "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"points":100`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call("GET", "/sets/"+x.ID+"/submissions", nil, player, "", nil); w.Code != 200 || strings.Contains(w.Body.String(), "flag{http}") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call("PUT", "/admin/sets/"+x.ID, body, admin, adminCSRF, nil); w.Code != 409 {
		t.Fatal("published edit", w.Code)
	}
}

// Kept as a small JSON map to exercise the public wire format.
func ginUploadInput(name string, size int64, fingerprint string) map[string]any {
	return map[string]any{"name": name, "size": size, "fingerprint": fingerprint}
}
