package api

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"huashan/internal/competition"
)

func (s *Server) registerTaxonomy(admin *gin.RouterGroup) {
	s.registerKnowledge(admin)
	admin.PUT("/question-types/:name/parent", func(c *gin.Context) {
		var input struct {
			Parent string `json:"parent_name"`
		}
		if !bindCompetition(c, &input) {
			return
		}
		if err := s.competitions().MoveQuestionType(c.Request.Context(), actor(c), c.Param("name"), input.Parent); err != nil {
			competitionError(c, err)
			return
		}
		ok(c, 200, gin.H{"updated": true})
	})
	admin.POST("/taxonomy/:kind/change", func(c *gin.Context) {
		var input competition.TaxonomyChange
		if !bindCompetition(c, &input) {
			return
		}
		count, err := s.competitions().ChangeTaxonomy(c.Request.Context(), actor(c), c.Param("kind"), input)
		if err != nil {
			competitionError(c, err)
			return
		}
		ok(c, 200, gin.H{"updated": count})
	})
	admin.POST("/questions/reclassify", func(c *gin.Context) {
		var input struct {
			Category string                         `json:"category"`
			Items    []competition.LibrarySelection `json:"items"`
		}
		if !bindCompetition(c, &input) {
			return
		}
		count, err := s.competitions().ReclassifyLibrary(c.Request.Context(), actor(c), input.Category, input.Items)
		if err != nil {
			competitionError(c, err)
			return
		}
		ok(c, 200, gin.H{"updated": count})
	})
	admin.GET("/question-types", func(c *gin.Context) {
		rows, err := s.auth.DB.QueryContext(c.Request.Context(), `SELECT t.name, COALESCE(t.parent_name,''), (SELECT count(*) FROM challenges q JOIN question_library l ON l.set_id=q.set_id WHERE q.category=t.name COLLATE NOCASE), (SELECT count(*) FROM challenges q WHERE q.category=t.name COLLATE NOCASE), (SELECT count(*) FROM knowledge_points k WHERE k.owner_type=t.name) FROM question_types t ORDER BY t.name COLLATE NOCASE`)
		if err != nil {
			handleError(c, err)
			return
		}
		defer rows.Close()
		items := []gin.H{}
		for rows.Next() {
			var name, parent string
			var library, total, knowledge int
			if err = rows.Scan(&name, &parent, &library, &total, &knowledge); err != nil {
				handleError(c, err)
				return
			}
			items = append(items, gin.H{"name": name, "parent_name": parent, "knowledge_count": knowledge, "question_count": library, "usage_count": total})
		}
		if err = rows.Err(); err != nil {
			handleError(c, err)
			return
		}
		rows.Close()
		tags := []string{}
		seen := map[string]bool{}
		stats := map[string]gin.H{}
		tagRows, err := s.auth.DB.QueryContext(c.Request.Context(), "SELECT tags_json,EXISTS(SELECT 1 FROM question_library l WHERE l.set_id=c.set_id) FROM challenges c")
		if err != nil {
			handleError(c, err)
			return
		}
		defer tagRows.Close()
		for tagRows.Next() {
			var raw string
			var library bool
			if err = tagRows.Scan(&raw, &library); err != nil {
				handleError(c, err)
				return
			}
			var values []string
			if err = json.Unmarshal([]byte(raw), &values); err != nil {
				handleError(c, err)
				return
			}
			local := map[string]bool{}
			for _, tag := range values {
				key := strings.ToLower(tag)
				if !seen[key] {
					seen[key] = true
					tags = append(tags, tag)
					stats[key] = gin.H{"name": tag, "question_count": 0, "usage_count": 0}
				}
				if !local[key] {
					local[key] = true
					stats[key]["usage_count"] = stats[key]["usage_count"].(int) + 1
					if library {
						stats[key]["question_count"] = stats[key]["question_count"].(int) + 1
					}
				}
			}
		}
		if err = tagRows.Err(); err != nil {
			handleError(c, err)
			return
		}
		tagItems := []gin.H{}
		for _, tag := range tags {
			tagItems = append(tagItems, stats[strings.ToLower(tag)])
		}
		ok(c, 200, gin.H{"items": items, "tags": tags, "tag_items": tagItems})
	})
	admin.POST("/question-types", func(c *gin.Context) {
		var input struct {
			Name   string `json:"name"`
			Parent string `json:"parent_name"`
		}
		if !bindCompetition(c, &input) {
			return
		}
		input.Name = strings.TrimSpace(input.Name)
		if input.Name == "" || utf8.RuneCountInString(input.Name) > 32 || len(input.Name) > 64 || strings.ContainsAny(input.Name, "\n\r\t") {
			fail(c, 400, "INVALID_TYPE", "题型名称须为 1 至 32 个字符（最多 64 字节），不能包含换行或制表符")
			return
		}
		input.Parent = strings.TrimSpace(input.Parent)
		var parent any
		if input.Parent != "" {
			var canonical string
			if err := s.auth.DB.QueryRowContext(c.Request.Context(), "SELECT name FROM question_types WHERE name=?", input.Parent).Scan(&canonical); err != nil {
				fail(c, 400, "INVALID_PARENT", "上级题型不存在，请刷新后重试")
				return
			}
			parent = canonical
		}
		result, err := s.auth.DB.ExecContext(c.Request.Context(), "INSERT OR IGNORE INTO question_types(name,parent_name) VALUES(?,?)", input.Name, parent)
		if err != nil {
			handleError(c, err)
			return
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			fail(c, 409, "DUPLICATE_TYPE", "该题型已存在，名称不区分大小写")
			return
		}
		ok(c, 201, gin.H{"name": input.Name})
	})
	admin.DELETE("/question-types/:name", func(c *gin.Context) {
		result, err := s.auth.DB.ExecContext(c.Request.Context(), `DELETE FROM question_types WHERE name=? AND NOT EXISTS(SELECT 1 FROM challenges WHERE category=question_types.name COLLATE NOCASE) AND NOT EXISTS(SELECT 1 FROM knowledge_points k WHERE k.owner_type=question_types.name) AND NOT EXISTS(SELECT 1 FROM question_types child WHERE child.parent_name=question_types.name)`, c.Param("name"))
		if err != nil {
			handleError(c, err)
			return
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			fail(c, 409, "TYPE_IN_USE", "题型不存在、仍有子题型或仍被题目引用，请先调整关联内容")
			return
		}
		ok(c, 200, gin.H{"deleted": true})
	})
}
