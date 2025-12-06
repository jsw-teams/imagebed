package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jsw-teams/imagebed/internal/config"
	"github.com/jsw-teams/imagebed/internal/database"
	"github.com/jsw-teams/imagebed/internal/httpserver/handlers"
	"github.com/jsw-teams/imagebed/internal/httpserver/middleware"
	"github.com/jsw-teams/imagebed/internal/moderation"
	"github.com/jsw-teams/imagebed/internal/models"
	"github.com/jsw-teams/imagebed/internal/storage"
	"github.com/jsw-teams/imagebed/internal/turnstile"
	webui "github.com/jsw-teams/imagebed/web"
)

// Server 封装 HTTP 服务及其依赖
type Server struct {
	addr       string
	configPath string

	mu       sync.RWMutex
	cfg      *config.Config
	db       *pgxpool.Pool
	r2Pool   *storage.R2Pool
	mod      *moderation.Service
	verifier *turnstile.Verifier
	tsCfg    *models.TurnstileSettings

	engine        *gin.Engine
	httpServer    *http.Server
	bucketHandler *handlers.BucketHandler
	imageHandler  *handlers.ImageHandler
}

// NewServer 使用给定配置创建 HTTP 服务器。
func NewServer(configPath string, cfg *config.Config) (*Server, error) {
	if cfg.HTTP.Addr == "" {
		cfg.HTTP.Addr = ":9000"
	}

	s := &Server{
		addr:       cfg.HTTP.Addr,
		configPath: configPath,
		cfg:        cfg,
	}

	// 已安装情况下，启动时初始化运行环境（DB / 迁移 / 审查 / Turnstile / R2Pool）
	if cfg.Installed {
		if err := s.initRuntime(context.Background()); err != nil {
			return nil, err
		}
	}

	s.buildEngine()
	s.httpServer = &http.Server{
		Addr:    s.addr,
		Handler: s.engine,
	}
	return s, nil
}

// initRuntime 根据当前 cfg 初始化 DB / 迁移 / 审查 / Turnstile / R2Pool
func (s *Server) initRuntime(ctx context.Context) error {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	if cfg == nil {
		return fmt.Errorf("initRuntime: nil config")
	}
	if cfg.Database.DSN == "" {
		return fmt.Errorf("initRuntime: empty database DSN")
	}

	db, err := database.New(ctx, cfg.Database.DSN)
	if err != nil {
		return fmt.Errorf("initRuntime: db connect failed: %w", err)
	}

	if err := database.RunMigrations(ctx, db); err != nil {
		db.Close()
		return fmt.Errorf("initRuntime: migrations failed: %w", err)
	}

	// 审查服务基于 config.Moderation
	modService := moderation.NewService(cfg.Moderation, cfg.App.AllowedMimeTypes)

	// Turnstile 配置从数据库读取；如果没配置则视为未启用
	tsCfg, err := models.GetTurnstileSettings(ctx, db)
	if err != nil {
		db.Close()
		return fmt.Errorf("initRuntime: load turnstile settings failed: %w", err)
	}
	var verifier *turnstile.Verifier
	if tsCfg != nil && tsCfg.Enabled && tsCfg.SecretKey != "" {
		verifier = turnstile.NewVerifier(tsCfg.SecretKey)
	}

	// R2Pool：按桶配置动态创建 R2Client；这里初始化一个空池
	r2Pool := storage.NewR2Pool()

	s.mu.Lock()
	if s.db != nil {
		s.db.Close()
	}
	s.db = db
	s.r2Pool = r2Pool
	s.mod = modService
	s.tsCfg = tsCfg
	s.verifier = verifier
	s.mu.Unlock()

	return nil
}

func (s *Server) getDB() *pgxpool.Pool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db
}

