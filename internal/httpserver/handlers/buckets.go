package handlers

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jsw-teams/imagebed/internal/models"
	"github.com/jsw-teams/imagebed/internal/storage"
)

// BucketHandler 负责 /api/admin/buckets 相关接口。
type BucketHandler struct {
	mu     sync.RWMutex
	db     *pgxpool.Pool
	r2Pool *storage.R2Pool
}

func NewBucketHandler() *BucketHandler {
	return &BucketHandler{}
}

// SetDeps 注入依赖：数据库连接池 + R2 客户端池。
func (h *BucketHandler) SetDeps(db *pgxpool.Pool, pool *storage.R2Pool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.db = db
	h.r2Pool = pool
}

func (h *BucketHandler) deps() (*pgxpool.Pool, *storage.R2Pool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.db, h.r2Pool
}

// ---- 请求/响应结构 ----

type createBucketRequest struct {
	Name              string `json:"name"`
	R2Bucket          string `json:"r2_bucket"`
	R2AccountID       string `json:"r2_account_id"`
	R2AccessKeyID     string `json:"r2_access_key_id"`
	R2SecretAccessKey string `json:"r2_secret_access_key"`
	R2Region          string `json:"r2_region"`
	R2Endpoint        string `json:"r2_endpoint"`
	// 0 表示不限；>0 为字节配额（前端可按 GB 转换为字节再提交）
	MaxBytes int64 `json:"max_bytes"`
}

// 更新时允许部分字段留空只改部分配置，但为简单起见，推荐前端每次传完整配置。
// Secret 用 *string，以便区分“没传”（不改）和“传了空字符串”（清空）。
type updateBucketRequest struct {
	Name              string  `json:"name"`
	R2Bucket          string  `json:"r2_bucket"`
	R2AccountID       string  `json:"r2_account_id"`
	R2AccessKeyID     string  `json:"r2_access_key_id"`
	R2SecretAccessKey *string `json:"r2_secret_access_key"`
	R2Region          string  `json:"r2_region"`
	R2Endpoint        string  `json:"r2_endpoint"`
	MaxBytes          int64   `json:"max_bytes"`
	IsActive          *bool   `json:"is_active"`
}

