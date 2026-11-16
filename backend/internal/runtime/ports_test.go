package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"huashan/internal/environment"
)

type engineTransport struct{ base string }

func (r engineTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = strings.TrimPrefix(r.base, "http://")
	return http.DefaultTransport.RoundTrip(clone)
}
func testEngine(t *testing.T, handler http.HandlerFunc) *Engine {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return &Engine{client: &http.Client{Transport: engineTransport{server.URL}}}
}

func TestCreateDirectPorts(t *testing.T) {
	for _, limited := range []bool{false, true} {
		t.Run(fmt.Sprintf("limited=%t", limited), func(t *testing.T) {
			created := 0
			networks := 0
			var config map[string]json.RawMessage
			m := &Manager{Platform: "test", PublicHost: "play.example.test"}
			m.Engine = testEngine(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "POST" && r.URL.Path == "/networks/create":
					networks++
					var body struct {
						Internal bool
						Options  map[string]string
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.Internal || body.Options["com.docker.network.bridge.gateway_mode_ipv4"] != "nat" {
						t.Error("not a published NAT network")
					}
					fmt.Fprint(w, `{"Id":"net"}`)
				case r.URL.Path == "/networks/net":
					fmt.Fprint(w, `{"Internal":false,"Driver":"bridge","Labels":{"huashan.platform":"test"},"Options":{"com.docker.network.bridge.gateway_mode_ipv4":"nat"}}`)
				case r.Method == "POST" && r.URL.Path == "/containers/create":
					created++
					if strings.Contains(r.URL.Query().Get("name"), "gateway") {
						t.Error("gateway created")
					}
					if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
						t.Error(err)
					}
					var host struct {
						PortBindings map[string][]struct{ HostIp, HostPort string }
					}
					if err := json.Unmarshal(config["HostConfig"], &host); err != nil {
						t.Error(err)
					}
					// Legacy manifest CPU/memory values must not impose runtime quotas.
					var resources map[string]json.RawMessage
					if err := json.Unmarshal(config["HostConfig"], &resources); err != nil {
						t.Fatal(err)
					}
					expectedResources := map[string]string{"Memory": "0", "MemorySwap": "-1", "NanoCpus": "0", "PidsLimit": "-1"}
					if limited {
						expectedResources = map[string]string{"Memory": "67108864", "MemorySwap": "-1", "NanoCpus": "500000000", "PidsLimit": "256"}
					}
					for key, expected := range expectedResources {
						if string(resources[key]) != expected {
							t.Errorf("unexpected %s: %s", key, resources[key])
						}
					}
					fmt.Fprint(w, `{"Id":"service"}`)
				case r.URL.Path == "/containers/service/start":
					w.WriteHeader(http.StatusNoContent)
				case r.URL.Path == "/containers/service/json":
					// Simulate Docker normalizing unlimited swap and PIDs on inspect.
					var inspected map[string]any
					if err := json.Unmarshal(config["HostConfig"], &inspected); err != nil {
						t.Error(err)
						return
					}
					if !limited {
						inspected["MemorySwap"] = 0
						inspected["PidsLimit"] = nil
					}
					json.NewEncoder(w).Encode(map[string]any{"State": map[string]any{"Running": true}, "HostConfig": inspected, "NetworkSettings": map[string]any{"Networks": map[string]any{"net": map[string]string{"NetworkID": "net"}}, "Ports": map[string]any{"53/udp": []map[string]string{{"HostIp": "0.0.0.0", "HostPort": "32123"}}}}})
				default:
					t.Errorf("unexpected Docker request: %s %s", r.Method, r.URL)
					http.Error(w, "unexpected request", 500)
				}
			})
			manifest := environment.Manifest{Services: []environment.Service{{ResourceLimitsEnabled: limited, PidsLimit: 256, Key: "web", Image: "app", MemoryMB: 64, CPU: 0.5, Ports: []environment.Port{{Port: 53, Protocol: "udp", Public: true}, {Port: 80, Protocol: "tcp"}}}}}
			result, err := m.create(context.Background(), "instance", manifest, map[string]string{"app": "sha256:test"})
			if err != nil {
				t.Fatal(err)
			}
			if created != 1 || networks != 1 {
				t.Fatalf("created %d containers and %d networks", created, networks)
			}
			if len(result.Endpoints) != 1 || result.Endpoints[0].Address != "play.example.test:32123" {
				t.Fatalf("endpoints: %+v", result.Endpoints)
			}
		})
	}
}

func TestPublishedEndpoints(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	for _, tc := range []struct {
		name, host, port   string
		running, wantError bool
	}{
		{"public hostname", "0.0.0.0", port, true, false},
		{"loopback binding", "127.0.0.1", port, true, true},
		{"empty port", "0.0.0.0", "", true, true},
		{"invalid port", "0.0.0.0", "65536", true, true},
		{"stopped", "0.0.0.0", port, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manager{PublicHost: "unresolvable.invalid"}
			m.Engine = testEngine(t, func(w http.ResponseWriter, r *http.Request) {
				json.NewEncoder(w).Encode(map[string]any{"State": map[string]bool{"Running": tc.running}, "NetworkSettings": map[string]any{"Ports": map[string]any{"80/tcp": []map[string]string{{"HostIp": tc.host, "HostPort": tc.port}}}}})
			})
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			endpoints, err := m.publishedEndpoints(ctx, environment.Manifest{Services: []environment.Service{{Key: "web", Ports: []environment.Port{{Port: 80, Protocol: "tcp", Public: true}}}}}, map[string]string{"web": "service"})
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantError && (len(endpoints) != 1 || endpoints[0].Address != "unresolvable.invalid:"+port) {
				t.Fatalf("endpoints: %+v", endpoints)
			}
		})
	}
}
