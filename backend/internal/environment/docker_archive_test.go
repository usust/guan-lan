package environment

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestWrapDockerArchive(t *testing.T) {
	for _, configPath := range []string{"config.json", "blobs/sha256/config"} {
		t.Run(configPath, func(t *testing.T) {
			raw := archive(t,
				member{"blobs/sha256/layer", []byte("layer"), 0},
				member{configPath, []byte(`{"architecture":"amd64","os":"linux","config":{"ExposedPorts":{"8080/tcp":{},"53/udp":{}}}}`), 0},
				member{"manifest.json", []byte(fmt.Sprintf(`[{"Config":%q,"Layers":["blobs/sha256/layer"],"RepoTags":["demo:latest"]}]`, configPath)), 0})
			var wrapped bytes.Buffer
			if err := WrapDockerArchive(bytes.NewReader(raw), &wrapped); err != nil {
				t.Fatal(err)
			}
			m, err := Inspect(bytes.NewReader(wrapped.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			if m.Architecture != "amd64" || len(m.Services) != 1 || len(m.Services[0].Ports) != 2 || m.Images[0].SHA256 != fmt.Sprintf("%x", sha256.Sum256(raw)) {
				t.Fatalf("incorrect generated manifest: %+v", m)
			}
		})
	}
}

func TestWrapDockerArchiveRejectsInvalidInput(t *testing.T) {
	config := member{"config.json", []byte(`{"architecture":"arm64","os":"linux"}`), 0}
	layer := member{"layer.tar", []byte("layer"), 0}
	manifest := member{"manifest.json", []byte(`[{"Config":"config.json","Layers":["layer.tar"]}]`), 0}
	for name, members := range map[string][]member{
		"missing layer":            {config, manifest},
		"missing config":           {layer, manifest},
		"directory as config":      {{"config.json", nil, '5'}, layer, manifest},
		"multiple images":          {config, layer, {"manifest.json", []byte(`[{"Config":"config.json"},{"Config":"config.json"}]`), 0}},
		"pure OCI":                 {{"oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`), 0}},
		"invalid platform package": {config, layer, manifest, {"manifest.yaml", []byte("bad"), 0}},
		"traversal":                {config, layer, manifest, {"../bad", nil, 0}},
	} {
		t.Run(name, func(t *testing.T) {
			var dst bytes.Buffer
			if err := WrapDockerArchive(bytes.NewReader(archive(t, members...)), &dst); err == nil {
				t.Fatal("accepted invalid archive")
			}
		})
	}
}
