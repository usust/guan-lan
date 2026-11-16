package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"huashan/internal/auth"
	"huashan/internal/database"
	"huashan/internal/environment"
	"huashan/internal/storage"
)

func TestValidateDockerPackageUpload(t *testing.T) {
	for _, compressed := range []bool{false, true} {
		t.Run(fmt.Sprintf("gzip=%v", compressed), func(t *testing.T) { validateDockerPackageUpload(t, compressed) })
	}
}
func validateDockerPackageUpload(t *testing.T, compressed bool) {
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
	admin, err := service.CreateUser(context.Background(), "", "admin", "Admin", "test-password-123", "ADMIN", true)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{auth: service, root: root}
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, entry := range []struct{ name, data string }{
		{"blobs/sha256/layer", "layer"},
		{"blobs/sha256/config", `{"architecture":"amd64","os":"linux","config":{"ExposedPorts":{"80/tcp":{}}}}`},
		{"manifest.json", `[{"Config":"blobs/sha256/config","Layers":["blobs/sha256/layer"]}]`},
	} {
		if err = tw.WriteHeader(&tar.Header{Name: entry.name, Mode: 0600, Size: int64(len(entry.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err = io.WriteString(tw, entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err = tw.Close(); err != nil {
		t.Fatal(err)
	}
	filename := "image.tar"
	if compressed {
		var zipped bytes.Buffer
		gz := gzip.NewWriter(&zipped)
		if _, err = gz.Write(raw.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err = gz.Close(); err != nil {
			t.Fatal(err)
		}
		raw = zipped
		filename += ".gz"
	}
	u := &packageUpload{ID: "docker-upload", Name: filename, Size: int64(raw.Len())}
	if err = os.WriteFile(s.uploadFile(u.ID), raw.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec("INSERT INTO package_uploads(id,actor_id,fingerprint,name,size,offset,updated_at) VALUES(?,?,?,?,?,?,?)", u.ID, admin.ID, "fingerprint", "image.tar", u.Size, u.Size, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a target left behind by a failed finalization attempt.
	if err = os.WriteFile(filepath.Join(root.Path, "uploads/packages", u.ID+".tar"), []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.validatePackageUpload(u, admin.ID); err != nil {
		t.Fatal(err)
	}
	var encoded string
	if err = db.QueryRow("SELECT result_json FROM package_uploads WHERE id=?", u.ID).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Token       string               `json:"package_token"`
		Environment environment.Manifest `json:"environment"`
		SHA         string               `json:"package_sha256"`
	}
	if err = json.Unmarshal([]byte(encoded), &result); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(root.Path, "uploads/packages", u.ID+".tar"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = environment.Inspect(bytes.NewReader(stored)); err != nil {
		t.Fatal(err)
	}
	if result.Environment.Images[0].SourceFile != filename {
		t.Fatal("original upload filename not retained")
	}
	if result.SHA != fmt.Sprintf("%x", sha256.Sum256(stored)) || len(result.Environment.Services) != 1 {
		t.Fatalf("invalid staged result: %+v", result)
	}
	if _, err = os.Stat(s.uploadFile(u.ID)); !os.IsNotExist(err) {
		t.Fatalf("source not cleaned: %v", err)
	}
	// Append the same Docker-save upload a second time. Both services must remain
	// backed by a single validated package, with distinct stable identities.
	result.Environment.Services[0].Name = "财务系统"
	body, _ := json.Marshal(map[string]any{"base_token": result.Token, "package_token": result.Token, "environment": result.Environment})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/api/v1/admin/packages/merge", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("user", admin)
	s.mergePackages(c)
	if recorder.Code != 200 {
		t.Fatalf("merge failed: %d %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Token       string               `json:"package_token"`
			Environment environment.Manifest `json:"environment"`
			SHA         string               `json:"package_sha256"`
		} `json:"data"`
	}
	if err = json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.Environment.Images[0].SourceFile != filename || response.Data.Environment.Images[1].SourceFile != filename {
		t.Fatal("source filenames lost during append")
	}
	services := response.Data.Environment.Services
	if len(services) != 2 || services[0].Name != "财务系统" || services[0].ID != result.Environment.Services[0].ID || services[0].ID == services[1].ID {
		t.Fatalf("incorrect appended services: %+v", services)
	}
	var mergedPath string
	if err = db.QueryRow("SELECT package_path FROM staged_packages WHERE token=? AND actor_id=?", response.Data.Token, admin.ID).Scan(&mergedPath); err != nil {
		t.Fatal(err)
	}
	mergedRaw, err := os.ReadFile(filepath.Join(root.Path, "uploads/packages", mergedPath))
	if err != nil {
		t.Fatal(err)
	}
	inspected, err := environment.Inspect(bytes.NewReader(mergedRaw))
	if err != nil {
		t.Fatal(err)
	}
	if !environment.SamePackageImages(inspected, response.Data.Environment) || response.Data.SHA != fmt.Sprintf("%x", sha256.Sum256(mergedRaw)) {
		t.Fatal("merged package identity mismatch")
	}

}

func TestExpandPackageGzip(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "temp"), 0700); err != nil {
		t.Fatal(err)
	}
	s := &Server{root: &storage.Root{Path: root}}
	var encoded bytes.Buffer
	gz := gzip.NewWriter(&encoded)
	gz.Write([]byte("tar contents"))
	gz.Close()
	damaged := append([]byte(nil), encoded.Bytes()...)
	damaged[len(damaged)-8] ^= 1
	for name, data := range map[string][]byte{"valid": encoded.Bytes(), "checksum": damaged, "truncated": encoded.Bytes()[:encoded.Len()-4]} {
		t.Run(name, func(t *testing.T) {
			src, err := os.CreateTemp(root, "source-*")
			if err != nil {
				t.Fatal(err)
			}
			defer src.Close()
			src.Write(data)
			src.Seek(0, io.SeekStart)
			dst, err := s.expandPackageGzip(src)
			if name == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				got, err := io.ReadAll(dst)
				dst.Close()
				os.Remove(dst.Name())
				if err != nil || string(got) != "tar contents" {
					t.Fatalf("unexpected output %q: %v", got, err)
				}
			} else if err == nil {
				dst.Close()
				t.Fatal("accepted corrupt gzip")
			}
			entries, err := os.ReadDir(filepath.Join(root, "temp"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("temporary files leaked: %v, %v", entries, err)
			}
		})
	}
}
