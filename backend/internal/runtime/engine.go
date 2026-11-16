package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Engine only connects to a local Unix socket. No user supplied Docker API paths.
type Engine struct{ client *http.Client }

func NewEngine(endpoint string) (*Engine, error) {
	if endpoint == "" {
		endpoint = DefaultEndpoint()
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "unix" || u.Path == "" || u.Host != "" {
		return nil, fmt.Errorf("Docker 运行仅支持本机 Unix Socket")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", u.Path)
	}}
	return &Engine{client: &http.Client{Transport: transport, Timeout: 85 * time.Second}}, nil
}
func (e *Engine) call(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	return e.send(ctx, method, path, r, "application/json", out)
}
func (e *Engine) send(ctx context.Context, method, path string, r io.Reader, contentType string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("Docker Engine 不可达，请检查 Docker Desktop 和 Socket 配置: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &dockerAPIError{Status: resp.StatusCode, Message: fmt.Sprintf("Docker %s 返回 %d: %s", path, resp.StatusCode, strings.TrimSpace(string(b)))}
	}
	if out != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
	}
	_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<20))
	return err
}
func (e *Engine) Identity(ctx context.Context) (string, error) {
	var info struct {
		ID           string
		OSType       string
		Architecture string
	}
	if err := e.call(ctx, "GET", "/info", nil, &info); err != nil {
		return "", err
	}
	if info.ID == "" || info.OSType != "linux" {
		return "", fmt.Errorf("容器题需要 Linux Docker Engine")
	}
	return info.ID, nil
}
func filter(labels map[string]string) string {
	var list []string
	for k, v := range labels {
		list = append(list, k+"="+v)
	}
	b, _ := json.Marshal(map[string]any{"label": list})
	return url.QueryEscape(string(b))
}

type containerInfo struct {
	ID    string `json:"Id"`
	State struct {
		Running   bool
		Error     string
		ExitCode  int
		OOMKilled bool
		Health    *struct {
			Status string
			Log    []struct {
				ExitCode int
				Output   string
			}
		}
	}
	Config struct {
		Healthcheck *struct {
			Interval, Timeout, StartPeriod int64
			Retries                        int
		}
	}
	HostConfig struct {
		Privileged  bool
		NetworkMode string
		CapAdd      []string
		CapDrop     []string
		SecurityOpt []string
		Memory      int64
		MemorySwap  int64
		NanoCPUs    int64
		PidsLimit   int64
		Binds       []string
	}
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string
		}
		Networks map[string]struct{ NetworkID string }
	}
	Mounts []struct{ Type string }
}

func (e *Engine) inspect(ctx context.Context, id string) (containerInfo, error) {
	var x containerInfo
	err := e.call(ctx, "GET", "/containers/"+url.PathEscape(id)+"/json", nil, &x)
	return x, err
}
