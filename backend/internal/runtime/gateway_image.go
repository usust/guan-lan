package runtime

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

//go:generate env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath "-ldflags=-s -w" -o gateway-linux-amd64 ./gateway
//go:generate env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath "-ldflags=-s -w" -o gateway-linux-arm64 ./gateway
//go:embed gateway-linux-amd64
var gatewayAMD64 []byte

//go:embed gateway-linux-arm64
var gatewayARM64 []byte

type gatewayRoute struct {
	Listen   int
	Target   string
	Protocol string
}

func (m *Manager) gatewayImage(ctx context.Context) (string, error) {
	var host struct{ Architecture string }
	if err := m.Engine.call(ctx, "GET", "/info", nil, &host); err != nil {
		return "", err
	}
	arch := host.Architecture
	var executable []byte
	switch arch {
	case "x86_64", "amd64":
		arch = "amd64"
		executable = gatewayAMD64
	case "aarch64", "arm64":
		arch = "arm64"
		executable = gatewayARM64
	default:
		return "", fmt.Errorf("网关暂不支持 Docker 架构 %s", arch)
	}
	var layer bytes.Buffer
	tw := tar.NewWriter(&layer)
	if err := tw.WriteHeader(&tar.Header{Name: "gateway", Mode: 0555, Size: int64(len(executable)), Typeflag: tar.TypeReg}); err != nil {
		return "", err
	}
	if _, err := tw.Write(executable); err != nil {
		return "", err
	}
	if err := tw.Close(); err != nil {
		return "", err
	}
	config, _ := json.Marshal(map[string]any{"architecture": arch, "os": "linux", "config": map[string]any{"Entrypoint": []string{"/gateway"}, "User": "65534:65534"}, "rootfs": map[string]any{"type": "layers", "diff_ids": []string{fmt.Sprintf("sha256:%x", sha256.Sum256(layer.Bytes()))}}})
	name := fmt.Sprintf("%x.json", sha256.Sum256(config))
	tag := "huashan-gateway:" + name[:64]
	var existing struct {
		ID string `json:"Id"`
	}
	if err := m.Engine.call(ctx, "GET", "/images/"+tag+"/json", nil, &existing); err == nil && existing.ID != "" {
		return existing.ID, nil
	}
	manifest, _ := json.Marshal([]map[string]any{{"Config": name, "RepoTags": []string{tag}, "Layers": []string{"layer.tar"}}})
	file, err := os.CreateTemp(filepath.Join(m.Root, "temp"), "gateway-image-*.tar")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	archive := tar.NewWriter(file)
	for _, item := range []struct {
		name string
		data []byte
	}{{name, config}, {"manifest.json", manifest}, {"layer.tar", layer.Bytes()}} {
		if err = archive.WriteHeader(&tar.Header{Name: item.name, Mode: 0600, Size: int64(len(item.data)), Typeflag: tar.TypeReg}); err != nil {
			return "", err
		}
		if _, err = archive.Write(item.data); err != nil {
			return "", err
		}
	}
	if err = archive.Close(); err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	if err = m.Engine.load(ctx, file.Name()); err != nil {
		return "", err
	}
	if err = m.Engine.call(ctx, "GET", "/images/"+tag+"/json", nil, &existing); err != nil {
		return "", err
	}
	return existing.ID, nil
}
