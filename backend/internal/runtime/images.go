package runtime

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"huashan/internal/environment"
	"huashan/internal/storage"
)

// Import checks the stored package again, replaces repository tags, and
// addresses images by the digest of their actual configuration, never a tag.
func (e *Engine) Import(ctx context.Context, packagePath, expectedSHA, temp string, m environment.Manifest) (map[string]string, error) {
	engine, err := e.Identity(ctx)
	if err != nil {
		return nil, err
	}
	return e.importImages(ctx, packagePath, expectedSHA, temp, m, engine)
}
func (e *Engine) importImages(ctx context.Context, packagePath, expectedSHA, temp string, m environment.Manifest, engine string) (map[string]string, error) {
	f, err := os.Open(packagePath)
	if err != nil {
		return nil, fmt.Errorf("读取环境包失败: %w", err)
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if err = storage.CheckDiskBudget(temp, uint64(stat.Size())*2, 256<<20); err != nil {
		return nil, fmt.Errorf("镜像导入所需临时磁盘空间不足: %w", err)
	}
	sum := sha256.New()
	inspected, err := environment.Inspect(io.TeeReader(f, sum))
	if err != nil {
		return nil, err
	}
	if fmt.Sprintf("%x", sum.Sum(nil)) != expectedSHA {
		return nil, fmt.Errorf("环境包摘要已变化，请重新上传")
	}
	if !environment.SamePackageImages(inspected, m) {
		return nil, fmt.Errorf("配置与已上传环境包不一致，请重新上传")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp(temp, "runtime-images-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)
	wanted := map[string]environment.Image{}
	for _, im := range m.Images {
		wanted[im.File] = im
	}
	ids := map[string]string{}
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		im, ok := wanted[h.Name]
		if !ok {
			continue
		}
		progress(ctx, "解包并导入镜像 "+im.Key)
		if id, ok, err := e.cachedImage(ctx, temp, engine, m.Architecture, im); err != nil {
			return nil, err
		} else if ok {
			ids[im.Key] = id
			progress(ctx, "镜像 "+im.Key+" 已存在，跳过重复导入")
			continue
		}
		raw := filepath.Join(work, im.Key+".tar")
		dst, err := os.Create(raw)
		if err != nil {
			return nil, err
		}
		_, err = io.Copy(dst, tr)
		closeErr := dst.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		id, clean, err := sanitizeImage(raw)
		if err != nil {
			return nil, fmt.Errorf("镜像 %s: %w", im.Key, err)
		}
		if err = e.load(ctx, clean); err != nil {
			return nil, fmt.Errorf("镜像 %s 导入失败: %w", im.Key, err)
		}
		var info struct {
			ID           string `json:"Id"`
			Architecture string
			Os           string
		}
		if err = e.call(ctx, "GET", "/images/"+id+"/json", nil, &info); err != nil {
			return nil, err
		}
		if !strings.HasPrefix(info.ID, "sha256:") || info.Os != "linux" || info.Architecture != m.Architecture {
			return nil, fmt.Errorf("镜像 %s 架构与环境配置不一致", im.Key)
		}
		progress(ctx, "镜像 "+im.Key+" 导入和架构检查通过")
		if err = rememberImage(temp, engine, m.Architecture, im.SHA256, info.ID); err != nil {
			return nil, err
		}
		ids[im.Key] = info.ID
	}
	if len(ids) != len(m.Images) {
		return nil, fmt.Errorf("环境包缺少镜像")
	}
	return ids, nil
}
func sanitizeImage(raw string) (string, string, error) {
	f, err := os.Open(raw)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	type entry struct {
		Config   string
		RepoTags []string
		Layers   []string
	}
	var manifest []entry
	digests := map[string]string{}
	seen := map[string]bool{}
	tr := tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", err
		}
		name := strings.TrimSuffix(h.Name, "/")
		if !environment.CleanFile(name) || seen[name] {
			return "", "", fmt.Errorf("镜像归档路径无效或重复")
		}
		seen[name] = true
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return "", "", fmt.Errorf("镜像归档禁止链接或特殊文件")
		}
		if name == "manifest.json" {
			if h.Size > 64<<10 {
				return "", "", fmt.Errorf("镜像清单过大")
			}
			if err = json.NewDecoder(tr).Decode(&manifest); err != nil {
				return "", "", err
			}
		} else {
			sum := sha256.New()
			if _, err = io.Copy(sum, tr); err != nil {
				return "", "", err
			}
			digests[name] = fmt.Sprintf("sha256:%x", sum.Sum(nil))
		}
	}
	if len(manifest) != 1 || digests[manifest[0].Config] == "" {
		return "", "", fmt.Errorf("需要 docker save 导出的单镜像 TAR（manifest.json）")
	}
	id := "huashan-import:" + strings.TrimPrefix(digests[manifest[0].Config], "sha256:")
	manifest[0].RepoTags = []string{id}
	for _, layer := range manifest[0].Layers {
		if digests[layer] == "" {
			return "", "", fmt.Errorf("镜像缺少层 %s", layer)
		}
	}
	if _, err = f.Seek(0, 0); err != nil {
		return "", "", err
	}
	clean := raw + ".clean"
	dst, err := os.Create(clean)
	if err != nil {
		return "", "", err
	}
	defer dst.Close()
	tw := tar.NewWriter(dst)
	tr = tar.NewReader(f)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", err
		}
		if h.Name == "repositories" || h.Name == "index.json" || h.Name == "oci-layout" {
			continue
		}
		var replacement []byte
		if h.Name == "manifest.json" {
			replacement, _ = json.Marshal(manifest)
			h.Size = int64(len(replacement))
		}
		if err = tw.WriteHeader(h); err != nil {
			return "", "", err
		}
		if replacement != nil {
			_, err = tw.Write(replacement)
		} else {
			_, err = io.Copy(tw, tr)
		}
		if err != nil {
			return "", "", err
		}
	}
	if err = tw.Close(); err != nil {
		return "", "", err
	}
	if err = dst.Close(); err != nil {
		return "", "", err
	}
	return id, clean, nil
}
func (e *Engine) load(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	req, err := http.NewRequestWithContext(ctx, "POST", "http://docker/images/load?quiet=1", f)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-tar")
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("Docker 导入返回 %d", resp.StatusCode)
	}
	d := json.NewDecoder(io.LimitReader(resp.Body, 4<<20))
	count := 0
	for {
		var msg struct {
			Error       string
			ErrorDetail *struct{ Message string }
		}
		err = d.Decode(&msg)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		count++
		if msg.Error != "" {
			return fmt.Errorf("%s", msg.Error)
		}
		if msg.ErrorDetail != nil {
			return fmt.Errorf("%s", msg.ErrorDetail.Message)
		}
	}
	if count == 0 {
		return fmt.Errorf("Docker 未返回镜像导入结果")
	}
	return nil
}