// 从 embed.FS 读取任意文件并返回
func serveEmbeddedFile(c *gin.Context, path, contentType string) {
	data, err := webui.FS.ReadFile(path)
	if err != nil {
		c.String(http.StatusNotFound, "not found")
		return
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	c.Data(http.StatusOK, contentType, data)
}

// HTML 包装
func serveEmbeddedHTML(c *gin.Context, path string) {
	serveEmbeddedFile(c, path, "text/html; charset=utf-8")
}

// buildEngine 构建 gin.Engine 和所有路由
func (s *Server) buildEngine() {
	gin.SetMode(gin.ReleaseMode)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.SecurityHeaders())

	// ---------- 前端页面路由 ----------

	// 根路径：未安装重定向到 /setup，已安装返回上传页
	r.GET("/", func(c *gin.Context) {
		if !s.IsInstalled() {
			c.Redirect(http.StatusFound, "/setup")
			return
		}
		serveEmbeddedHTML(c, "index.html")
	})

	// 上传页兼容路径 /upload
	r.GET("/upload", func(c *gin.Context) {
		if !s.IsInstalled() {
			c.Redirect(http.StatusFound, "/setup")
			return
		}
		serveEmbeddedHTML(c, "index.html")
	})
	r.GET("/upload/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/upload")
	})

	// 初始化安装页面：只在未安装时可访问；已安装直接 404
	r.GET("/setup", s.serveSetupPage)
	r.GET("/setup/", s.serveSetupPage)

	// 管理后台登录页
	r.GET("/admin", func(c *gin.Context) {
		if !s.IsInstalled() {
			c.Redirect(http.StatusFound, "/setup")
			return
		}
		serveEmbeddedHTML(c, "admin/index.html")
	})
	r.GET("/admin/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/admin")
	})

	// 管理后台仪表盘
	r.GET("/admin/dashboard", func(c *gin.Context) {
		if !s.IsInstalled() {
			c.Redirect(http.StatusFound, "/setup")
			return
		}
		serveEmbeddedHTML(c, "admin/dashboard.html")
	})
	r.GET("/admin/dashboard/", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/admin/dashboard")
	})

	// ---------- 静态资源：CSS / JS（同样从 embed.FS 提供） ----------

	// 首页上传页
	r.GET("/index.css", func(c *gin.Context) {
		serveEmbeddedFile(c, "index.css", "text/css; charset=utf-8")
	})
	r.GET("/index.js", func(c *gin.Context) {
		serveEmbeddedFile(c, "index.js", "application/javascript; charset=utf-8")
	})

	// 管理后台登录页
	r.GET("/admin/index.css", func(c *gin.Context) {
		serveEmbeddedFile(c, "admin/index.css", "text/css; charset=utf-8")
	})
	r.GET("/admin/index.js", func(c *gin.Context) {
		serveEmbeddedFile(c, "admin/index.js", "application/javascript; charset=utf-8")
	})

	// 管理后台 Dashboard
	r.GET("/admin/dashboard.css", func(c *gin.Context) {
		serveEmbeddedFile(c, "admin/dashboard.css", "text/css; charset=utf-8")
	})
	r.GET("/admin/dashboard.js", func(c *gin.Context) {
		serveEmbeddedFile(c, "admin/dashboard.js", "application/javascript; charset=utf-8")
	})

	// 安装向导
	r.GET("/setup/index.css", func(c *gin.Context) {
		serveEmbeddedFile(c, "setup/index.css", "text/css; charset=utf-8")
	})
	r.GET("/setup/index.js", func(c *gin.Context) {
		serveEmbeddedFile(c, "setup/index.js", "application/javascript; charset=utf-8")
	})

	// 健康检查（不依赖安装状态）
	r.GET("/healthz", handlers.HealthHandler())

	// ---------- 安装 API（未安装时可用） ----------

	r.POST("/api/setup/database", s.handleSetupDatabase)
	r.POST("/api/setup/admin", s.handleSetupAdmin)

	// ---------- 已安装后才能访问的 API ----------

	api := r.Group("/api")
	api.Use(middleware.RequireInstalled(func() bool { return s.IsInstalled() }))
	{
		api.GET("/healthz", handlers.HealthHandler())

		// 创建 handler 实例
		s.bucketHandler = handlers.NewBucketHandler()
		s.imageHandler = handlers.NewImageHandler(s.cfg.App.MaxUploadBytes)

		// 启动时如果已经安装，则注入依赖
		if s.IsInstalled() {
			s.mu.RLock()
			db := s.db
			r2Pool := s.r2Pool
			modService := s.mod
			s.mu.RUnlock()
			if db != nil {
				s.bucketHandler.SetDeps(db, r2Pool)
				s.imageHandler.SetDeps(db, r2Pool, modService)
			}
		}

		// Turnstile 中间件（上传用）；只有在后台启用 + 配置完成后才真正生效
		tsMiddleware := middleware.Turnstile(
			func() *turnstile.Verifier { return s.GetVerifier() },
			func() bool { return s.IsTurnstileEnabled() },
		)

		// ---------- Turnstile 公共查询接口（前端上传页 / 管理后台登录页使用） ----------
		api.GET("/turnstile", s.handlePublicTurnstile)

		// ---------- 管理员登录相关（不加 AdminAuthRequired） ----------
		adminOpen := api.Group("/admin")
		{
			adminOpen.GET("/session", middleware.HandleAdminSessionStatus())
			adminOpen.POST("/login", middleware.HandleAdminLogin(s.getDB))
			adminOpen.POST("/logout", middleware.HandleAdminLogout())
		}

		// ---------- 需要管理员登录的后台 API ----------
		adminSec := api.Group("/admin")
		adminSec.Use(middleware.AdminAuthRequired())
		{
			// R2 桶管理（每桶独立账号 / endpoint）
			adminSec.GET("/buckets", s.bucketHandler.ListBuckets)
			adminSec.GET("/buckets/:id", s.bucketHandler.GetBucket)
			adminSec.POST("/buckets", s.bucketHandler.CreateBucket)
			adminSec.PUT("/buckets/:id", s.bucketHandler.UpdateBucket)
			adminSec.DELETE("/buckets/:id", s.bucketHandler.DeleteBucket)

			// Turnstile 配置（仅后台使用）
			adminSec.GET("/turnstile", s.handleAdminGetTurnstile)
			adminSec.POST("/turnstile", s.handleAdminUpdateTurnstile)
			adminSec.POST("/turnstile/test", s.handleAdminTestTurnstile)
		}

		// 老接口：指定桶上传（第三方客户端）
		api.POST("/buckets/:bucketID/upload", tsMiddleware, s.imageHandler.Upload)

		// 新接口：自动挑桶上传（前端上传页使用）
		api.POST("/upload", tsMiddleware, s.handleAutoUpload)

		// 查询图片元信息
		api.GET("/images/:id", s.imageHandler.GetImageMeta)
	}

	// 图片访问统一入口
	r.GET("/i/:id", func(c *gin.Context) {
		if !s.IsInstalled() {
			c.Redirect(http.StatusFound, "/setup")
			return
		}
		s.imageHandler.ServeImage(c)
	})

	s.engine = r
}

