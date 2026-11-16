package api

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"huashan/internal/auth"
	dockerruntime "huashan/internal/runtime"
	"huashan/internal/storage"
)

type Config struct {
	ConfigFile     string
	Listen         string
	Runtime        *dockerruntime.Manager
	SecureCookie   bool
	AllowedOrigin  string
	DockerEndpoint string
	MinFreeBytes   uint64
}
type Server struct {
	resetMu      sync.RWMutex
	settingsMu   sync.Mutex
	uploadLocks  sync.Map
	auth         *auth.Service
	root         *storage.Root
	config       Config
	loginMu      sync.Mutex
	attempts     map[string]attempt
	hashSlots    chan struct{}
	packageSlots chan struct{}
	runtime      *dockerruntime.Manager
}
type attempt struct {
	Count int
	Until time.Time
}

func New(service *auth.Service, root *storage.Root, cfg Config) *gin.Engine {
	s := &Server{auth: service, root: root, config: cfg, attempts: map[string]attempt{}, hashSlots: make(chan struct{}, 2), packageSlots: make(chan struct{}, 1)}
	s.runtime = cfg.Runtime
	if s.runtime == nil {
		s.runtime, _ = dockerruntime.NewManager(service.DB, root.Path, root.PlatformID, cfg.DockerEndpoint)
	}
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		if c.Request.URL.Path == "/api/v1/admin/system/reset" && c.Request.Method == "POST" {
			s.resetMu.Lock()
			defer s.resetMu.Unlock()
		} else {
			s.resetMu.RLock()
			defer s.resetMu.RUnlock()
		}
		c.Next()
	})
	_ = r.SetTrustedProxies(nil)
	r.Use(func(c *gin.Context) {
		c.Set("request_id", auth.RandomToken())
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Cache-Control", "no-store")
		limit := int64(16 << 10)
		if c.Request.URL.Path == "/api/v1/admin/platform-settings" {
			limit = 400 << 10
		}
		if strings.HasPrefix(c.Request.URL.Path, "/api/v1/admin/sets") {
			limit = 2 << 20
		}
		if c.Request.Method == "POST" && (c.Request.URL.Path == "/api/v1/admin/packages" ||
			(strings.HasPrefix(c.Request.URL.Path, "/api/v1/admin/sets/") && strings.HasSuffix(c.Request.URL.Path, "/package"))) {
			limit = 0
		}
		// Attachments are saved with the set; do not impose a body size cap on set saves.
		setPath := strings.TrimPrefix(c.Request.URL.Path, "/api/v1/admin/sets")
		setSave := strings.HasPrefix(c.Request.URL.Path, "/api/v1/admin/sets") &&
			((c.Request.Method == "POST" && setPath == "") ||
				(c.Request.Method == "PUT" && strings.HasPrefix(setPath, "/") && len(setPath) > 1 && !strings.Contains(setPath[1:], "/")))
		if c.Request.Method == "PUT" && (strings.HasPrefix(c.Request.URL.Path, "/api/v1/admin/package-uploads/") || strings.HasPrefix(c.Request.URL.Path, "/api/v1/admin/attachment-uploads/")) {
			limit = packageChunkBytes
		}
		if !setSave && limit > 0 {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	})
	v := r.Group("/api/v1")
	v.GET("/health", func(c *gin.Context) { ok(c, 200, gin.H{"status": "ok"}) })
	v.GET("/platform", s.publicPlatformSettings)
	v.GET("/difficulties", s.difficultySettings)
	v.POST("/auth/login", s.login)
	v.POST("/auth/register", s.register)
	secured := v.Group("")
	secured.Use(s.authenticate)
	secured.GET("/auth/me", func(c *gin.Context) { ok(c, 200, gin.H{"user": c.MustGet("user"), "csrf_token": c.GetString("csrf")}) })
	secured.POST("/auth/logout", func(c *gin.Context) {
		if err := s.auth.Logout(c.Request.Context(), c.GetString("token")); err != nil {
			handleError(c, err)
			return
		}
		s.cookie(c, "", -1)
		ok(c, 200, gin.H{"logged_out": true})
	})
	admin := secured.Group("/admin")
	admin.Use(func(c *gin.Context) {
		if c.MustGet("user").(auth.User).Role != "ADMIN" {
			fail(c, 403, "ACCESS_DENIED", "需要管理员权限")
			return
		}
		c.Next()
	})
	admin.GET("/system", s.system)
	admin.POST("/system/reset", s.resetSystem)
	admin.GET("/difficulty-settings", s.difficultySettings)
	admin.PUT("/difficulty-settings", s.saveDifficultySettings)
	admin.GET("/platform-settings", s.platformSettings)
	admin.PUT("/platform-settings", s.savePlatformSettings)
	admin.GET("/users", func(c *gin.Context) {
		items, err := s.auth.Users(c.Request.Context())
		if err != nil {
			handleError(c, err)
			return
		}
		ok(c, 200, gin.H{"items": items})
	})
	admin.POST("/users", s.createUser)
	admin.PATCH("/users/:id", s.setStatus)
	s.registerCompetitions(secured, admin)
	s.CleanPackageOrphans()
	return r
}
func ok(c *gin.Context, status int, data any) {
	c.JSON(status, gin.H{"data": data, "request_id": c.GetString("request_id")})
}
func fail(c *gin.Context, status int, code, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{"code": code, "message": message}, "request_id": c.GetString("request_id")})
}
func handleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, auth.ErrValidation):
		fail(c, 400, "INVALID_INPUT", "用户名须为3–64位小写字母、数字或_.-，密码须为12–128字节，显示名称和角色须合法")
	case errors.Is(err, auth.ErrConflict):
		fail(c, 409, "USER_STATE_CONFLICT", "用户名已存在，或操作会禁用自己/最后一位管理员")
	case errors.Is(err, sql.ErrNoRows):
		fail(c, 404, "NOT_FOUND", "用户不存在")
	default:
		fail(c, 500, "INTERNAL_ERROR", "服务暂时不可用")
	}
}
func (s *Server) authenticate(c *gin.Context) {
	token, _ := c.Cookie("huashan_session")
	u, csrf, err := s.auth.Session(c.Request.Context(), token)
	if err != nil {
		if errors.Is(err, auth.ErrCredentials) {
			fail(c, 401, "AUTH_REQUIRED", "请先登录")
		} else {
			handleError(c, err)
		}
		return
	}
	if c.Request.Method != "GET" && c.Request.Method != "HEAD" && subtle.ConstantTimeCompare([]byte(csrf), []byte(c.GetHeader("X-CSRF-Token"))) != 1 {
		fail(c, 403, "CSRF_INVALID", "请求校验失败，请刷新后重试")
		return
	}
	c.Set("user", u)
	c.Set("csrf", csrf)
	c.Set("token", token)
	c.Next()
}
func (s *Server) cookie(c *gin.Context, value string, age int) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie("huashan_session", value, age, "/", "", s.config.SecureCookie, true)
}
func (s *Server) allowLogin(ip string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := time.Now()
	for key, value := range s.attempts {
		if now.After(value.Until) {
			delete(s.attempts, key)
		}
	}
	a := s.attempts[ip]
	if a.Until.IsZero() {
		if len(s.attempts) >= 10000 {
			return false
		}
		a.Until = now.Add(time.Minute)
	}
	a.Count++
	s.attempts[ip] = a
	return a.Count <= 10
}
func (s *Server) login(c *gin.Context) {
	if !s.allowLogin(c.ClientIP()) {
		c.Header("Retry-After", "60")
		fail(c, 429, "LOGIN_RATE_LIMITED", "登录尝试过于频繁，请稍后重试")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if c.ShouldBindJSON(&input) != nil || len(input.Username) > 64 || len(input.Password) > 128 {
		fail(c, 400, "INVALID_INPUT", "无效的登录请求")
		return
	}
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	default:
		fail(c, 429, "AUTH_BUSY", "认证服务繁忙，请稍后重试")
		return
	}
	u, token, csrf, err := s.auth.Login(c.Request.Context(), input.Username, input.Password)
	if err != nil {
		if errors.Is(err, auth.ErrCredentials) {
			fail(c, 401, "AUTH_INVALID_CREDENTIALS", "用户名或密码错误")
		} else {
			handleError(c, err)
		}
		return
	}
	if old, e := c.Cookie("huashan_session"); e == nil && old != "" {
		_ = s.auth.Logout(c.Request.Context(), old)
	}
	s.cookie(c, token, 12*60*60)
	ok(c, 200, gin.H{"user": u, "csrf_token": csrf})
}
func (s *Server) register(c *gin.Context) {
	if !s.allowLogin("register:" + c.ClientIP()) {
		c.Header("Retry-After", "60")
		fail(c, 429, "REGISTER_RATE_LIMITED", "注册尝试过于频繁，请稍后重试")
		return
	}
	var input struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	if c.ShouldBindJSON(&input) != nil {
		fail(c, 400, "INVALID_INPUT", "无效的注册信息")
		return
	}
	if strings.TrimSpace(input.DisplayName) == "" {
		input.DisplayName = strings.ToLower(strings.TrimSpace(input.Username))
	}
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	default:
		fail(c, 429, "AUTH_BUSY", "认证服务繁忙，请稍后重试")
		return
	}
	// Public registration never accepts a role from the client.
	u, err := s.auth.CreateUser(c.Request.Context(), "", input.Username, input.DisplayName, input.Password, "PLAYER", false)
	if err != nil {
		if errors.Is(err, auth.ErrConflict) {
			fail(c, 409, "USERNAME_TAKEN", "用户名已存在，请更换用户名或直接登录")
		} else {
			handleError(c, err)
		}
		return
	}
	ok(c, 201, gin.H{"user": u})
}

