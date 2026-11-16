package api

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"huashan/internal/auth"
	"huashan/internal/competition"
	"huashan/internal/environment"
	"huashan/internal/storage"
)

func (s *Server) competitions() *competition.Service { return &competition.Service{DB: s.auth.DB} }
func isAdmin(c *gin.Context) bool                    { return c.MustGet("user").(auth.User).Role == "ADMIN" }
func actor(c *gin.Context) string                    { return c.MustGet("user").(auth.User).ID }
func competitionError(c *gin.Context, err error) {
	var v *competition.ValidationError
	switch {
	case errors.As(err, &v):
		fail(c, 400, "INVALID_COMPETITION", v.Error())
	case errors.Is(err, competition.ErrConflict):
		fail(c, 409, "CONTENT_CONFLICT", err.Error())
	case errors.Is(err, competition.ErrClosed):
		fail(c, 409, "SUBMISSION_CLOSED", err.Error())
	case errors.Is(err, competition.ErrRate):
		c.Header("Retry-After", "60")
		fail(c, 429, "SUBMISSION_RATE_LIMITED", err.Error())
	case errors.Is(err, sql.ErrNoRows):
		fail(c, 404, "NOT_FOUND", "套题、题目或小题不存在")
	default:
		handleError(c, err)
	}
}
func bindCompetition(c *gin.Context, input any) bool {
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(input); err != nil {
		fail(c, 400, "INVALID_INPUT", "请求格式或字段无效")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		fail(c, 400, "INVALID_INPUT", "只允许一个 JSON 对象")
		return false
	}
	return true
}
func (s *Server) registerCompetitions(secured, admin *gin.RouterGroup) {
	s.registerTaxonomy(admin)
	admin.GET("/questions", func(c *gin.Context) {
		items, err := s.competitions().ListLibrary(c.Request.Context())
		if err != nil {
			competitionError(c, err)
			return
		}
		ok(c, 200, gin.H{"items": items})
	})
	secured.GET("/sets", func(c *gin.Context) {
		items, err := s.competitions().List(c.Request.Context(), isAdmin(c))
		if err != nil {
			competitionError(c, err)
			return
		}
		ok(c, 200, gin.H{"items": items})
	})
	secured.GET("/sets/:id", s.getSet)
	secured.GET("/sets/:id/leaderboard", func(c *gin.Context) {
		items, err := s.competitions().Leaderboard(c.Request.Context(), c.Param("id"), isAdmin(c))
		if err != nil {
			competitionError(c, err)
			return
		}
		ok(c, 200, gin.H{"items": items})
	})
	secured.GET("/sets/:id/submissions", func(c *gin.Context) {
		items, err := s.competitions().History(c.Request.Context(), actor(c), c.Param("id"))
		if err != nil {
			competitionError(c, err)
			return
		}
		ok(c, 200, gin.H{"items": items})
	})
	secured.POST("/subtasks/:id/submissions", func(c *gin.Context) {
		if isAdmin(c) {
			fail(c, 403, "PLAYER_REQUIRED", "请使用选手账号提交，避免管理员测试进入正式成绩")
			return
		}
		var input struct {
			Flag string `json:"flag"`
		}
		if !bindCompetition(c, &input) {
			return
		}
		result, err := s.competitions().Submit(c.Request.Context(), actor(c), c.Param("id"), input.Flag)
		if err != nil {
			competitionError(c, err)
			return
		}
		ok(c, 200, result)
	})
	admin.POST("/sets", s.saveSet)
	admin.PUT("/sets/:id", s.saveSet)
	admin.PUT("/sets/:id/info", func(c *gin.Context) {
		var input struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			StartsAt    int64  `json:"starts_at"`
			EndsAt      int64  `json:"ends_at"`
			Version     int    `json:"version"`
		}
		if !bindCompetition(c, &input) {
			return
		}
		if err := s.competitions().UpdateInfo(c.Request.Context(), actor(c), c.Param("id"), competition.Set{Title: input.Title, Description: input.Description, StartsAt: input.StartsAt, EndsAt: input.EndsAt, Version: input.Version}); err != nil {
			competitionError(c, err)
			return
		}
		s.getSet(c)
	})
	admin.POST("/sets/:id/status", func(c *gin.Context) {
		var input struct {
			Status  string `json:"status"`
			Version int    `json:"version"`
		}
		if !bindCompetition(c, &input) {
			return
		}
		service := s.competitions()
		if input.Status == "PUBLISHED" {
			x, err := service.Get(c.Request.Context(), c.Param("id"), actor(c), true)
			if err != nil {
				competitionError(c, err)
				return
			}
			for _, q := range x.Questions {
				if q.Environment != nil {
					if !q.RuntimeReady {
						competitionError(c, &competition.ValidationError{Message: "请先保存修改，并对每道容器题执行「运行验收」，通过后再发布"})
						return
					}
					if s.runtime == nil {
						fail(c, 503, "RUNTIME_UNAVAILABLE", "Docker Socket 配置无效")
						return
					}
					service.RuntimeEngineID, err = s.runtime.Engine.Identity(c.Request.Context())
					if err != nil {
						runtimeError(c, err)
						return
					}
					break
				}
			}
		}
		if err := service.Transition(c.Request.Context(), actor(c), c.Param("id"), input.Status, input.Version); err != nil {
			competitionError(c, err)
			return
		}
		s.getSet(c)
	})
	admin.DELETE("/sets/:id", func(c *gin.Context) {
		version, err := strconv.Atoi(c.Query("version"))
		if err != nil {
			fail(c, 400, "INVALID_INPUT", "缺少有效的内容版本")
			return
		}
		paths := s.packagePaths(c.Param("id"))
		if err = s.competitions().Delete(c.Request.Context(), actor(c), c.Param("id"), version); err != nil {
			competitionError(c, err)
			return
		}
		s.prunePackages(paths)
		ok(c, 200, gin.H{"deleted": true})
	})
	admin.POST("/sets/:id/questions/:question/package", s.uploadPackage)
	admin.POST("/packages", s.uploadPackage)
	admin.POST("/packages/merge", s.mergePackages)
	s.registerPackageUploads(admin)
	s.registerAttachmentUploads(admin)
	secured.GET("/attachments/:file", s.downloadStoredAttachment)
	s.registerRuntime(secured, admin)
}
func (s *Server) getSet(c *gin.Context) {
	var visible bool
	err := s.auth.DB.QueryRowContext(c.Request.Context(), `SELECT EXISTS(SELECT 1 FROM challenge_sets s WHERE s.id=? AND (? OR (s.status='PUBLISHED' AND NOT EXISTS(SELECT 1 FROM question_library l WHERE l.set_id=s.id))))`, c.Param("id"), isAdmin(c)).Scan(&visible)
	if err != nil {
		handleError(c, err)
		return
	}
	if !visible {
		competitionError(c, sql.ErrNoRows)
		return
	}
	if err = s.migrateLegacyAttachments(c.Request.Context(), c.Param("id"), actor(c)); err != nil {
		handleError(c, err)
		return
	}
	x, err := s.competitions().Get(c.Request.Context(), c.Param("id"), actor(c), isAdmin(c))
	if err != nil {
		competitionError(c, err)
		return
	}

	ok(c, 200, gin.H{"set": x, "runtime_available": s.runtime != nil, "runtime_message": "容器题先运行验收再发布；每位选手独立环境，公开端口直接映射到 Docker 主机所有 IPv4 网卡。外网访问需配置公网地址和防火墙；端口不提供选手身份鉴权。"})
}
func (s *Server) saveSet(c *gin.Context) {
	var x competition.Set
	if !bindCompetition(c, &x) {
		return
	}
	if x.ID != "" && x.ID != c.Param("id") {
		fail(c, 400, "INVALID_INPUT", "请求路径与套题标识不一致")
		return
	}
	x.ID = c.Param("id")
	paths := s.packagePaths(x.ID)
	id, err := s.competitions().Save(c.Request.Context(), actor(c), x)
	if err != nil {
		competitionError(c, err)
		return
	}
	s.prunePackages(paths)
	if err = s.migrateLegacyAttachments(c.Request.Context(), id, actor(c)); err != nil {
		handleError(c, err)
		return
	}
	x, err = s.competitions().Get(c.Request.Context(), id, actor(c), true)
	if err != nil {
		competitionError(c, err)
		return
	}
	status := 200
	if c.Request.Method == http.MethodPost {
		status = 201
	}
	ok(c, status, gin.H{"set": x})
}
func (s *Server) packagePaths(set string) []string {
	rows, err := s.auth.DB.Query("SELECT package_path FROM challenges WHERE set_id=? AND package_path!=''", set)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			paths = append(paths, p)
		}
	}
	return paths
}
func (s *Server) prunePackages(paths []string) {
	for _, p := range paths {
		// Only platform-generated filenames in the private package directory.
		if len(p) != 68 || filepath.Base(p) != p || filepath.Ext(p) != ".tar" {
			continue
		}
		if _, err := hex.DecodeString(p[:64]); err != nil {
			continue
		}
		var n int
		if s.auth.DB.QueryRow("SELECT (SELECT count(*) FROM challenges WHERE package_path=?) + (SELECT count(*) FROM staged_packages WHERE package_path=? AND expires_at>?)", p, p, time.Now().UnixMilli()).Scan(&n) == nil && n == 0 {
			_ = os.Remove(filepath.Join(s.root.Path, "uploads/packages", p))
		}
	}
}
func (s *Server) uploadPackage(c *gin.Context) {
	select {
	case s.packageSlots <- struct{}{}:
		defer func() { <-s.packageSlots }()
	default:
		fail(c, 429, "PACKAGE_UPLOAD_BUSY", "已有环境包正在校验，请稍后重试")
		return
	}
	var version int
	var err error
	if c.Param("id") != "" {
		version, err = strconv.Atoi(c.GetHeader("X-Content-Version"))
		if err != nil {
			fail(c, 400, "INVALID_INPUT", "上传须携带当前内容版本")
			return
		}
		current, e := s.competitions().Get(c.Request.Context(), c.Param("id"), actor(c), true)
		if e != nil {
			competitionError(c, e)
			return
		}
		if current.Status != "DRAFT" || current.Version != version {
			competitionError(c, competition.ErrConflict)
			return
		}
		found := false
		for _, q := range current.Questions {
			if q.ID == c.Param("question") {
				found = true
			}
		}
		if !found {
			competitionError(c, sql.ErrNoRows)
			return
		}
	}
	if c.ContentType() != "application/x-tar" {
		fail(c, 415, "INVALID_PACKAGE_TYPE", "请上传未压缩的标准 TAR 环境包")
		return
	}
	size := c.Request.ContentLength
	if size <= 0 {
		size = 0
	}
	if err = storage.CheckDiskBudget(s.root.Path, uint64(size), s.config.MinFreeBytes); err != nil {
		fail(c, 507, "INSUFFICIENT_STORAGE", "磁盘剩余空间不足，无法安全保存环境包")
		return
	}
	f, err := os.CreateTemp(filepath.Join(s.root.Path, "temp"), "package-*")
	if err != nil {
		handleError(c, err)
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()
	sum := sha256.New()
	m, err := environment.Inspect(io.TeeReader(c.Request.Body, io.MultiWriter(f, sum)))
	if err != nil {
		fail(c, 400, "INVALID_PACKAGE", "环境包校验失败："+err.Error())
		return
	}
	if err = f.Sync(); err != nil {
		handleError(c, err)
		return
	}
	if err = f.Close(); err != nil {
		handleError(c, err)
		return
	}
	name := auth.RandomToken() + ".tar"
	target := filepath.Join(s.root.Path, "uploads/packages", name)
	if err = os.Rename(f.Name(), target); err != nil {
		handleError(c, err)
		return
	}
	m.AssignServiceIDs(fmt.Sprintf("%x", sum.Sum(nil)))
	if c.Param("id") == "" {
		token := auth.RandomToken()
		env, marshalErr := json.Marshal(m)
		if marshalErr != nil {
			_ = os.Remove(target)
			handleError(c, marshalErr)
			return
		}
		_, err = s.auth.DB.ExecContext(c.Request.Context(), "INSERT INTO staged_packages(token,actor_id,environment_json,package_path,package_sha256,expires_at) VALUES(?,?,?,?,?,?)", token, actor(c), string(env), name, fmt.Sprintf("%x", sum.Sum(nil)), time.Now().Add(24*time.Hour).UnixMilli())
		if err != nil {
			_ = os.Remove(target)
			handleError(c, err)
			return
		}
		ok(c, 200, gin.H{"package_token": token, "environment": m, "package_sha256": fmt.Sprintf("%x", sum.Sum(nil))})
		return
	}
	paths := s.packagePaths(c.Param("id"))
	if err = s.competitions().AttachPackage(c.Request.Context(), actor(c), c.Param("id"), c.Param("question"), version, m, name, fmt.Sprintf("%x", sum.Sum(nil))); err != nil {
		_ = os.Remove(target)
		competitionError(c, err)
		return
	}
	s.prunePackages(paths)
	s.getSet(c)
}

// CleanPackageOrphans removes only unreferenced platform-generated archives
// after a grace period, including files left by a crash before DB attachment.
func (s *Server) CleanPackageOrphans() {
	temp, _ := os.ReadDir(filepath.Join(s.root.Path, "temp"))
	for _, entry := range temp {
		info, err := entry.Info()
		if err == nil && info.Mode().IsRegular() && strings.HasPrefix(entry.Name(), "package-") && time.Since(info.ModTime()) > 24*time.Hour {
			_ = os.Remove(filepath.Join(s.root.Path, "temp", entry.Name()))
		}
	}
	entries, err := os.ReadDir(filepath.Join(s.root.Path, "uploads/packages"))
	if err != nil {
		return
	}
	for _, entry := range entries {
		info, e := entry.Info()
		if e != nil || !info.Mode().IsRegular() || time.Since(info.ModTime()) < 24*time.Hour {
			continue
		}
		s.prunePackages([]string{entry.Name()})
	}
}