// 初始化页面：未安装才可以访问；已安装直接 404
func (s *Server) serveSetupPage(c *gin.Context) {
	if s.IsInstalled() {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	serveEmbeddedHTML(c, "setup/index.html")
}

// Run 启动 HTTP 服务
func (s *Server) Run() error {
	return s.httpServer.ListenAndServe()
}

// Shutdown 平滑关闭 HTTP 服务
func (s *Server) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

// Addr 返回监听地址
func (s *Server) Addr() string {
	return s.addr
}

// CloseDB 关闭数据库连接
func (s *Server) CloseDB() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		s.db.Close()
		s.db = nil
	}
}

// IsInstalled 当前是否已完成安装
func (s *Server) IsInstalled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg != nil && s.cfg.Installed
}

// GetVerifier 返回当前 Turnstile Verifier
func (s *Server) GetVerifier() *turnstile.Verifier {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.verifier
}

// IsTurnstileEnabled Turnstile 启用状态来自数据库配置
// 只有在：tsCfg 不为空、Enabled=true、SiteKey & SecretKey 均非空 且 verifier 已初始化 时返回 true
func (s *Server) IsTurnstileEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.tsCfg == nil {
		return false
	}
	if !s.tsCfg.Enabled {
		return false
	}
	if s.tsCfg.SiteKey == "" || s.tsCfg.SecretKey == "" {
		return false
	}
	return s.verifier != nil
}

