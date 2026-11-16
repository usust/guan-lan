package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

type difficultySettings map[string]string

// Import names saved by the original three-level settings once.
func (s *Server) importDifficultySettings() error {
	tx, err := s.auth.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var imported int
	if err = tx.QueryRow("SELECT count(*) FROM metadata WHERE key='difficulty_settings_imported'").Scan(&imported); err != nil {
		return err
	}
	if imported != 0 {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(s.root.Path, "config", "difficulties.json"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		var old difficultySettings
		if err = json.Unmarshal(data, &old); err != nil {
			return err
		}
		for _, code := range []string{"EASY", "MEDIUM", "HARD"} {
			if name := old[code]; name != "" {
				if _, err = tx.Exec("UPDATE difficulties SET name=? WHERE code=?", name, code); err != nil {
					return err
				}
			}
		}
	}
	if _, err = tx.Exec("INSERT INTO metadata(key,value) VALUES('difficulty_settings_imported','1')"); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Server) readDifficultySettings() (difficultySettings, error) {
	if err := s.importDifficultySettings(); err != nil {
		return nil, err
	}
	rows, err := s.auth.DB.Query("SELECT code,name FROM difficulties ORDER BY position,code")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	settings := difficultySettings{}
	for rows.Next() {
		var code, name string
		if err = rows.Scan(&code, &name); err != nil {
			return nil, err
		}
		settings[code] = name
	}
	return settings, rows.Err()
}
func (s *Server) difficultySettings(c *gin.Context) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	settings, err := s.readDifficultySettings()
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, 200, settings)
}
func (s *Server) saveDifficultySettings(c *gin.Context) {
	var input difficultySettings
	if c.ShouldBindJSON(&input) != nil || len(input) < 1 || len(input) > 50 {
		fail(c, 400, "INVALID_INPUT", "请保留1至50个难度")
		return
	}
	seen := map[string]bool{}
	for code, name := range input {
		name = strings.TrimSpace(name)
		validCode := len(code) > 0 && len(code) <= 64
		for _, ch := range code {
			if !(ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_') {
				validCode = false
			}
		}
		key := strings.ToLower(name)
		if !validCode || name == "" || utf8.RuneCountInString(name) > 16 || strings.ContainsAny(name, "\r\n\t") || seen[key] {
			fail(c, 400, "INVALID_INPUT", "难度名称须为1至16个字符且不能重复，难度标识无效")
			return
		}
		seen[key] = true
		input[code] = name
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if err := s.importDifficultySettings(); err != nil {
		handleError(c, err)
		return
	}
	tx, err := s.auth.DB.BeginTx(c.Request.Context(), nil)
	if err != nil {
		handleError(c, err)
		return
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(c.Request.Context(), "SELECT code FROM difficulties")
	if err != nil {
		handleError(c, err)
		return
	}
	var removed []string
	for rows.Next() {
		var code string
		if err = rows.Scan(&code); err != nil {
			rows.Close()
			handleError(c, err)
			return
		}
		if _, keep := input[code]; !keep {
			removed = append(removed, code)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		handleError(c, err)
		return
	}
	for _, code := range removed {
		var count int
		if err = tx.QueryRowContext(c.Request.Context(), "SELECT count(*) FROM challenges WHERE difficulty=?", code).Scan(&count); err != nil {
			handleError(c, err)
			return
		}
		if count > 0 {
			fail(c, 409, "DIFFICULTY_IN_USE", "该难度仍被题目使用，请先修改关联题目的难度")
			return
		}
	}
	// Replace names together so swapping two labels is valid.
	if _, err = tx.ExecContext(c.Request.Context(), "DELETE FROM difficulties"); err != nil {
		handleError(c, err)
		return
	}
	codes := make([]string, 0, len(input))
	for code := range input {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	defaults := map[string]int{"EASY": 0, "MEDIUM": 1, "HARD": 2}
	sort.SliceStable(codes, func(i, j int) bool {
		a, ok := defaults[codes[i]]
		if !ok {
			a = 3
		}
		b, ok := defaults[codes[j]]
		if !ok {
			b = 3
		}
		return a < b
	})
	for i, code := range codes {
		if _, err = tx.ExecContext(c.Request.Context(), "INSERT INTO difficulties(code,name,position) VALUES(?,?,?)", code, input[code], i); err != nil {
			handleError(c, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		handleError(c, err)
		return
	}
	ok(c, 200, input)
}
