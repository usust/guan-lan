package environment

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// WrapDockerArchive converts a docker-save archive into the platform contract.
// Layers are streamed, never extracted or retained in memory.
func WrapDockerArchive(src io.ReadSeeker, dst io.Writer) error {
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return err
	}
	type entry struct {
		Config string
		Layers []string
	}
	var entries []entry
	seen := map[string]bool{}
	files := map[string]bool{}
	tr := tar.NewReader(src)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(h.Name, "/")
		if !CleanFile(name) || seen[name] {
			return fmt.Errorf("镜像归档路径无效或重复")
		}
		seen[name] = true
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return fmt.Errorf("镜像归档禁止链接和特殊文件")
		}
		files[name] = true
		if name == "manifest.yaml" {
			return fmt.Errorf("平台环境包格式无效，请检查 manifest.yaml 和镜像摘要")
		}
		if name == "manifest.json" {
			if h.Size > 64<<10 {
				return fmt.Errorf("镜像清单过大")
			}
			data, err := io.ReadAll(tr)
			if err != nil {
				return err
			}
			if err = json.Unmarshal(data, &entries); err != nil {
				return fmt.Errorf("Docker 镜像清单无效: %w", err)
			}
		}
	}
	// Reject hidden payloads after the TAR terminator.
	buffer := make([]byte, 32<<10)
	for {
		n, err := src.Read(buffer)
		for _, b := range buffer[:n] {
			if b != 0 {
				return fmt.Errorf("TAR 结束标记后包含额外内容")
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if len(entries) != 1 {
		return fmt.Errorf("请上传 docker save 导出的单镜像 TAR，或包含 manifest.yaml 的平台环境包；不支持纯 OCI 或多镜像归档")
	}
	image := entries[0]
	if !CleanFile(image.Config) || !files[image.Config] || len(image.Layers) == 0 {
		return fmt.Errorf("Docker 镜像缺少配置或镜像层")
	}
	for _, layer := range image.Layers {
		if !CleanFile(layer) || !files[layer] {
			return fmt.Errorf("Docker 镜像缺少层 %s", layer)
		}
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var config struct {
		Architecture string
		OS           string `json:"os"`
		Config       struct{ ExposedPorts map[string]json.RawMessage }
	}
	tr = tar.NewReader(src)
	for {
		h, err := tr.Next()
		if err != nil {
			return fmt.Errorf("读取镜像配置失败: %w", err)
		}
		if h.Name != image.Config {
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA || h.Size > 1<<20 {
			return fmt.Errorf("镜像配置无效或超过 1 MiB")
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(data, &config); err != nil {
			return err
		}
		break
	}
	if config.OS != "linux" {
		return fmt.Errorf("仅支持 Linux 容器镜像")
	}
	ports := []Port{}
	keys := make([]string, 0, len(config.Config.ExposedPorts))
	for key := range config.Config.ExposedPorts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts := strings.Split(key, "/")
		if len(parts) != 2 {
			return fmt.Errorf("镜像声明的端口无效: %s", key)
		}
		port, err := strconv.Atoi(parts[0])
		if err != nil {
			return err
		}
		ports = append(ports, Port{Port: port, Protocol: parts[1], Public: true})
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return err
	}
	sum := sha256.New()
	size, err := io.Copy(sum, src)
	if err != nil {
		return err
	}
	m := Manifest{FormatVersion: 1, Version: "1.0", Architecture: config.Architecture, Images: []Image{{Key: "image", File: "images/image.tar", SHA256: fmt.Sprintf("%x", sum.Sum(nil))}}, Services: []Service{{Key: "container", Image: "image", Ports: ports}}}
	if err = m.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	if _, err = src.Seek(0, io.SeekStart); err != nil {
		return err
	}
	tw := tar.NewWriter(dst)
	if err = tw.WriteHeader(&tar.Header{Name: "manifest.yaml", Mode: 0600, Size: int64(len(data))}); err != nil {
		return err
	}
	if _, err = tw.Write(data); err != nil {
		return err
	}
	if err = tw.WriteHeader(&tar.Header{Name: "images/image.tar", Mode: 0600, Size: size}); err != nil {
		return err
	}
	if _, err = io.CopyN(tw, src, size); err != nil {
		return err
	}
	return tw.Close()
}
