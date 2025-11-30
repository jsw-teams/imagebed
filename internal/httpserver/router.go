package httpserver

import (
	"context"
	"net/http"
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
)

type Server struct {
	addr       string
	configPath string

	mu       sync.RWMutex
	cfg      *config.Config
	db       *pgxpool.Pool
	r2       *storage.R2Client
	mod      *moderation.Service
	verifier *turnstile.Verifier

	engine        *gin.Engine
	httpServer    *http.Server
	bucketHandler *handlers.BucketHandler
	imageHandler  *handlers.ImageHandler
}

func NewServer(configPath string, cfg *config.Config) (*Server, error) {
	if cfg.HTTP.Addr == "" {
		cfg.HTTP.Addr = ":8080"
	}

	s := &Server{
		addr:       cfg.HTTP.Addr,
		configPath: configPath,
		cfg:        cfg,
	}

	// 如果已经安装过，启动时就初始化 DB / R2 / Turnstile / 审查，并执行一次迁移
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

func (s *Server) initRuntime(ctx context.Context) error {
	// 连接数据库
	db, err := database.New(ctx, s.cfg.Database.DSN)
	if err != nil {
		return err
	}

	// 自动执行 migrations（建表）
	if err := database.RunMigrations(ctx, db); err != nil {
		db.Close()
		return err
	}

	// 初始化 R2
	r2Client, err := storage.NewR2Client(ctx, s.cfg.R2)
	if err != nil {
		db.Close()
		return err
	}

	// 审查服务
	modService := moderation.NewService(s.cfg.Moderation, s.cfg.App.AllowedMimeTypes)

	// Turnstile 验证器
	var verifier *turnstile.Verifier
	if s.cfg.Turnstile.Enabled && s.cfg.Turnstile.SecretKey != "" {
		verifier = turnstile.NewVerifier(s.cfg.Turnstile.SecretKey)
	}

	s.db = db
	s.r2 = r2Client
	s.mod = modService
	s.verifier = verifier

	return nil
}

func (s *Server) buildEngine() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.SecurityHeaders())

	// 静态前端：
	// /setup/ -> web/setup/index.html
	// /admin/ -> web/admin/index.html
	r.Static("/setup", "./web/setup")
	r.Static("/admin", "./web/admin")

	// 根路径：未安装 -> /setup；已安装 -> web/index.html
	r.GET("/", func(c *gin.Context) {
		if !s.IsInstalled() {
			c.Redirect(http.StatusFound, "/setup/")
			return
		}
		c.File("./web/index.html")
	})

	// liveness 健康检查：无论安装与否都可用
	r.GET("/healthz", handlers.HealthHandler())

	// 初始化安装 API（仅未安装时可用）
	r.POST("/api/setup", s.handleSetup)

	// 正常 API：需要已安装
	api := r.Group("/api")
	api.Use(middleware.RequireInstalled(func() bool { return s.IsInstalled() }))
	{
		api.GET("/healthz", handlers.HealthHandler())

		s.bucketHandler = handlers.NewBucketHandler()
		s.imageHandler = handlers.NewImageHandler(s.cfg.App.MaxUploadBytes)

		// 如果服务启动时已经是安装状态，立即注入依赖
		if s.IsInstalled() {
			s.mu.RLock()
			db := s.db
			r2Client := s.r2
			modService := s.mod
			s.mu.RUnlock()
			if db != nil && r2Client != nil {
				s.bucketHandler.SetDeps(db, r2Client)
				s.imageHandler.SetDeps(db, r2Client, modService)
			}
		}

		tsMiddleware := middleware.Turnstile(
			func() *turnstile.Verifier { return s.GetVerifier() },
			func() bool { return s.IsTurnstileEnabled() },
		)

		buckets := api.Group("/buckets")
		{
			buckets.GET("", s.bucketHandler.ListBuckets)
			buckets.GET("/:id", s.bucketHandler.GetBucket)
			buckets.POST("", tsMiddleware, s.bucketHandler.CreateBucket)
			buckets.PUT("/:id", tsMiddleware, s.bucketHandler.UpdateBucket)
			buckets.DELETE("/:id", tsMiddleware, s.bucketHandler.DeleteBucket)
		}

		api.POST("/buckets/:bucketID/upload", tsMiddleware, s.imageHandler.Upload)
		api.GET("/images/:id", s.imageHandler.GetImageMeta)
	}

	// 图片访问：未安装时跳转到 /setup；已安装时走 handler
	r.GET("/i/:id", func(c *gin.Context) {
		if !s.IsInstalled() {
			c.Redirect(http.StatusFound, "/setup/")
			return
		}
		s.imageHandler.ServeImage(c)
	})

	s.engine = r
}

func (s *Server) Run() error {
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) Addr() string {
	return s.addr
}

func (s *Server) CloseDB() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		s.db.Close()
		s.db = nil
	}
}

