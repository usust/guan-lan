package api

import (
	"context"
	"database/sql"
	"errors"
	runtimepkg "huashan/internal/runtime"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func runtimeError(c *gin.Context, err error) {
	fail(c, http.StatusConflict, "RUNTIME_FAILED", err.Error())
}
func (s *Server) registerRuntime(secured, admin *gin.RouterGroup) {
	admin.POST("/sets/:id/questions/:question/verify", func(c *gin.Context) {
		_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(82 * time.Minute))
		var input struct {
			Version int `json:"version"`
		}
		if !bindCompetition(c, &input) {
			return
		}
		// Scope the question to the requested set before any Engine operation.
		var n int
		if err := s.auth.DB.QueryRowContext(c.Request.Context(), "SELECT count(*) FROM challenges WHERE id=? AND set_id=?", c.Param("question"), c.Param("id")).Scan(&n); err != nil {
			handleError(c, err)
			return
		}
		if n != 1 {
			competitionError(c, sql.ErrNoRows)
			return
		}
		if s.runtime == nil {
			fail(c, 503, "RUNTIME_UNAVAILABLE", "Docker Socket 配置无效")
			return
		}
		if strings.Contains(c.GetHeader("Accept"), "application/x-ndjson") {
			c.Header("Content-Type", "application/x-ndjson")
			c.Header("Cache-Control", "no-cache")
			c.Header("X-Accel-Buffering", "no")
			send, stopStream := verificationStream(c.Request.Context(), c.Writer, 10*time.Second)
			defer stopStream()
			ctx := runtimepkg.WithProgress(c.Request.Context(), func(message string) { send("progress", message) })
			send("progress", "开始运行验收（按镜像健康检查配置等待，每个系统最多 5 分钟，清理最多 15 秒）")
			if err := s.runtime.Verify(ctx, c.Param("question"), input.Version); err != nil {
				message := err.Error()
				if errors.Is(err, context.DeadlineExceeded) {
					message = "运行验收超时：" + message
				}
				send("error", message)
			} else {
				send("done", "运行验收通过，测试资源已清理")
			}
			return
		}
		if err := s.runtime.Verify(c.Request.Context(), c.Param("question"), input.Version); err != nil {
			runtimeError(c, err)
			return
		}
		s.getSet(c)
	})
	secured.GET("/subtasks/:id/runtime", func(c *gin.Context) {
		if !s.runtimeAccess(c) {
			return
		}
		x, err := s.runtime.Status(c.Request.Context(), actor(c), c.Param("id"))
		if err != nil {
			runtimeError(c, err)
			return
		}
		ok(c, 200, x)
	})
	secured.POST("/subtasks/:id/runtime", func(c *gin.Context) {
		if !s.runtimeAccess(c) {
			return
		}
		_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(82 * time.Minute))
		if strings.Contains(c.GetHeader("Accept"), "application/x-ndjson") {
			c.Header("Content-Type", "application/x-ndjson")
			c.Header("Cache-Control", "no-cache")
			c.Header("X-Accel-Buffering", "no")
			send, stop := verificationStream(c.Request.Context(), c.Writer, 10*time.Second)
			defer stop()
			ctx := runtimepkg.WithProgress(c.Request.Context(), func(message string) { send("progress", message) })
			x, err := s.runtime.Start(ctx, actor(c), c.Param("id"))
			if err != nil {
				send("error", err.Error())
				return
			}
			send("done", "系统已就绪", x)
			return
		}
		x, err := s.runtime.Start(c.Request.Context(), actor(c), c.Param("id"))
		if err != nil {
			runtimeError(c, err)
			return
		}
		ok(c, 200, x)
	})
	secured.DELETE("/subtasks/:id/runtime", func(c *gin.Context) {
		if !s.runtimeAccess(c) {
			return
		}
		if err := s.runtime.Stop(c.Request.Context(), actor(c), c.Param("id")); err != nil {
			runtimeError(c, err)
			return
		}
		ok(c, 200, gin.H{"stopped": true})
	})
}
func (s *Server) runtimeAccess(c *gin.Context) bool {
	if s.runtime == nil {
		fail(c, 503, "RUNTIME_UNAVAILABLE", "Docker Socket 配置无效")
		return false
	}
	if isAdmin(c) {
		fail(c, 403, "PLAYER_REQUIRED", "管理员请使用运行验收；开题需使用选手账号")
		return false
	}
	var n int
	err := s.auth.DB.QueryRowContext(c.Request.Context(), `SELECT count(*) FROM subtasks t JOIN subtask_resources r ON r.subtask_id=t.id JOIN challenges q ON q.id=t.challenge_id JOIN challenge_sets s ON q.set_id=s.id WHERE t.id=? AND r.service_keys_json NOT IN ('null','[]') AND s.status='PUBLISHED' AND q.environment_json!='null'`, c.Param("id")).Scan(&n)
	if err != nil {
		handleError(c, err)
		return false
	}
	if n != 1 {
		competitionError(c, sql.ErrNoRows)
		return false
	}
	return true
}
