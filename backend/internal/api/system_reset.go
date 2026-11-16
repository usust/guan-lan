package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"huashan/internal/auth"
)

func (s *Server) resetSystem(c *gin.Context) {
	var input struct {
		Confirmation string   `json:"confirmation"`
		Groups       []string `json:"groups"`
	}
	if c.ShouldBindJSON(&input) != nil || input.Confirmation != "重置系统" {
		fail(c, 400, "RESET_CONFIRMATION_REQUIRED", "请输入“重置系统”确认永久删除")
		return
	}
	selected := map[string]bool{}
	allowed := map[string]bool{"content": true, "taxonomy": true, "results": true, "users": true, "runtime": true, "branding": true, "logs": true, "backups": true}
	for _, group := range input.Groups {
		if !allowed[group] {
			fail(c, 400, "INVALID_RESET_GROUP", "无效的重置数据分组")
			return
		}
		selected[group] = true
	}
	if len(selected) == 0 {
		fail(c, 400, "RESET_GROUP_REQUIRED", "请至少选择一组需要删除的数据")
		return
	}
	// Dependencies must be explicitly selected and shown before confirmation.
	if (selected["content"] || selected["users"]) && (!selected["results"] || !selected["runtime"]) {
		fail(c, 400, "RESET_DEPENDENCY_REQUIRED", "删除题目比赛或用户时，须同时选择提交成绩和运行环境")
		return
	}

	// Finish cleanup even if the browser disconnects.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	// Background package validation outlives its HTTP request.
	select {
	case s.packageSlots <- struct{}{}:
		defer func() { <-s.packageSlots }()
	default:
		fail(c, 409, "RESET_BUSY", "环境包正在校验，请等待完成后再重置")
		return
	}
	actor := c.MustGet("user").(auth.User).ID
	clear := func() error {
		var dirs []string
		if selected["content"] {
			dirs = append(dirs, "uploads/packages", "uploads/attachments", "images/archives", "challenges/templates", "temp")
		}
		if selected["runtime"] {
			dirs = append(dirs, "instances/runtime")
		}
		if selected["logs"] {
			dirs = append(dirs, "logs")
		}
		if selected["backups"] {
			dirs = append(dirs, "backups")
		}
		// Validate every parent before any deletion; never follow managed symlinks.
		for _, dir := range dirs {
			for p := filepath.Join(s.root.Path, dir); p != s.root.Path; p = filepath.Dir(p) {
				info, err := os.Lstat(p)
				if err != nil {
					return err
				}
				if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("invalid reset directory: %s", p)
				}
			}
		}
		tx, err := s.auth.DB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var queries []string
		if selected["results"] {
			queries = append(queries, "DELETE FROM solves", "DELETE FROM submissions")
		}
		if selected["runtime"] {
			queries = append(queries, "DELETE FROM runtime_bindings", "DELETE FROM runtime_instances", "DELETE FROM runtime_verifications")
		}
		if selected["content"] {
			queries = append(queries, "DELETE FROM challenge_sets", "DELETE FROM staged_packages", "DELETE FROM package_uploads", "DELETE FROM attachment_uploads")
		}
		if selected["taxonomy"] {
			queries = append(queries, "DELETE FROM knowledge_point_types", "UPDATE knowledge_points SET parent_name=NULL", "DELETE FROM knowledge_points", "UPDATE question_types SET parent_name=NULL", "DELETE FROM question_types", "DELETE FROM difficulties")
			// Remove references from retained questions and invalidate open editors.
			queries = append(queries, "UPDATE challenges SET category='',difficulty='',tags_json='[]'", "UPDATE challenge_sets SET version=version+1")
		}
		if selected["logs"] {
			queries = append(queries, "DELETE FROM audit_logs")
		}
		if selected["users"] {
			queries = append(queries, "DELETE FROM sessions")
		}
		for _, query := range queries {
			if _, err = tx.ExecContext(ctx, query); err != nil {
				return err
			}
		}
		if selected["users"] {
			// Retained audit history no longer identifies deleted users.
			if _, err = tx.ExecContext(ctx, "UPDATE audit_logs SET actor_id=NULL WHERE actor_id<>?", actor); err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM users WHERE id<>?", actor); err != nil {
				return err
			}
		}

		for _, dir := range dirs {
			entries, err := os.ReadDir(filepath.Join(s.root.Path, dir))
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if err = os.RemoveAll(filepath.Join(s.root.Path, dir, entry.Name())); err != nil {
					return err
				}
			}
		}
		if selected["branding"] {
			if err = os.Remove(filepath.Join(s.root.Path, "config", "platform.json")); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		return tx.Commit()
	}
	var err error
	if selected["runtime"] && s.runtime != nil {
		err = s.runtime.Reset(ctx, clear)
	} else {
		err = clear()
	}
	if err != nil {
		fail(c, 500, "RESET_FAILED", "重置未完成，部分文件或运行环境可能已清理，请重试；若仍失败请检查服务器日志及存储权限")
		return
	}
	if selected["users"] {
		s.cookie(c, "", -1)
	}
	ok(c, 200, gin.H{"reset": true, "logged_out": selected["users"]})
}