//
// ---------- 安装 API：步骤一 - 配置数据库 ----------
//

type setupDatabaseRequest struct {
	DBName string `json:"db_name"`
	DBUser string `json:"db_user"`
	DBPass string `json:"db_password"`
}

func (s *Server) handleSetupDatabase(c *gin.Context) {
	s.mu.RLock()
	alreadyInstalled := s.cfg != nil && s.cfg.Installed
	s.mu.RUnlock()
	if alreadyInstalled {
		c.JSON(http.StatusBadRequest, gin.H{"error": "already_installed"})
		return
	}

	var req setupDatabaseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}

	trim := func(v string) string { return strings.TrimSpace(v) }
	req.DBName = trim(req.DBName)
	req.DBUser = trim(req.DBUser)
	req.DBPass = trim(req.DBPass)

	if req.DBName == "" || req.DBUser == "" || req.DBPass == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing_fields"})
		return
	}

	host := "127.0.0.1"
	port := 5432
	sslmode := "disable"

	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(req.DBUser, req.DBPass),
		Host:   fmt.Sprintf("%s:%d", host, port),
		Path:   req.DBName,
	}
	q := u.Query()
	q.Set("sslmode", sslmode)
	u.RawQuery = q.Encode()
	dsn := u.String()

	ctx := c.Request.Context()

	pool, err := database.New(ctx, dsn)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "db_connect_failed",
			"detail": err.Error(),
		})
		return
	}

	if err := database.RunMigrations(ctx, pool); err != nil {
		pool.Close()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "db_migration_failed",
			"detail": err.Error(),
		})
		return
	}

	s.mu.RLock()
	oldCfg := s.cfg
	s.mu.RUnlock()
	if oldCfg == nil {
		oldCfg = &config.Config{}
	}
	newCfg := *oldCfg
	newCfg.Database.DSN = dsn
	// 仅完成数据库配置，尚未创建管理员账号
	newCfg.Installed = false

	if err := config.Save(s.configPath, &newCfg); err != nil {
		pool.Close()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "save_config_failed",
			"detail": err.Error(),
		})
		return
	}

	s.mu.Lock()
	if s.db != nil {
		s.db.Close()
	}
	s.cfg = &newCfg
	s.db = pool
	s.mu.Unlock()

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

//
// ---------- 安装 API：步骤二 - 设置管理员账号 ----------
//

type setupAdminRequest struct {
	AdminUsername string `json:"admin_username"`
	AdminPassword string `json:"admin_password"`
}