func (s *Server) createUser(c *gin.Context) {
	var input struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
		Role        string `json:"role"`
	}
	if c.ShouldBindJSON(&input) != nil {
		fail(c, 400, "INVALID_INPUT", "无效的用户信息")
		return
	}
	select {
	case s.hashSlots <- struct{}{}:
		defer func() { <-s.hashSlots }()
	default:
		fail(c, 429, "AUTH_BUSY", "认证服务繁忙，请稍后重试")
		return
	}
	u, err := s.auth.CreateUser(c.Request.Context(), c.MustGet("user").(auth.User).ID, input.Username, input.DisplayName, input.Password, input.Role, false)
	if err != nil {
		handleError(c, err)
		return
	}
	ok(c, 201, gin.H{"user": u})
}
func (s *Server) setStatus(c *gin.Context) {
	var input struct {
		Status string `json:"status"`
	}
	if c.ShouldBindJSON(&input) != nil {
		fail(c, 400, "INVALID_INPUT", "无效的用户状态")
		return
	}
	if err := s.auth.SetStatus(c.Request.Context(), c.MustGet("user").(auth.User).ID, c.Param("id"), input.Status); err != nil {
		handleError(c, err)
		return
	}
	ok(c, 200, gin.H{"updated": true})
}
func (s *Server) system(c *gin.Context) {
	used, total, err := storage.DiskUsage(s.root.Path)
	disk := gin.H{"available": err == nil, "used": used, "total": total}

	ok(c, 200, gin.H{"platform_id": s.root.PlatformID, "root": s.root.Path, "os": runtime.GOOS, "arch": runtime.GOARCH, "docker": dockerruntime.Probe(c.Request.Context(), s.config.DockerEndpoint), "resources": dockerruntime.ResourceUsage(c.Request.Context(), s.config.DockerEndpoint), "disk": disk})
}
