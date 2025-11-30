package httpserver

import (
	"context"
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
		cfg.HTTP.Addr = ":9000"
	}

	s := &Server{
		addr:       cfg.HTTP.Addr,
		configPath: configPath,
		cfg:        cfg,
	}

	// 如果已经安装过，则在启动时初始化运行环境
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

// initRuntime 根据当前 cfg 初始化 DB / 迁移 / R2 / 审查 / Turnstile
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

	// R2 可选：如果未配置，则保持为 nil，让上传时返回“r2_not_configured”
	var r2Client *storage.R2Client
	if hasR2Config(&cfg.R2) {
		r2Client, err = storage.NewR2Client(ctx, cfg.R2)
		if err != nil {
			db.Close()
			return fmt.Errorf("initRuntime: r2 init failed: %w", err)
		}
	}

	modService := moderation.NewService(cfg.Moderation, cfg.App.AllowedMimeTypes)

	var verifier *turnstile.Verifier
	if cfg.Turnstile.Enabled && cfg.Turnstile.SecretKey != "" {
		verifier = turnstile.NewVerifier(cfg.Turnstile.SecretKey)
	}

	s.mu.Lock()
	if s.db != nil {
		s.db.Close()
	}
	s.db = db
	s.r2 = r2Client
	s.mod = modService
	s.verifier = verifier
	s.mu.Unlock()

	return nil
}

func hasR2Config(r2 *config.R2Config) bool {
	if r2 == nil {
		return false
	}
	return r2.AccountID != "" && r2.AccessKeyID != "" && r2.SecretAccessKey != ""
}

func (s *Server) buildEngine() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.SecurityHeaders())

	// 静态前端
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

	// 健康检查
	r.GET("/healthz", handlers.HealthHandler())

	// 初始化安装 API：分两步
	r.POST("/api/setup/database", s.handleSetupDatabase)
	r.POST("/api/setup/admin", s.handleSetupAdmin)

	// 需要已安装的 API
	api := r.Group("/api")
	api.Use(middleware.RequireInstalled(func() bool { return s.IsInstalled() }))
	{
		api.GET("/healthz", handlers.HealthHandler())

		s.bucketHandler = handlers.NewBucketHandler()
		s.imageHandler = handlers.NewImageHandler(s.cfg.App.MaxUploadBytes)

		// 如果启动时已安装，注入依赖
		if s.IsInstalled() {
			s.mu.RLock()
			db := s.db
			r2Client := s.r2
			modService := s.mod
			s.mu.RUnlock()

			if db != nil {
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

	// 图片访问
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
	return s.cfg != nil && s.cfg.Installed
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

// ---------- 安装 API：步骤一 - 配置数据库 ----------

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

	// 构造 DSN：postgres://user:pass@127.0.0.1:5432/dbname?sslmode=disable
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

	// 测试连接
	pool, err := database.New(ctx, dsn)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "db_connect_failed",
			"detail": err.Error(),
		})
		return
	}

	// 自动执行 migrations
	if err := database.RunMigrations(ctx, pool); err != nil {
		pool.Close()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "db_migration_failed",
			"detail": err.Error(),
		})
		return
	}

	// 写入配置（但 installed 仍为 false）
	s.mu.RLock()
	oldCfg := s.cfg
	s.mu.RUnlock()
	if oldCfg == nil {
		oldCfg = &config.Config{}
	}
	newCfg := *oldCfg
	newCfg.Database.DSN = dsn
	newCfg.Installed = false

	if err := config.Save(s.configPath, &newCfg); err != nil {
		pool.Close()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "save_config_failed",
			"detail": err.Error(),
		})
		return
	}

	// 更新运行时
	s.mu.Lock()
	if s.db != nil {
		s.db.Close()
	}
	s.cfg = &newCfg
	s.db = pool
	s.mu.Unlock()

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------- 安装 API：步骤二 - 设置管理员 ----------

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

	// 如果当前没有 db 连接，再连一次
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

	// 确保 migrations 已执行（幂等）
	if err := database.RunMigrations(ctx, db); err != nil {
		db.Close()
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "db_migration_failed",
			"detail": err.Error(),
		})
		return
	}

	// 创建初始管理员
	if err := models.EnsureInitialAdmin(ctx, db, req.AdminUsername, req.AdminPassword); err != nil {
		db.Close()
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "create_admin_failed",
			"detail": err.Error(),
		})
		return
	}

	// 更新配置：标记已安装
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

	// 初始化 R2（如有配置）和审查 / Turnstile
	var r2Client *storage.R2Client
	if hasR2Config(&newCfg.R2) {
		r2Client, err = storage.NewR2Client(ctx, newCfg.R2)
		if err != nil {
			db.Close()
			c.JSON(http.StatusBadRequest, gin.H{
				"error":  "r2_init_failed",
				"detail": err.Error(),
			})
			return
		}
	}
	modService := moderation.NewService(newCfg.Moderation, newCfg.App.AllowedMimeTypes)

	var verifier *turnstile.Verifier
	if newCfg.Turnstile.Enabled && newCfg.Turnstile.SecretKey != "" {
		verifier = turnstile.NewVerifier(newCfg.Turnstile.SecretKey)
	}

	// 更新运行时
	s.mu.Lock()
	if s.db != nil && s.db != db {
		s.db.Close()
	}
	s.cfg = &newCfg
	s.db = db
	s.r2 = r2Client
	s.mod = modService
	s.verifier = verifier
	s.mu.Unlock()

	// 将依赖注入到 handlers 中（安装完成后才会真正使用）
	if s.bucketHandler != nil {
		s.bucketHandler.SetDeps(db, r2Client)
	}
	if s.imageHandler != nil {
		s.imageHandler.SetDeps(db, r2Client, modService)
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}