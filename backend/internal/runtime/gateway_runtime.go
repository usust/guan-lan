package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"

	"huashan/internal/environment"
)

func (m *Manager) createGateway(ctx context.Context, id, internal string, manifest environment.Manifest) ([]Endpoint, error) {
	routes := []gatewayRoute{}
	endpoints := []Endpoint{}
	ports := []string{}
	exposed := map[string]any{}
	bindings := map[string]any{}
	for _, s := range manifest.Services {
		for _, p := range s.Ports {
			if !p.Public {
				continue
			}
			listen := 10000 + len(routes)
			routes = append(routes, gatewayRoute{listen, net.JoinHostPort(s.Key, strconv.Itoa(p.Port)), p.Protocol})
			port := fmt.Sprintf("%d/%s", listen, p.Protocol)
			ports = append(ports, port)
			exposed[port] = map[string]any{}
			bindings[port] = []map[string]string{{"HostIp": "127.0.0.1", "HostPort": ""}}
			endpoints = append(endpoints, Endpoint{Service: s.Key, Port: p.Port, Protocol: p.Protocol})
		}
	}
	if len(routes) == 0 {
		return endpoints, nil
	}
	image, err := m.gatewayImage(ctx)
	if err != nil {
		return nil, fmt.Errorf("准备访问网关: %w", err)
	}
	var network struct {
		ID string `json:"Id"`
	}
	if err = m.Engine.call(ctx, "POST", "/networks/create", map[string]any{"Name": "huashan-access-" + id, "Driver": "bridge", "Internal": false, "Labels": m.labels(id), "Options": map[string]string{"com.docker.network.bridge.enable_icc": "false"}}, &network); err != nil {
		return nil, err
	}
	args, _ := json.Marshal(routes)
	config := map[string]any{"Image": image, "Cmd": []string{string(args)}, "Labels": m.labels(id), "ExposedPorts": exposed,
		"HostConfig":       map[string]any{"NetworkMode": network.ID, "ReadonlyRootfs": true, "CapDrop": []string{"ALL"}, "SecurityOpt": []string{"no-new-privileges:true"}, "Memory": 64 << 20, "MemorySwap": 64 << 20, "NanoCpus": int64(500000000), "PidsLimit": 128, "PortBindings": bindings, "LogConfig": map[string]any{"Type": "json-file", "Config": map[string]string{"max-size": "1m", "max-file": "1"}}},
		"NetworkingConfig": map[string]any{"EndpointsConfig": map[string]any{network.ID: map[string]any{}, internal: map[string]any{}}}}
	var gateway struct {
		ID string `json:"Id"`
	}
	if err = m.Engine.call(ctx, "POST", "/containers/create?name=huashan-gateway-"+id, config, &gateway); err != nil {
		return nil, err
	}
	if err = m.Engine.call(ctx, "POST", "/containers/"+gateway.ID+"/start", nil, nil); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		x, err := m.Engine.inspect(ctx, gateway.ID)
		if err != nil {
			return nil, err
		}
		if !x.State.Running {
			return nil, fmt.Errorf("访问网关启动失败，请检查入口服务是否监听配置端口")
		}
		if x.HostConfig.Privileged || !contains(x.HostConfig.CapDrop, "ALL") || !contains(x.HostConfig.SecurityOpt, "no-new-privileges") || len(x.NetworkSettings.Networks) != 2 {
			return nil, fmt.Errorf("访问网关隔离策略未生效")
		}
		ready := true
		for i, p := range ports {
			binding := x.NetworkSettings.Ports[p]
			if len(binding) != 1 || binding[0].HostIP != "127.0.0.1" || binding[0].HostPort == "" {
				return nil, fmt.Errorf("访问网关本机端口分配失败")
			}
			address := net.JoinHostPort("127.0.0.1", binding[0].HostPort)
			endpoints[i].Address = address
			if endpoints[i].Protocol == "tcp" {
				conn, e := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "tcp", address)
				if e != nil {
					ready = false
				} else {
					conn.Close()
				}
			}
		}
		if ready {
			return endpoints, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("访问网关入口健康检查超时")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
