// Package environment validates the platform package contract. Validation is
// deliberately separate from image import and runtime readiness.
package environment

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type Manifest struct {
	FormatVersion int       `json:"format_version" yaml:"format_version"`
	Version       string    `json:"version" yaml:"version"`
	Architecture  string    `json:"architecture" yaml:"architecture"`
	Images        []Image   `json:"images" yaml:"images"`
	Services      []Service `json:"services" yaml:"services"`
}
type Image struct {
	SourceFile string `json:"source_file,omitempty" yaml:"source_file,omitempty"`
	Name       string `json:"name,omitempty" yaml:"name,omitempty"`
	Key        string `json:"key" yaml:"key"`
	File       string `json:"file" yaml:"file"`
	SHA256     string `json:"sha256" yaml:"sha256"`
}
type Service struct {
	ResourceLimitsEnabled bool     `json:"resource_limits_enabled,omitempty" yaml:"resource_limits_enabled,omitempty"`
	PidsLimit             int64    `json:"pids_limit,omitempty" yaml:"pids_limit,omitempty"`
	ID                    string   `json:"id,omitempty" yaml:"-"`
	Name                  string   `json:"name,omitempty" yaml:"name,omitempty"`
	Key                   string   `json:"key" yaml:"key"`
	Image                 string   `json:"image" yaml:"image"`
	DependsOn             []string `json:"depends_on" yaml:"depends_on"`
	MemoryMB              int      `json:"memory_mb" yaml:"memory_mb"`
	CPU                   float64  `json:"cpu" yaml:"cpu"`
	Ports                 []Port   `json:"ports" yaml:"ports"`
}
type Port struct {
	Port     int    `json:"port" yaml:"port"`
	Protocol string `json:"protocol" yaml:"protocol"`
	Public   bool   `json:"public" yaml:"public"`
	Label    string `json:"label" yaml:"label"`
}

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func CleanFile(name string) bool {
	return name != "" && name == path.Clean(name) && !strings.HasPrefix(name, "/") && name != ".." && !strings.HasPrefix(name, "../") && !strings.ContainsAny(name, "\\:\x00")
}
func Parse(data []byte) (Manifest, error) {
	var m Manifest
	if len(data) > 64<<10 {
		return m, fmt.Errorf("manifest.yaml 超过 64 KiB")
	}
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	if err := d.Decode(&m); err != nil {
		return m, fmt.Errorf("环境描述格式错误: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return m, fmt.Errorf("仅允许一个 YAML 文档")
	}
	return m, m.Validate()
}
func (m Manifest) Validate() error {
	if m.FormatVersion != 1 || strings.TrimSpace(m.Version) == "" || len(m.Version) > 64 {
		return fmt.Errorf("环境格式版本须为 1，且须填写环境版本")
	}
	if m.Architecture != "amd64" && m.Architecture != "arm64" {
		return fmt.Errorf("架构须为 amd64 或 arm64")
	}
	if len(m.Services) < 1 || len(m.Services) > 16 || len(m.Images) < 1 || len(m.Images) > 16 {
		return fmt.Errorf("环境须包含 1–16 个镜像和服务")
	}
	images := map[string]bool{}
	files := map[string]bool{}
	for _, im := range m.Images {
		b, err := hex.DecodeString(im.SHA256)
		if utf8.RuneCountInString(im.SourceFile) > 255 {
			return fmt.Errorf("上传文件名最长 255 字符")
		}
		if utf8.RuneCountInString(im.Name) > 128 {
			return fmt.Errorf("镜像名称最长 128 字符")
		}
		if !keyPattern.MatchString(im.Key) || images[im.Key] || !CleanFile(im.File) || !strings.HasPrefix(im.File, "images/") || !strings.HasSuffix(im.File, ".tar") || files[im.File] || err != nil || len(b) != 32 || im.SHA256 != strings.ToLower(im.SHA256) {
			return fmt.Errorf("镜像标识、归档路径或 SHA256 无效/重复")
		}
		images[im.Key] = true
		files[im.File] = true
	}
	services := map[string]Service{}
	for _, s := range m.Services {
		if utf8.RuneCountInString(s.Name) > 128 {
			return fmt.Errorf("服务名称最长 128 字符")
		}
		if !keyPattern.MatchString(s.Key) || !images[s.Image] || s.MemoryMB < 0 || (s.MemoryMB > 0 && s.MemoryMB < 16) || s.MemoryMB > 16384 || math.IsNaN(s.CPU) || math.IsInf(s.CPU, 0) || s.CPU < 0 || (s.CPU > 0 && s.CPU < 0.1) || s.CPU > 4096 || s.PidsLimit < 0 || s.PidsLimit > 1048576 || len(s.Ports) > 32 || len(s.DependsOn) > 16 {
			return fmt.Errorf("服务 %s 的镜像、资源或标识无效", s.Key)
		}
		if _, ok := services[s.Key]; ok {
			return fmt.Errorf("服务标识重复")
		}
		services[s.Key] = s
		ports := map[string]bool{}
		for _, p := range s.Ports {
			k := fmt.Sprintf("%d/%s", p.Port, p.Protocol)
			if p.Port < 1 || p.Port > 65535 || (p.Protocol != "tcp" && p.Protocol != "udp") || ports[k] || len(p.Label) > 128 {
				return fmt.Errorf("服务 %s 的端口无效或重复", s.Key)
			}
			ports[k] = true
		}
	}
	visited := map[string]int{}
	var visit func(string) error
	visit = func(key string) error {
		if visited[key] == 1 {
			return fmt.Errorf("服务依赖存在环")
		}
		if visited[key] == 2 {
			return nil
		}
		s, ok := services[key]
		if !ok {
			return fmt.Errorf("依赖引用了不存在的服务 %s", key)
		}
		visited[key] = 1
		seen := map[string]bool{}
		for _, dep := range s.DependsOn {
			if seen[dep] {
				return fmt.Errorf("服务依赖重复")
			}
			seen[dep] = true
			if err := visit(dep); err != nil {
				return err
			}
		}
		visited[key] = 2
		return nil
	}
	for key := range services {
		if err := visit(key); err != nil {
			return err
		}
	}
	return nil
}

// SamePackageImages permits runtime configuration overrides while preserving
// the immutable image identity and architecture verified from the archive.
func SamePackageImages(a, b Manifest) bool {
	if a.FormatVersion != b.FormatVersion || a.Architecture != b.Architecture || len(a.Images) != len(b.Images) {
		return false
	}
	images := map[string]Image{}
	for _, image := range a.Images {
		images[image.Key] = image
	}
	for _, image := range b.Images {
		if original, ok := images[image.Key]; !ok || original.Key != image.Key || original.File != image.File || original.SHA256 != image.SHA256 {
			return false
		}
	}
	return true
}

// AssignServiceIDs derives stable platform identities independently of display
// names, while retaining package keys for DNS and dependency compatibility.
func (m *Manifest) AssignServiceIDs(packageSHA string) {
	for i := range m.Services {
		if m.Services[i].ID != "" {
			continue
		}
		sum := sha256.Sum256([]byte("huashan-service\x00" + packageSHA + "\x00" + m.Services[i].Key))
		m.Services[i].ID = "svc-" + hex.EncodeToString(sum[:16])
	}
}