func (s *Server) handleSetupAdmin(c *gin.Context) {
	s.mu.RLock()
	cfg := s.cfg
	db := s.db
	s.mu.RUnlock()

	if cfg != nil && cfg.Installed {
		c.JSON(http.StatusBadRequest, gin.H{"error": "already_installed"})
		return
	}
	if cfg == nil || cfg.Database.DSN == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "db_not_configured"})
		return
	}

	var req setupAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}

	trim := func(v string) string { return strings.TrimSpace(v) }
	req.AdminUsername = trim(req.AdminUsername)
	req.AdminPassword = trim(req.AdminPassword)
	if req.AdminUsername == "" || req.AdminPassword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing_fields"})
		return
	}

	ctx := c.Request.Context()

	var err error
	if db == nil {
		db, err = database.New(ctx, cfg.Database.DSN)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":  "db_connect_failed",
				"detail": err.Error(),
			})
			return
		}
	}

	if err := database.RunMigrations(ctx, db); err != nil {
		db.Close()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "db_migration_failed",
			"detail": err.Error(),
		})
		return
	}

	if err := models.EnsureInitialAdmin(ctx, db, req.AdminUsername, req.AdminPassword); err != nil {
		db.Close()
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "create_admin_failed",
			"detail": err.Error(),
		})
		return
	}

	// 标记安装完成
	newCfg := *cfg
	newCfg.Installed = true

	if err := config.Save(s.configPath, &newCfg); err != nil {
		db.Close()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "save_config_failed",
			"detail": err.Error(),
		})
		return
	}

	// 安装完成后，初始化 Moderation / Turnstile / R2Pool
	modService := moderation.NewService(newCfg.Moderation, newCfg.App.AllowedMimeTypes)

	tsCfg, err := models.GetTurnstileSettings(ctx, db)
	if err != nil {
		db.Close()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "load_turnstile_failed",
			"detail": err.Error(),
		})
		return
	}
	var verifier *turnstile.Verifier
	if tsCfg != nil && tsCfg.Enabled && tsCfg.SecretKey != "" {
		verifier = turnstile.NewVerifier(tsCfg.SecretKey)
	}

	r2Pool := storage.NewR2Pool()

	s.mu.Lock()
	if s.db != nil && s.db != db {
		s.db.Close()
	}
	s.cfg = &newCfg
	s.db = db
	s.r2Pool = r2Pool
	s.mod = modService
	s.tsCfg = tsCfg
	s.verifier = verifier
	s.mu.Unlock()

	// 如果 handler 已经创建，则注入依赖
	if s.bucketHandler != nil {
		s.bucketHandler.SetDeps(db, r2Pool)
	}
	if s.imageHandler != nil {
		s.imageHandler.SetDeps(db, r2Pool, modService)
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

//
// ---------- 自动分配桶并上传 ----------
//

func (s *Server) handleAutoUpload(c *gin.Context) {
	s.mu.RLock()
	db := s.db
	s.mu.RUnlock()

	if db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "db_not_ready"})
		return
	}

	bucketID, err := models.PickAutoBucketID(c.Request.Context(), db)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "auto_bucket_failed",
			"detail": err.Error(),
		})
		return
	}
	if bucketID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no_bucket_available"})
		return
	}

	// 把 auto 选出的 bucketID 写入 Gin 的路径参数，复用 Upload 逻辑
	c.Params = append(c.Params, gin.Param{
		Key:   "bucketID",
		Value: bucketID,
	})

	s.imageHandler.Upload(c)
}

//
// ---------- Turnstile 配置相关 ----------
//

// 后台（带 has_secret）响应
type turnstileConfigResponse struct {
	Enabled   bool   `json:"enabled"`
	SiteKey   string `json:"site_key"`
	HasSecret bool   `json:"has_secret"`
}

// 后台更新请求
type updateTurnstileRequest struct {
	Enabled   bool   `json:"enabled"`
	SiteKey   string `json:"site_key"`
	SecretKey string `json:"secret_key"`
}

// 后台测试请求
type testTurnstileRequest struct {
	SecretKey string `json:"secret_key"`
	TestToken string `json:"test_token"`
}

// 前端公开查询（不包含 secret）
type publicTurnstileConfigResponse struct {
	Enabled bool   `json:"enabled"`
	SiteKey string `json:"site_key"`
}

// handlePublicTurnstile 提供给前端（上传页 / 后台登录页）查询是否启用 Turnstile + siteKey。
// 未启用或未完整配置时，返回 enabled=false, site_key=""。
func (s *Server) handlePublicTurnstile(c *gin.Context) {
	s.mu.RLock()
	ts := s.tsCfg
	s.mu.RUnlock()

	if ts == nil || !ts.Enabled || ts.SiteKey == "" || ts.SecretKey == "" {
		c.JSON(http.StatusOK, publicTurnstileConfigResponse{
			Enabled: false,
			SiteKey: "",
		})
		return
	}

	c.JSON(http.StatusOK, publicTurnstileConfigResponse{
		Enabled: true,
		SiteKey: ts.SiteKey,
	})
}