// 返回给前端的桶信息，不包含密钥，只包含 has_secret。
type bucketResponse struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	R2Bucket      string    `json:"r2_bucket"`
	R2AccountID   string    `json:"r2_account_id"`
	R2AccessKeyID string    `json:"r2_access_key_id"`
	R2Region      string    `json:"r2_region"`
	R2Endpoint    string    `json:"r2_endpoint"`
	MaxBytes      int64     `json:"max_bytes"`
	CurrentBytes  int64     `json:"current_bytes"`
	IsActive      bool      `json:"is_active"`
	HasSecret     bool      `json:"has_secret"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func bucketToResponse(b *models.Bucket) bucketResponse {
	return bucketResponse{
		ID:            b.ID,
		Name:          b.Name,
		R2Bucket:      b.R2Bucket,
		R2AccountID:   b.R2AccountID,
		R2AccessKeyID: b.R2AccessKeyID,
		R2Region:      b.R2Region,
		R2Endpoint:    b.R2Endpoint,
		MaxBytes:      b.MaxBytes,
		CurrentBytes:  b.CurrentBytes,
		IsActive:      b.IsActive,
		HasSecret:     b.R2SecretAccessKey != "",
		CreatedAt:     b.CreatedAt,
		UpdatedAt:     b.UpdatedAt,
	}
}

// ---- 工具函数 ----

// 校验 endpoint：必须是 http/https，不能包含路径（不允许带 /bucket）。
func validateEndpoint(ep string) error {
	if ep == "" {
		return nil
	}
	u, err := url.Parse(ep)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return &url.Error{Op: "parse", URL: ep, Err: http.ErrNotSupported}
	}
	if u.Host == "" {
		return &url.Error{Op: "parse", URL: ep, Err: http.ErrNoLocation}
	}
	// 不允许额外 path，避免用户填入 ".../bucket"
	if u.Path != "" && u.Path != "/" {
		return &url.Error{Op: "parse", URL: ep, Err: http.ErrUseLastResponse}
	}
	return nil
}

// ---- Handlers ----

// CreateBucket 创建一个新的桶配置，并尝试在对应 R2 账号下创建真实桶。
func (h *BucketHandler) CreateBucket(c *gin.Context) {
	db, _ := h.deps()
	if db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":     "service_not_ready",
			"setup_url": "/setup/",
		})
		return
	}

	var req createBucketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}

	trim := func(v string) string { return strings.TrimSpace(v) }
	req.Name = trim(req.Name)
	req.R2Bucket = trim(req.R2Bucket)
	req.R2AccountID = trim(req.R2AccountID)
	req.R2AccessKeyID = trim(req.R2AccessKeyID)
	req.R2SecretAccessKey = trim(req.R2SecretAccessKey)
	req.R2Region = trim(req.R2Region)
	req.R2Endpoint = trim(req.R2Endpoint)

	if req.Name == "" || req.R2Bucket == "" || req.R2AccountID == "" ||
		req.R2AccessKeyID == "" || req.R2SecretAccessKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing_required_fields"})
		return
	}

	if req.MaxBytes < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_max_bytes"})
		return
	}
	if req.R2Region == "" {
		req.R2Region = "auto"
	}

	if err := validateEndpoint(req.R2Endpoint); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":  "invalid_r2_endpoint",
			"detail": err.Error(),
		})
		return
	}

	ctx := c.Request.Context()

	// 尝试用该配置连接 R2 并创建桶（如果桶已存在可能返回错误）
	r2Client, err := storage.NewR2ClientFromParams(
		ctx,
		req.R2AccountID,
		req.R2AccessKeyID,
		req.R2SecretAccessKey,
		req.R2Region,
		req.R2Endpoint,
	)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error":  "r2_connect_failed",
			"detail": err.Error(),
		})
		return
	}

	if err := r2Client.CreateBucket(ctx, req.R2Bucket); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error":  "r2_create_bucket_failed",
			"detail": err.Error(),
		})
		return
	}

	b := &models.Bucket{
		ID:                uuid.New(),
		Name:              req.Name,
		R2Bucket:          req.R2Bucket,
		R2AccountID:       req.R2AccountID,
		R2AccessKeyID:     req.R2AccessKeyID,
		R2SecretAccessKey: req.R2SecretAccessKey,
		R2Region:          req.R2Region,
		R2Endpoint:        req.R2Endpoint,
		MaxBytes:          req.MaxBytes,
		CurrentBytes:      0,
		IsActive:          true,
	}

	if err := models.InsertBucket(ctx, db, b); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_save_bucket"})
		return
	}

	c.JSON(http.StatusCreated, bucketToResponse(b))
}

// ListBuckets 返回所有桶配置（不包含密钥，只包含 has_secret）。
func (h *BucketHandler) ListBuckets(c *gin.Context) {
	db, _ := h.deps()
	if db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":     "service_not_ready",
			"setup_url": "/setup/",
		})
		return
	}

	ctx := c.Request.Context()
	list, err := models.ListBuckets(ctx, db)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_list_buckets"})
		return
	}

	resp := make([]bucketResponse, 0, len(list))
	for i := range list {
		resp = append(resp, bucketToResponse(&list[i]))
	}
	c.JSON(http.StatusOK, resp)
}

// GetBucket 获取单个桶配置。
func (h *BucketHandler) GetBucket(c *gin.Context) {
	db, _ := h.deps()
	if db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":     "service_not_ready",
			"setup_url": "/setup/",
		})
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_id"})
		return
	}

	ctx := c.Request.Context()
	b, err := models.GetBucketByID(ctx, db, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "bucket_not_found"})
		return
	}
	c.JSON(http.StatusOK, bucketToResponse(b))
}

// UpdateBucket 更新桶配置（名称 / R2 配置 / 配额 / 启用状态）。
func (h *BucketHandler) UpdateBucket(c *gin.Context) {
	db, pool := h.deps()
	if db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":     "service_not_ready",
			"setup_url": "/setup/",
		})
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_id"})
		return
	}

	var req updateBucketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request"})
		return
	}

	trim := func(v string) string { return strings.TrimSpace(v) }
	req.Name = trim(req.Name)
	req.R2Bucket = trim(req.R2Bucket)
	req.R2AccountID = trim(req.R2AccountID)
	req.R2AccessKeyID = trim(req.R2AccessKeyID)
	req.R2Region = trim(req.R2Region)
	req.R2Endpoint = trim(req.R2Endpoint)
	if req.R2SecretAccessKey != nil {
		val := trim(*req.R2SecretAccessKey)
		req.R2SecretAccessKey = &val
	}

	if req.MaxBytes < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_max_bytes"})
		return
	}
	if req.R2Endpoint != "" {
		if err := validateEndpoint(req.R2Endpoint); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":  "invalid_r2_endpoint",
				"detail": err.Error(),
			})
			return
		}
	}

	ctx := c.Request.Context()

	// 先取出当前配置
	b, err := models.GetBucketByID(ctx, db, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "bucket_not_found"})
		return
	}

	// 应用更新值（空字符串就保持原值）
	if req.Name != "" {
		b.Name = req.Name
	}
	if req.R2Bucket != "" {
		b.R2Bucket = req.R2Bucket
	}
	if req.R2AccountID != "" {
		b.R2AccountID = req.R2AccountID
	}
	if req.R2AccessKeyID != "" {
		b.R2AccessKeyID = req.R2AccessKeyID
	}
	if req.R2Region != "" {
		b.R2Region = req.R2Region
	}
	if req.R2Endpoint != "" {
		b.R2Endpoint = req.R2Endpoint
	}
	if req.R2SecretAccessKey != nil {
		// 允许清空密钥（不推荐，但保留可能性）
		b.R2SecretAccessKey = *req.R2SecretAccessKey
	}
	if req.MaxBytes >= 0 {
		b.MaxBytes = req.MaxBytes
	}
	if req.IsActive != nil {
		b.IsActive = *req.IsActive
	}

	// 这里简单使用 UPDATE 语句重写全部字段，复用 InsertBucket 的列名顺序
	// 也可以在 models 中单独写一个 UpdateBucket 函数，这里暂时内联。
	_, err = db.Exec(ctx, `
		UPDATE buckets
		SET
			name                 = $2,
			r2_bucket            = $3,
			r2_account_id        = $4,
			r2_access_key_id     = $5,
			r2_secret_access_key = $6,
			r2_region            = $7,
			r2_endpoint          = $8,
			max_bytes            = $9,
			is_active            = $10,
			updated_at           = now()
		WHERE id = $1
	`,
		b.ID,
		b.Name,
		b.R2Bucket,
		b.R2AccountID,
		b.R2AccessKeyID,
		b.R2SecretAccessKey,
		b.R2Region,
		b.R2Endpoint,
		b.MaxBytes,
		b.IsActive,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_update_bucket"})
		return
	}

	// 更新后，清理缓存的 R2Client，避免继续使用旧配置。
	if pool != nil {
		pool.InvalidateBucket(b.ID)
	}

	// 重新读取，保证返回的时间戳是最新的
	b2, err := models.GetBucketByID(ctx, db, b.ID)
	if err != nil {
		// 如果这里失败，就返回旧对象，至少让前端有数据
		c.JSON(http.StatusOK, bucketToResponse(b))
		return
	}

	c.JSON(http.StatusOK, bucketToResponse(b2))
}

// DeleteBucket 删除桶配置。
// 注意：此操作 **不会** 自动删除 R2 上真实的桶，只删除本地配置。
func (h *BucketHandler) DeleteBucket(c *gin.Context) {
	db, pool := h.deps()
	if db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":     "service_not_ready",
			"setup_url": "/setup/",
		})
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_id"})
		return
	}

	ctx := c.Request.Context()

	// 确认存在
	b, err := models.GetBucketByID(ctx, db, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "bucket_not_found"})
		return
	}

	// 删除配置记录（images 有外键 ON DELETE CASCADE）
	if err := models.DeleteBucket(ctx, db, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed_to_delete_bucket"})
		return
	}

	// 丢弃缓存的 R2Client
	if pool != nil {
		pool.InvalidateBucket(b.ID)
	}

	// 不删除 R2 上真实的桶，交由用户在 Cloudflare 面板或后续“危险操作”接口中手动处理
	c.Status(http.StatusNoContent)
}