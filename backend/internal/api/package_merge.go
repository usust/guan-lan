package api

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"huashan/internal/auth"
	"huashan/internal/environment"
	"io"
	"os"
	"path/filepath"
	"time"
)

func (s *Server) mergePackages(c *gin.Context) {
	var input struct {
		BaseToken   string               `json:"base_token"`
		QuestionID  string               `json:"question_id"`
		Token       string               `json:"package_token"`
		Environment environment.Manifest `json:"environment"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		fail(c, 400, "INVALID_INPUT", "合并参数无效")
		return
	}
	load := func(token string) (environment.Manifest, string, string, error) {
		var m environment.Manifest
		var raw, path, sha string
		err := s.auth.DB.QueryRowContext(c.Request.Context(), "SELECT environment_json,package_path,package_sha256 FROM staged_packages WHERE token=? AND actor_id=? AND expires_at>?", token, actor(c), time.Now().UnixMilli()).Scan(&raw, &path, &sha)
		if err == nil {
			err = json.Unmarshal([]byte(raw), &m)
		}
		return m, path, sha, err
	}
	base, basePath, baseSHA, err := load(input.BaseToken)
	if input.BaseToken == "" {
		var raw string
		err = s.auth.DB.QueryRowContext(c.Request.Context(), "SELECT q.environment_json,q.package_path,q.package_sha256 FROM challenges q JOIN challenge_sets s ON s.id=q.set_id WHERE q.id=? AND s.status IN ('DRAFT','CLOSED')", input.QuestionID).Scan(&raw, &basePath, &baseSHA)
		if err == nil {
			err = json.Unmarshal([]byte(raw), &base)
		}
	}
	if err != nil {
		fail(c, 400, "INVALID_PACKAGE", "原环境不存在或暂存已过期，请刷新后重试")
		return
	}
	added, addedPath, addedSHA, err := load(input.Token)
	if err != nil {
		fail(c, 400, "INVALID_PACKAGE", "新增环境包不存在或暂存已过期")
		return
	}
	if err = input.Environment.Validate(); err != nil || !environment.SamePackageImages(base, input.Environment) {
		fail(c, 400, "INVALID_PACKAGE", "原环境配置与已上传镜像不一致")
		return
	}
	base.AssignServiceIDs(baseSHA)
	for i := range input.Environment.Services {
		input.Environment.Services[i].ID = ""
		for _, original := range base.Services {
			if original.Key == input.Environment.Services[i].Key {
				input.Environment.Services[i].ID = original.ID
				break
			}
		}
	}
	base = input.Environment
	base.AssignServiceIDs(baseSHA)
	added.AssignServiceIDs(addedSHA)
	merged, paths, err := environment.Merge(base, added)
	if err != nil {
		fail(c, 400, "INVALID_PACKAGE", err.Error())
		return
	}
	openVerified := func(name, sha string) (*os.File, error) {
		if filepath.Base(name) != name {
			return nil, fmt.Errorf("环境包路径无效")
		}
		f, e := os.Open(filepath.Join(s.root.Path, "uploads/packages", name))
		if e != nil {
			return nil, e
		}
		hash := sha256.New()
		_, e = environment.Inspect(io.TeeReader(f, hash))
		if e == nil && fmt.Sprintf("%x", hash.Sum(nil)) != sha {
			e = fmt.Errorf("环境包摘要不一致")
		}
		if e == nil {
			_, e = f.Seek(0, 0)
		}
		if e != nil {
			f.Close()
			return nil, e
		}
		return f, nil
	}
	a, err := openVerified(basePath, baseSHA)
	if err != nil {
		handleError(c, err)
		return
	}
	defer a.Close()
	b, err := openVerified(addedPath, addedSHA)
	if err != nil {
		handleError(c, err)
		return
	}
	defer b.Close()
	name := auth.RandomToken() + ".tar"
	target := filepath.Join(s.root.Path, "uploads/packages", name)
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		handleError(c, err)
		return
	}
	retained := false
	defer func() {
		if !retained {
			os.Remove(target)
		}
	}()
	hash := sha256.New()
	err = environment.WriteMerged(io.MultiWriter(f, hash), merged, a, b, paths)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		handleError(c, err)
		return
	}
	digest := fmt.Sprintf("%x", hash.Sum(nil))
	token := auth.RandomToken()
	raw, _ := json.Marshal(merged)
	_, err = s.auth.DB.ExecContext(c.Request.Context(), "INSERT INTO staged_packages(token,actor_id,environment_json,package_path,package_sha256,expires_at) VALUES(?,?,?,?,?,?)", token, actor(c), string(raw), name, digest, time.Now().Add(7*24*time.Hour).UnixMilli())
	if err != nil {
		handleError(c, err)
		return
	}
	retained = true
	ok(c, 200, gin.H{"package_token": token, "environment": merged, "package_sha256": digest})
}
