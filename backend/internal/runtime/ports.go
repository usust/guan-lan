package runtime

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"huashan/internal/environment"
)

// publishedEndpoints checks local listeners without depending on public DNS or NAT hairpinning.
func (m *Manager) publishedEndpoints(ctx context.Context, manifest environment.Manifest, containers map[string]string) ([]Endpoint, error) {
	host := m.endpointHost()
	deadline := time.Now().Add(30 * time.Second)
	for {
		endpoints := []Endpoint{}
		ready := true
		for _, s := range manifest.Services {
			x, err := m.Engine.inspect(ctx, containers[s.Key])
			if err != nil {
				return nil, err
			}
			if !x.State.Running {
				return nil, fmt.Errorf("服务 %s 已退出", s.Key)
			}
			for _, p := range s.Ports {
				if !p.Public {
					continue
				}
				binding := x.NetworkSettings.Ports[fmt.Sprintf("%d/%s", p.Port, p.Protocol)]
				if len(binding) != 1 || binding[0].HostIP != "0.0.0.0" {
					return nil, fmt.Errorf("服务 %s 的公开端口映射未生效", s.Key)
				}
				port, err := strconv.Atoi(binding[0].HostPort)
				if err != nil || port < 1 || port > 65535 {
					return nil, fmt.Errorf("服务 %s 的宿主端口分配失败", s.Key)
				}
				endpoints = append(endpoints, Endpoint{Service: s.Key, Port: p.Port, Protocol: p.Protocol, Address: net.JoinHostPort(host, binding[0].HostPort)})
				if p.Protocol == "tcp" {
					conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "tcp4", net.JoinHostPort("127.0.0.1", binding[0].HostPort))
					if err != nil {
						ready = false
					} else {
						conn.Close()
					}
				}
			}
		}
		if ready {
			return endpoints, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("题目公开 TCP 端口健康检查超时")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// Prefer the server LAN address for the default local Docker deployment.
func (m *Manager) endpointHost() string {
	host := m.PublicHost
	ip := net.ParseIP(host)
	if host != "" && host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return host
	}
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		virtual := false
		for _, prefix := range []string{"docker", "br-", "veth", "utun", "tun", "tap", "virbr"} {
			if strings.HasPrefix(iface.Name, prefix) {
				virtual = true
			}
		}
		if virtual {
			continue
		}
		addresses, _ := iface.Addrs()
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil && ip.IsPrivate() {
				return ip.String()
			}
		}
	}
	return "127.0.0.1"
}