func (s *Server) IsInstalled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg != nil && s.cfg.Installed && s.db != nil && s.r2 != nil
}

func (s *Server) GetVerifier() *turnstile.Verifier {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.verifier
}

func (s *Server) IsTurnstileEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil {
		return false
	}
	return s.cfg.Turnstile.Enabled && s.verifier != nil
}

// ---------- 安装 API：/api/setup ----------

type setupRequest struct {
	DatabaseDSN       string `json:"database_dsn"`
	R2AccountID       string `json:"r2_account_id"`
	R2AccessKeyID     string `json:"r2_access_key_id"`
	R2SecretAccessKey string `json:"r2_secret_access_key"`
	R2Region          string `json:"r2_region"`
	R2PublicBaseURL   string `json:"r2_public_base_url"`
	AdminUsername     string `json:"admin_username"`
	AdminPassword     string `json:"admin_password"`
}

func (s *Server) handleSetup(c *gin.Context) {
	s.mu.RLock()
	alreadyInstalled := s.cfg != nil && s.cfg.Installed
	s.mu.RUnlock()

	if alreadyInstalled {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "already_installed",
		})
		return
	}

	var req setupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}

	trim := func(v string) string { return strings.TrimSpace(v) }

	req.DatabaseDSN = trim(req.DatabaseDSN)
	req.R2AccountID = trim(req.R2AccountID)
	req.R2AccessKeyID = trim(req.R2AccessKeyID)
	req.R2SecretAccessKey = trim(req.R2SecretAccessKey)
	req.R2Region = trim(req.R2Region)
	req.R2PublicBaseURL = trim(req.R2PublicBaseURL)
	req.AdminUsername = trim(req.AdminUsername)
	req.AdminPassword = trim(req.AdminPassword)

	if req.DatabaseDSN == "" ||
		req.R2AccountID == "" ||
		req.R2AccessKeyID == "" ||
		req.R2SecretAccessKey == "" ||
		req.AdminUsername == "" ||
		req.AdminPassword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing_fields"})
		return
	}
	if req.R2Region == "" {
		req.R2Region = "auto"
	}

	ctx := c.Request.Context()

	// 1. 测试数据库连接（不通过就不写配置）
	pool, err := database.New(ctx, req.DatabaseDSN)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "db_connect_failed",
			"detail": err.Error(),
		})
		return
	}

	// 2. 自动执行 migrations（首次建表）
	if err := database.RunMigrations(ctx, pool); err != nil {
		pool.Close()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "db_migration_failed",
			"detail": err.Error(),
		})
		return
	}

	// 3. 初始化管理员（依赖 admins 表）
	if err := models.EnsureInitialAdmin(ctx, pool, req.AdminUsername, req.AdminPassword); err != nil {
		pool.Close()
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "create_admin_failed",
			"detail": err.Error(),
		})
		return
	}

	// 4. 基于现有 cfg 生成新 cfg
	s.mu.RLock()
	oldCfg := s.cfg
	s.mu.RUnlock()
	if oldCfg == nil {
		oldCfg = &config.Config{}
	}
	newCfg := *oldCfg
	newCfg.Installed = true
	newCfg.Database.DSN = req.DatabaseDSN
	newCfg.R2.AccountID = req.R2AccountID
	newCfg.R2.AccessKeyID = req.R2AccessKeyID
	newCfg.R2.SecretAccessKey = req.R2SecretAccessKey
	newCfg.R2.Region = req.R2Region
	if req.R2PublicBaseURL != "" {
		newCfg.R2.PublicBaseURL = req.R2PublicBaseURL
	}

	// 5. 先写配置文件
	if err := config.Save(s.configPath, &newCfg); err != nil {
		pool.Close()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "save_config_failed",
			"detail": err.Error(),
		})
		return
	}

	// 6. 初始化 R2 / 审查 / Turnstile
	r2Client, err := storage.NewR2Client(ctx, newCfg.R2)
	if err != nil {
		pool.Close()
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "r2_init_failed",
			"detail": err.Error(),
		})
		return
	}
	modService := moderation.NewService(newCfg.Moderation, newCfg.App.AllowedMimeTypes)

	var verifier *turnstile.Verifier
	if newCfg.Turnstile.Enabled && newCfg.Turnstile.SecretKey != "" {
		verifier = turnstile.NewVerifier(newCfg.Turnstile.SecretKey)
	}

	// 7. 更新运行时依赖（无须重启）
	s.mu.Lock()
	if s.db != nil {
		s.db.Close()
	}
	s.cfg = &newCfg
	s.db = pool
	s.r2 = r2Client
	s.mod = modService
	s.verifier = verifier
	s.mu.Unlock()

	// 8. 注入到 handlers
	if s.bucketHandler != nil {
		s.bucketHandler.SetDeps(pool, r2Client)
	}
	if s.imageHandler != nil {
		s.imageHandler.SetDeps(pool, r2Client, modService)
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}
