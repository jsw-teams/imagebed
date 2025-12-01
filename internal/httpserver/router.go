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
	tsCfg    *models.TurnstileSettings

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

	// R2 可选：未配置时置为 nil
	var r2Client *storage.R2Client
	if hasR2Config(&cfg.R2) {
		r2Client, err = storage.NewR2Client(ctx, cfg.R2)
		if err != nil {
			db.Close()
			return fmt.Errorf("initRuntime: r2 init failed: %w", err)
		}
	}

	modService := moderation.NewService(cfg.Moderation, cfg.App.AllowedMimeTypes)

	// Turnstile 配置改由数据库提供
	tsCfg, err := models.GetTurnstileSettings(ctx, db)
	if err != nil {
		db.Close()
		return fmt.Errorf("initRuntime: load turnstile settings failed: %w", err)
	}
	var verifier *turnstile.Verifier
	if tsCfg != nil && tsCfg.Enabled && tsCfg.SecretKey != "" {
		verifier = turnstile.NewVerifier(tsCfg.SecretKey)
	}

	s.mu.Lock()
	if s.db != nil {
		s.db.Close()
	}
	s.db = db
	s.r2 = r2Client
	s.mod = modService
	s.tsCfg = tsCfg
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

func (s *Server) getDB() *pgxpool.Pool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db
}

func (s *Server) buildEngine() {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.SecurityHeaders())

	// 后台静态管理页（/admin/）
	r.Static("/admin", "./web/admin")

	// /setup 仅在未安装时能访问；安装完成后直接 404
	r.GET("/setup", s.serveSetupPage)
	r.GET("/setup/", s.serveSetupPage)

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

	// 初始化安装 API（两步）
	r.POST("/api/setup/database", s.handleSetupDatabase)
	r.POST("/api/setup/admin", s.handleSetupAdmin)

	// 已安装后才能访问的 API
	api := r.Group("/api")
	api.Use(middleware.RequireInstalled(func() bool { return s.IsInstalled() }))
	{
		api.GET("/healthz", handlers.HealthHandler())

		s.bucketHandler = handlers.NewBucketHandler()
		s.imageHandler = handlers.NewImageHandler(s.cfg.App.MaxUploadBytes)

		// 如果启动时就安装好了，注入依赖
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

		// bucket 管理接口：所有读写都需要管理员登录（不叠加 Turnstile）
		bucketsAdmin := api.Group("/buckets")
		bucketsAdmin.Use(middleware.AdminAuth(s.getDB))
		{
			bucketsAdmin.GET("", s.bucketHandler.ListBuckets)
			bucketsAdmin.GET("/:id", s.bucketHandler.GetBucket)
			bucketsAdmin.POST("", s.bucketHandler.CreateBucket)
			bucketsAdmin.PUT("/:id", s.bucketHandler.UpdateBucket)
			bucketsAdmin.DELETE("/:id", s.bucketHandler.DeleteBucket)
		}

		// 旧接口：指定桶上传（供第三方程序使用），使用 Turnstile 防滥用
		api.POST("/buckets/:bucketID/upload", tsMiddleware, s.imageHandler.Upload)

		// 新接口：自动分配桶上传（给前端上传页使用），同样使用 Turnstile 防滥用
		api.POST("/upload", tsMiddleware, s.handleAutoUpload)

		api.GET("/images/:id", s.imageHandler.GetImageMeta)

		// 后台登录检测接口
		api.POST("/admin/login", s.handleAdminLogin)

		// 后台 Turnstile 配置接口（需管理员登录）
		adminSec := api.Group("/admin")
		adminSec.Use(middleware.AdminAuth(s.getDB))
		{
			adminSec.GET("/turnstile", s.handleAdminGetTurnstile)
			adminSec.POST("/turnstile", s.handleAdminUpdateTurnstile)
		}
	}

	// 图片访问统一入口
	r.GET("/i/:id", func(c *gin.Context) {
		if !s.IsInstalled() {
			c.Redirect(http.StatusFound, "/setup/")
			return
		}
		s.imageHandler.ServeImage(c)
	})

	s.engine = r
}

// 安装页：未安装才可以访问；已安装直接 404，禁止再次初始化
func (s *Server) serveSetupPage(c *gin.Context) {
	if s.IsInstalled() {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.File("./web/setup/index.html")
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

// Turnstile 启用状态来自数据库配置
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

	// 安装完成后，同步从数据库载入 Turnstile 配置
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

	s.mu.Lock()
	if s.db != nil && s.db != db {
		s.db.Close()
	}
	s.cfg = &newCfg
	s.db = db
	s.r2 = r2Client
	s.mod = modService
	s.tsCfg = tsCfg
	s.verifier = verifier
	s.mu.Unlock()

	if s.bucketHandler != nil {
		s.bucketHandler.SetDeps(db, r2Client)
	}
	if s.imageHandler != nil {
		s.imageHandler.SetDeps(db, r2Client, modService)
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------- 后台登录 API ----------

type adminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleAdminLogin(c *gin.Context) {
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

	var req adminLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	req.Password = strings.TrimSpace(req.Password)
	if req.Username == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing_fields"})
		return
	}

	ok, err := models.CheckAdminCredentials(c.Request.Context(), db, req.Username, req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":  "auth_failed",
			"detail": err.Error(),
		})
		return
	}
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid_credentials"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------- 自动分配桶并上传 ----------

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

	// 把自动挑选出来的 bucketID 写入 Gin 的路径参数，
	// 复用现有的 Upload 逻辑（它会通过 c.Param("bucketID") 取桶 ID）
	c.Params = append(c.Params, gin.Param{
		Key:   "bucketID",
		Value: bucketID,
	})

	s.imageHandler.Upload(c)
}

// ---------- 后台 Turnstile 配置 API ----------

type turnstileConfigResponse struct {
	Enabled   bool   `json:"enabled"`
	SiteKey   string `json:"site_key"`
	HasSecret bool   `json:"has_secret"`
}

type updateTurnstileRequest struct {
	Enabled   bool   `json:"enabled"`
	SiteKey   string `json:"site_key"`
	SecretKey string `json:"secret_key"`
	TestToken string `json:"test_token"` // 管理后台小组件生成的真实 token
}

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
	req.TestToken = strings.TrimSpace(req.TestToken)

	// 启用时：必须有 site_key / secret_key / test_token，并且 siteverify 成功才允许启用
	if req.Enabled {
		if req.SiteKey == "" || req.SecretKey == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "enabled_requires_site_and_secret"})
			return
		}
		if req.TestToken == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "enabled_requires_test_token"})
			return
		}
		if err := verifyTurnstileConfig(c.Request.Context(), req.SecretKey, req.TestToken); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":  "turnstile_verify_failed",
				"detail": err.Error(),
			})
			return
		}
	}

	// 如果只是保存 Key 而不启用，可以不验证，直接写入 enabled=false
	if !req.Enabled && (req.SiteKey == "" && req.SecretKey == "") {
		// 允许直接关闭并清空
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

// ---------- Turnstile 配置验证（用真实 token 调用 siteverify） ----------

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

	// 这里严格区分：如果是 secret 错误，明确提示；否则视为 token 无效
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

	if len(out.ErrorCodes) > 0 {
		return fmt.Errorf("invalid token: %s", strings.Join(out.ErrorCodes, ","))
	}

	return fmt.Errorf("turnstile verification failed")
}