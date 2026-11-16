package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Probe is deliberately read-only. Container orchestration will use the SDK adapter.
type DockerInfo struct {
	Available    bool   `json:"available"`
	Endpoint     string `json:"endpoint"`
	Version      string `json:"version"`
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Error        string `json:"error,omitempty"`
}

func DefaultEndpoint() string {
	if v := os.Getenv("DOCKER_HOST"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	desktop := filepath.Join(home, ".docker", "run", "docker.sock")
	if _, err := os.Stat(desktop); err == nil {
		return "unix://" + desktop
	}
	return "unix:///var/run/docker.sock"
}
func Probe(ctx context.Context, endpoint string) DockerInfo {
	result := DockerInfo{Endpoint: endpoint}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "unix" || u.Path == "" {
		result.Error = "当前增量仅支持显式 Unix Socket，不支持远程 Engine 或 Docker Context 自动解析"
		return result
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", u.Path)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/version", nil)
	resp, err := client.Do(req)
	if err != nil {
		result.Error = "Docker Engine 不可达，请检查 Socket 配置与 Docker Desktop 状态"
		return result
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		result.Error = fmt.Sprintf("Docker Engine returned HTTP %d", resp.StatusCode)
		return result
	}
	var version struct {
		Version string
		Os      string
		Arch    string
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&version); err != nil {
		result.Error = "Docker Engine 返回了无效版本信息"
		return result
	}
	if strings.TrimSpace(version.Version) == "" {
		result.Error = "Docker Engine 未返回版本"
		return result
	}
	result.Available = true
	result.Version = version.Version
	result.OS = version.Os
	result.Architecture = version.Arch
	return result
}