// 后台获取 Turnstile 配置（需要管理员登录）
func (s *Server) handleAdminGetTurnstile(c *gin.Context) {
	s.mu.RLock()
	cfg := s.cfg
	db := s.db
	s.mu.RUnlock()

	if cfg == nil || !cfg.Installed {
		c.JSON(http.StatusBadRequest, gin.H{"error": "not_installed"})
		return
	}
	if db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "db_not_ready"})
		return
	}

	tsCfg, err := models.GetTurnstileSettings(c.Request.Context(), db)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "load_turnstile_failed",
			"detail": err.Error(),
		})
		return
	}

	// 同步到内存
	var verifier *turnstile.Verifier
	if tsCfg != nil && tsCfg.Enabled && tsCfg.SecretKey != "" {
		verifier = turnstile.NewVerifier(tsCfg.SecretKey)
	}
	s.mu.Lock()
	s.tsCfg = tsCfg
	s.verifier = verifier
	s.mu.Unlock()

	if tsCfg == nil {
		c.JSON(http.StatusOK, turnstileConfigResponse{
			Enabled:   false,
			SiteKey:   "",
			HasSecret: false,
		})
		return
	}

	c.JSON(http.StatusOK, turnstileConfigResponse{
		Enabled:   tsCfg.Enabled,
		SiteKey:   tsCfg.SiteKey,
		HasSecret: tsCfg.SecretKey != "",
	})
}

// 后台更新 Turnstile 配置
func (s *Server) handleAdminUpdateTurnstile(c *gin.Context) {
	s.mu.RLock()
	cfg := s.cfg
	db := s.db
	s.mu.RUnlock()

	if cfg == nil || !cfg.Installed {
		c.JSON(http.StatusBadRequest, gin.H{"error": "not_installed"})
		return
	}
	if db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "db_not_ready"})
		return
	}

	var req updateTurnstileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}

	req.SiteKey = strings.TrimSpace(req.SiteKey)
	req.SecretKey = strings.TrimSpace(req.SecretKey)

	if req.Enabled && (req.SiteKey == "" || req.SecretKey == "") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "enabled_requires_site_and_secret"})
		return
	}

	ctx := c.Request.Context()

	tsCfg, err := models.UpsertTurnstileSettings(ctx, db, req.Enabled, req.SiteKey, req.SecretKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "save_turnstile_failed",
			"detail": err.Error(),
		})
		return
	}

	var verifier *turnstile.Verifier
	if tsCfg != nil && tsCfg.Enabled && tsCfg.SecretKey != "" {
		verifier = turnstile.NewVerifier(tsCfg.SecretKey)
	}

	s.mu.Lock()
	s.tsCfg = tsCfg
	s.verifier = verifier
	s.mu.Unlock()

	c.JSON(http.StatusOK, turnstileConfigResponse{
		Enabled:   tsCfg.Enabled,
		SiteKey:   tsCfg.SiteKey,
		HasSecret: tsCfg.SecretKey != "",
	})
}

// /api/admin/turnstile/test：用真实 token 调用 siteverify 验证密钥是否有效
func (s *Server) handleAdminTestTurnstile(c *gin.Context) {
	var req testTurnstileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}
	req.SecretKey = strings.TrimSpace(req.SecretKey)
	req.TestToken = strings.TrimSpace(req.TestToken)

	if req.SecretKey == "" || req.TestToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing_secret_or_token"})
		return
	}

	if err := verifyTurnstileConfig(c.Request.Context(), req.SecretKey, req.TestToken); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "turnstile_verify_failed",
			"detail": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

//
// ---------- Turnstile siteverify ----------
//

type siteVerifyResp struct {
	Success    bool     `json:"success"`
	ErrorCodes []string `json:"error-codes"`
}

func verifyTurnstileConfig(ctx context.Context, secret, token string) error {
	if secret == "" {
		return fmt.Errorf("empty secret")
	}
	if token == "" {
		return fmt.Errorf("empty token")
	}

	form := url.Values{}
	form.Set("secret", secret)
	form.Set("response", token)

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"https://challenges.cloudflare.com/turnstile/v0/siteverify",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var out siteVerifyResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}

	if out.Success {
		return nil
	}

	// secret 相关错误：直接提示密钥配置问题
	secretErrs := map[string]bool{
		"invalid-input-secret": true,
		"missing-input-secret": true,
		"secret-mismatch":      true,
	}

	for _, code := range out.ErrorCodes {
		if secretErrs[code] {
			return fmt.Errorf("invalid secret: %s", code)
		}
	}

	// 其它错误视为 token 无效
	if len(out.ErrorCodes) > 0 {
		return fmt.Errorf("invalid token: %s", strings.Join(out.ErrorCodes, ","))
	}

	return fmt.Errorf("turnstile verification failed")
}