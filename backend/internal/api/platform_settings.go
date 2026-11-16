package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

type platformSettings struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

func (s *Server) readPlatformSettings() (platformSettings, error) {
	settings := platformSettings{Name: "华山平台", Description: "竞赛与训练"}
	path := filepath.Join(s.root.Path, "config", "platform.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if !info.Mode().IsRegular() || info.Size() > 400<<10 {
		return settings, fmt.Errorf("invalid platform settings file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return settings, err
	}
	err = json.Unmarshal(data, &settings)
	return settings, err
}
func (s *Server) publicPlatformSettings(c *gin.Context) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	settings, err := s.readPlatformSettings()
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, 200, settings)
}
func (s *Server) platformSettings(c *gin.Context) {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	settings, err := s.readPlatformSettings()
	if err != nil {
		handleError(c, err)
		return
	}
	paths := map[string]string{}
	for key, dir := range map[string]string{"settings": "config", "database": "data/database/huashan.db", "packages": "uploads/packages", "attachments": "uploads/attachments", "images": "images/archives", "templates": "challenges/templates", "runtime": "instances/runtime", "logs": "logs", "temp": "temp", "backups": "backups"} {
		paths[key] = filepath.Join(s.root.Path, dir)
	}
	publicHost := ""
	if s.runtime != nil {
		publicHost = s.runtime.PublicHost
	}
	ok(c, 200, gin.H{"branding": settings, "root": s.root.Path, "platform_id": s.root.PlatformID, "paths": paths, "config_file": s.config.ConfigFile, "listen": s.config.Listen, "allowed_origin": s.config.AllowedOrigin, "secure_cookie": s.config.SecureCookie, "docker_endpoint": s.config.DockerEndpoint, "runtime_public_host": publicHost, "min_free_bytes": s.config.MinFreeBytes})
}
func (s *Server) savePlatformSettings(c *gin.Context) {
	var input platformSettings
	if c.ShouldBindJSON(&input) != nil {
		fail(c, 400, "INVALID_INPUT", "无效的平台设置")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 64 || utf8.RuneCountInString(input.Description) > 200 {
		fail(c, 400, "INVALID_INPUT", "平台名称须为1–64个字符，平台简介不超过200个字符")
		return
	}
	if input.Icon != "" {
		prefix, encoded, found := strings.Cut(input.Icon, ",")
		data, err := base64.StdEncoding.DecodeString(encoded)
		if !found || (prefix != "data:image/png;base64" && prefix != "data:image/jpeg;base64") || err != nil || len(data) > 256<<10 {
			fail(c, 400, "INVALID_ICON", "请使用不超过256 KB的 PNG 或 JPEG 图标")
			return
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width > 2048 || cfg.Height > 2048 || cfg.Width < 1 || cfg.Height < 1 || (format == "png" && prefix != "data:image/png;base64") || (format == "jpeg" && prefix != "data:image/jpeg;base64") {
			fail(c, 400, "INVALID_ICON", "图标须为有效的 PNG 或 JPEG，尺寸不超过2048×2048")
			return
		}
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	data, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		handleError(c, err)
		return
	}
	file, err := os.CreateTemp(filepath.Join(s.root.Path, "config"), ".platform-*")
	if err != nil {
		handleError(c, err)
		return
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), filepath.Join(s.root.Path, "config", "platform.json"))
	}
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, 200, input)
}
