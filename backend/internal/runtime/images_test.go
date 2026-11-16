package runtime

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func imageFixture(t *testing.T, items map[string][]byte) string {
	t.Helper()
	var buffer bytes.Buffer
	tw := tar.NewWriter(&buffer)
	for name, data := range items {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		tw.Write(data)
	}
	tw.Close()
	path := filepath.Join(t.TempDir(), "image.tar")
	if err := os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestImageImportReplacesUserTagsAndIgnoresOCIIndex(t *testing.T) {
	config := []byte(`{"architecture":"amd64","os":"linux"}`)
	manifest := []byte(`[{"Config":"config.json","RepoTags":["important:latest"],"Layers":["layer.tar"]}]`)
	path := imageFixture(t, map[string][]byte{"config.json": config, "manifest.json": manifest, "layer.tar": []byte("layer"), "repositories": []byte("old tags"), "index.json": []byte("unrelated OCI tags"), "oci-layout": []byte("layout")})
	tag, clean, err := sanitizeImage(path)
	if err != nil {
		t.Fatal(err)
	}
	if tag != fmt.Sprintf("huashan-import:%x", sha256.Sum256(config)) {
		t.Fatal("not content addressed", tag)
	}
	f, err := os.Open(clean)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := tar.NewReader(f)
	found := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "repositories" || h.Name == "index.json" || h.Name == "oci-layout" {
			t.Fatal("mutable tag metadata retained")
		}
		if h.Name == "manifest.json" {
			var rows []struct{ RepoTags []string }
			if err = json.NewDecoder(tr).Decode(&rows); err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || len(rows[0].RepoTags) != 1 || rows[0].RepoTags[0] != tag {
				t.Fatal(rows)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing image manifest")
	}
}
func TestRejectIncompleteOrMultipleImageArchives(t *testing.T) {
	for _, manifest := range []string{`[]`, `[{"Config":"missing"}]`, `[{"Config":"config","Layers":["missing"]}]`, `[{"Config":"config"},{"Config":"config"}]`} {
		path := imageFixture(t, map[string][]byte{"config": []byte("config"), "manifest.json": []byte(manifest)})
		if _, _, err := sanitizeImage(path); err == nil {
			t.Fatal("accepted invalid image", manifest)
		}
	}
	path := imageFixture(t, map[string][]byte{"../escape": []byte("bad")})
	if _, _, err := sanitizeImage(path); err == nil || !strings.Contains(err.Error(), "路径") {
		t.Fatal("accepted unsafe path", err)
	}
}
