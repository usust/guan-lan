package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"huashan/internal/environment"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type dockerAPIError struct {
	Status  int
	Message string
}

func (e *dockerAPIError) Error() string { return e.Message }

func imageCachePath(temp, engine, architecture, digest string) string {
	sum := sha256.Sum256([]byte(engine + "\x00" + architecture + "\x00" + digest))
	return filepath.Join(temp, "verified-image-cache", hex.EncodeToString(sum[:]))
}
func immutableImageID(id string) bool {
	if !strings.HasPrefix(id, "sha256:") || len(id) != 71 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(id, "sha256:"))
	return err == nil
}
func (e *Engine) cachedImage(ctx context.Context, temp, engine, architecture string, image environment.Image) (string, bool, error) {
	data, err := os.ReadFile(imageCachePath(temp, engine, architecture, image.SHA256))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	id := string(data)
	if !immutableImageID(id) {
		return "", false, nil
	}
	var info struct {
		ID           string `json:"Id"`
		Architecture string
		Os           string
	}
	if err = e.call(ctx, "GET", "/images/"+id+"/json", nil, &info); err != nil {
		var apiError *dockerAPIError
		if errors.As(err, &apiError) && apiError.Status == http.StatusNotFound {
			return "", false, nil
		}
		return "", false, err
	}
	if info.ID != id || info.Os != "linux" || info.Architecture != architecture {
		return "", false, nil
	}
	return id, true, nil
}
func rememberImage(temp, engine, architecture, digest, id string) error {
	if !immutableImageID(id) {
		return fmt.Errorf("Docker 返回的镜像 ID 无效")
	}
	path := imageCachePath(temp, engine, architecture, digest)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".image-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(id)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}

// ReadyImages reuses verified, immutable image IDs without opening the TAR.
// A cache miss/deleted image triggers package verification and selective import.
func (e *Engine) ReadyImages(ctx context.Context, packagePath, expectedSHA, temp string, m environment.Manifest) (map[string]string, error) {
	engine, err := e.Identity(ctx)
	if err != nil {
		return nil, err
	}
	ids := map[string]string{}
	for _, image := range m.Images {
		id, ok, err := e.cachedImage(ctx, temp, engine, m.Architecture, image)
		if err != nil {
			return nil, err
		}
		if !ok {
			progress(ctx, "本机镜像缺失，正在从已验证环境包恢复")
			return e.importImages(ctx, packagePath, expectedSHA, temp, m, engine)
		}
		ids[image.Key] = id
	}
	progress(ctx, "已复用本机镜像，直接创建新容器")
	return ids, nil
}
