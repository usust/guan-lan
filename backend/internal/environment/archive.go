package environment

import (
	"archive/tar"
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
)

// Inspect never extracts or executes archive members. Image digests cover the
// entire nested image archive, not just its metadata.
func Inspect(r io.Reader) (Manifest, error) {
	var empty Manifest
	tr := tar.NewReader(r)
	seen := map[string]bool{}
	digests := map[string]string{}
	var data []byte
	for count := 0; ; count++ {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return empty, fmt.Errorf("TAR 文件损坏: %w", err)
		}
		if count >= 128 {
			return empty, fmt.Errorf("环境包文件数量超过 128")
		}
		name := strings.TrimSuffix(h.Name, "/")
		if !CleanFile(name) || seen[name] {
			return empty, fmt.Errorf("环境包路径无效或重复")
		}
		seen[name] = true
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return empty, fmt.Errorf("环境包禁止链接和特殊文件")
		}
		if h.Size < 0 {
			return empty, fmt.Errorf("环境包过大")
		}
		switch {
		case name == "manifest.yaml":
			if h.Size > 64<<10 {
				return empty, fmt.Errorf("manifest.yaml 过大")
			}
			data, err = io.ReadAll(tr)
		case strings.HasPrefix(name, "images/") && strings.HasSuffix(name, ".tar"):
			sum := sha256.New()
			_, err = io.Copy(sum, tr)
			digests[name] = fmt.Sprintf("%x", sum.Sum(nil))
		case name == "README.md" || name == "checksums.sha256":
			_, err = io.Copy(io.Discard, tr)
		default:
			return empty, fmt.Errorf("当前包格式不支持文件 %s", name)
		}
		if err != nil {
			return empty, err
		}
	}
	// Only zero padding may follow the archive terminator. A second archive
	// or hidden trailing payload is not part of the validated package.
	padding := make([]byte, 32<<10)
	for {
		n, err := r.Read(padding)
		for _, b := range padding[:n] {
			if b != 0 {
				return empty, fmt.Errorf("TAR 结束标记后包含额外内容")
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return empty, err
		}
	}
	if len(data) == 0 {
		return empty, fmt.Errorf("缺少 manifest.yaml；普通镜像 TAR 不包含完整环境拓扑")
	}
	m, err := Parse(data)
	if err != nil {
		return empty, err
	}
	for _, im := range m.Images {
		if digests[im.File] != im.SHA256 {
			return empty, fmt.Errorf("镜像 %s 缺失或 SHA256 不匹配", im.Key)
		}
	}
	if len(digests) != len(m.Images) {
		return empty, fmt.Errorf("存在未在 manifest.yaml 声明的镜像")
	}
	return m, nil
}
