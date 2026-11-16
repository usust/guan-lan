package main

import (
	"bufio"
	"fmt"
	dockerruntime "huashan/internal/runtime"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type config struct {
	Root              string
	Listen            string
	Username          string
	Password          string
	SecureCookie      bool
	AllowedOrigin     string
	DockerEndpoint    string
	RuntimePublicHost string
	MinFreeBytes      uint64
}

func loadConfig(path string) (config, error) {
	cfg, err := readConfig(path)
	if err != nil {
		return config{}, fmt.Errorf("config %q: %w", path, err)
	}
	return cfg, nil
}

func readConfig(path string) (config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return config{}, err
	}

	cfg := &config{
		Listen: "127.0.0.1:8080", Username: "admin",
		AllowedOrigin:     "http://localhost:5173",
		RuntimePublicHost: "127.0.0.1",
		MinFreeBytes:      256 << 20,
	}
	fields := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(strings.TrimPrefix(string(data), "\ufeff")))
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") || strings.HasPrefix(text, ";") {
			continue
		}
		if text == "[huashan]" {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return config{}, fmt.Errorf("INI line %d: expected key=value", line)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if fields[key] {
			return config{}, fmt.Errorf("INI line %d: duplicate key %q", line, key)
		}
		fields[key] = true
		if strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "'") {
			quote := value[0]
			if len(value) < 2 || value[len(value)-1] != quote {
				return config{}, fmt.Errorf("INI line %d: unterminated quoted value", line)
			}
			value = value[1 : len(value)-1]
		}
		switch key {
		case "root":
			cfg.Root = value
		case "listen":
			cfg.Listen = value
		case "username":
			cfg.Username = value
		case "password":
			cfg.Password = value
		case "secure_cookie":
			cfg.SecureCookie, err = strconv.ParseBool(value)
			if err != nil {
				return config{}, fmt.Errorf("INI line %d: secure_cookie must be a boolean", line)
			}
		case "allowed_origin":
			cfg.AllowedOrigin = value
		case "min_free_bytes":
			cfg.MinFreeBytes, err = strconv.ParseUint(value, 10, 64)
			if err != nil {
				return config{}, fmt.Errorf("INI line %d: min_free_bytes must be an unsigned integer", line)
			}
		case "runtime_public_host":
			cfg.RuntimePublicHost = value
		case "docker_endpoint":
			cfg.DockerEndpoint = value
		default:
			return config{}, fmt.Errorf("INI line %d: unknown key %q", line, key)
		}
	}
	if err := scanner.Err(); err != nil {
		return config{}, fmt.Errorf("read INI: %w", err)
	}
	if err := defaultConfigPaths(fields, cfg); err != nil {
		return config{}, err
	}
	if !filepath.IsAbs(cfg.Root) {
		return config{}, fmt.Errorf("root must be a non-empty absolute path")
	}
	if strings.TrimSpace(cfg.Listen) == "" {
		return config{}, fmt.Errorf("listen must not be empty")
	}
	if strings.TrimSpace(cfg.Username) == "" {
		return config{}, fmt.Errorf("username must not be empty")
	}
	if !validRuntimePublicHost(cfg.RuntimePublicHost) {
		return config{}, fmt.Errorf("runtime_public_host must be an IPv4 address or hostname without scheme or port (not 0.0.0.0)")
	}
	return *cfg, nil
}

func validRuntimePublicHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.To4() != nil && !strings.Contains(host, ":") && !ip.IsUnspecified() && !ip.IsMulticast()
	}
	if len(host) == 0 || len(host) > 253 || !strings.ContainsAny(host, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		return false
	}
	label := regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
	for _, part := range strings.Split(host, ".") {
		if !label.MatchString(part) {
			return false
		}
	}
	return true
}

func defaultConfigPaths(fields map[string]bool, cfg *config) error {
	if password := os.Getenv("HUASHAN_ADMIN_PASSWORD"); password != "" {
		cfg.Password = password
	}
	if root := os.Getenv("HUASHAN_ROOT"); root != "" {
		if fields["root"] && cfg.Root != "" && filepath.Clean(cfg.Root) != filepath.Clean(root) {
			return fmt.Errorf("HUASHAN_ROOT conflicts with configured root; remove or align the INI root before switching data directories")
		}
		cfg.Root = root
	} else if _, exists := fields["root"]; !exists {
		cfg.Root = os.Getenv("HUASHAN_ROOT")
		if cfg.Root == "" {
			dir, err := os.UserConfigDir()
			if err != nil {
				return err
			}
			cfg.Root = filepath.Join(dir, "Huashan")
		}
	}
	if _, exists := fields["docker_endpoint"]; !exists {
		cfg.DockerEndpoint = dockerruntime.DefaultEndpoint()
	}
	return nil
}
