package runtime

import (
	"context"
	"crypto/sha256"
	"fmt"
	"gopkg.in/yaml.v3"
	"huashan/internal/environment"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadyImagesReusesVerifiedIDsAndRestoresDeletedImages(t *testing.T) {
	rawPath := imageFixture(t, map[string][]byte{"manifest.json": []byte(`[{"Config":"config.json","Layers":["layer.tar"]}]`), "config.json": []byte(`{"architecture":"amd64","os":"linux"}`), "layer.tar": []byte("layer")})
	raw, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	m := environment.Manifest{FormatVersion: 1, Version: "1", Architecture: "amd64", Images: []environment.Image{{Key: "web", File: "images/web.tar", SHA256: fmt.Sprintf("%x", sha256.Sum256(raw))}}, Services: []environment.Service{{Key: "web", Image: "web", CPU: 1, MemoryMB: 128}}}
	encoded, err := yaml.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	packagePath := imageFixture(t, map[string][]byte{"manifest.yaml": encoded, "images/web.tar": raw})
	packageRaw, _ := os.ReadFile(packagePath)
	packageSHA := fmt.Sprintf("%x", sha256.Sum256(packageRaw))
	id := "sha256:" + strings.Repeat("1", 64)
	present := false
	loads := 0
	engineID := "engine-one"
	engineFailure := false
	engine := testEngine(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/info":
			fmt.Fprintf(w, `{"ID":%q,"OSType":"linux"}`, engineID)
		case r.URL.Path == "/images/load":
			io.Copy(io.Discard, r.Body)
			loads++
			present = true
			fmt.Fprint(w, `{"stream":"loaded"}`)
		case strings.HasPrefix(r.URL.Path, "/images/"):
			if engineFailure {
				http.Error(w, "unavailable", 500)
				return
			}
			if !present {
				http.NotFound(w, r)
				return
			}
			fmt.Fprintf(w, `{"Id":%q,"Architecture":"amd64","Os":"linux"}`, id)
		default:
			t.Errorf("unexpected Docker request: %s", r.URL)
			http.NotFound(w, r)
		}
	})
	temp := t.TempDir()
	ctx := context.Background()
	if _, err = engine.Import(ctx, packagePath, packageSHA, temp, m); err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Fatal("initial import missing")
	}
	// No archive at this path: successful startup proves the TAR is not opened.
	restartedEngine := &Engine{client: engine.client}
	ids, err := restartedEngine.ReadyImages(ctx, filepath.Join(temp, "absent.tar"), packageSHA, temp, m)
	if err != nil || ids["web"] != id || loads != 1 {
		t.Fatalf("warm startup: %v %v loads=%d", ids, err, loads)
	}
	// Display/key changes from merged packages still reuse content-addressed images.
	m.Images[0].Key = "renamed"
	m.Services[0].Image = "renamed"
	if _, err = engine.ReadyImages(ctx, "absent.tar", packageSHA, temp, m); err != nil || loads != 1 {
		t.Fatal("rename invalidated image cache", err)
	}
	m.Images[0].Key = "web"
	m.Services[0].Image = "web"
	present = false
	if _, err = engine.ReadyImages(ctx, packagePath, packageSHA, temp, m); err != nil || loads != 2 {
		t.Fatal("deleted image not restored", err, loads)
	}
	engineFailure = true
	if _, err = engine.ReadyImages(ctx, packagePath, packageSHA, temp, m); err == nil || loads != 2 {
		t.Fatal("Docker failure triggered reimport instead of surfacing error")
	}
	engineFailure = false
	engineID = "engine-two"
	if _, err = engine.ReadyImages(ctx, packagePath, packageSHA, temp, m); err != nil || loads != 3 {
		t.Fatal("cache crossed engine identity", err, loads)
	}
	// Acceptance always verifies package integrity, even when the image is warm.
	if _, err = engine.Import(ctx, packagePath, strings.Repeat("0", 64), temp, m); err == nil {
		t.Fatal("acceptance skipped package checksum")
	}
}
