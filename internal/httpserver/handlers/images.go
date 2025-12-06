package handlers

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jsw-teams/imagebed/internal/moderation"
	"github.com/jsw-teams/imagebed/internal/models"
	"github.com/jsw-teams/imagebed/internal/storage"
)

type ImageHandler struct {
	mu             sync.RWMutex
	db             *pgxpool.Pool
	r2Pool         *storage.R2Pool
	moderation     *moderation.Service
	maxUploadBytes int64
}

func NewImageHandler(maxUploadBytes int64) *ImageHandler {
	return &ImageHandler{
		maxUploadBytes: maxUploadBytes,
	}
}

// SetDeps 注入依赖：数据库连接池 + R2 客户端池 + 审核服务。
func (h *ImageHandler) SetDeps(db *pgxpool.Pool, r2Pool *storage.R2Pool, mod *moderation.Service) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.db = db
	h.r2Pool = r2Pool
	h.moderation = mod
}

func (h *ImageHandler) deps() (*pgxpool.Pool, *storage.R2Pool, *moderation.Service) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.db, h.r2Pool, h.moderation
}

// Upload 处理图片上传：严格限制文件类型、大小，并根据桶配额进行校验。
func (h *ImageHandler) Upload(c *gin.Context) {
	db, r2Pool, modService := h.deps()
	if db == nil || r2Pool == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":     "service_not_ready",
			"setup_url": "/setup/",
		})
		return
	}

	bucketIDStr := c.Param("bucketID")
	bucketID, err := uuid.Parse(bucketIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid bucket id"})
		return
	}

	if err := c.Request.ParseMultipartForm(h.maxUploadBytes); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file too large"})
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing file"})
		return
	}
	defer file.Close()

	if header.Size <= 0 || header.Size > h.maxUploadBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file size invalid or too big"})
		return
	}

	data, contentType, err := readAndDetectImage(file, h.maxUploadBytes)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if modService != nil && modService.Enabled() {
		decision, err := modService.Moderate(c.Request.Context(), contentType, data)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "moderation error"})
			return
		}
		if decision == moderation.DecisionRejected {
			c.JSON(http.StatusBadRequest, gin.H{"error": "image rejected by moderation"})
			return
		}
	}

	ctx := c.Request.Context()

	tx, err := db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to begin tx"})
		return
	}
	defer tx.Rollback(ctx)

	bucket, err := models.GetBucketForUpdate(ctx, tx, bucketID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "bucket not found"})
		return
	}

	size := int64(len(data))

	// 配额：max_bytes == 0 表示不限，否则要求 current + size <= max_bytes。
	if bucket.MaxBytes > 0 && bucket.CurrentBytes+size > bucket.MaxBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "bucket quota exceeded"})
		return
	}

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" {
		ext = ".bin"
	}
	objectKey := uuid.New().String() + ext

	// 每个桶使用独立 R2 账号/endpoint。
	r2Client, err := r2Pool.GetClientForBucket(ctx, bucket)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error":  "r2_connect_failed",
			"detail": err.Error(),
		})
		return
	}

	if err := r2Client.PutObject(ctx, bucket.R2Bucket, objectKey, contentType, bytes.NewReader(data), size); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to upload to R2"})
		return
	}

	img := &models.Image{
		ID:          uuid.New(),
		BucketID:    bucket.ID,
		ObjectKey:   objectKey,
		SizeBytes:   size,
		ContentType: contentType,
		Status:      string(moderation.DecisionApproved),
	}

	if err := models.InsertImage(ctx, tx, img); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save image"})
		return
	}

	if err := models.IncrementBucketSize(ctx, tx, bucket.ID, size); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update bucket usage"})
		return
	}

	if err := tx.Commit(ctx); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to commit tx"})
		return
	}

	// 直链仅用于调试/管理，外部实际访问统一走 /i/{id}
	url := r2Client.PublicURL(bucket.R2Bucket, objectKey)

	c.JSON(http.StatusCreated, gin.H{
		"id":           img.ID,
		"url":          url,
		"content_type": contentType,
		"size_bytes":   size,
	})
}

// GetImageMeta 查询图片元信息。
func (h *ImageHandler) GetImageMeta(c *gin.Context) {
	db, r2Pool, _ := h.deps()
	if db == nil || r2Pool == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":     "service_not_ready",
			"setup_url": "/setup/",
		})
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	ctx := c.Request.Context()

	img, err := models.GetImageByID(ctx, db, id)
	if err != nil || img.Status != string(moderation.DecisionApproved) {
		c.JSON(http.StatusNotFound, gin.H{"error": "image not found"})
		return
	}

	bucket, err := models.GetBucketByID(ctx, db, img.BucketID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "bucket not found"})
		return
	}

	r2Client, err := r2Pool.GetClientForBucket(ctx, bucket)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error":  "r2_connect_failed",
			"detail": err.Error(),
		})
		return
	}

	url := r2Client.PublicURL(bucket.R2Bucket, img.ObjectKey)

	c.JSON(http.StatusOK, gin.H{
		"id":           img.ID,
		"url":          url,
		"bucket_id":    img.BucketID,
		"content_type": img.ContentType,
		"size_bytes":   img.SizeBytes,
		"created_at":   img.CreatedAt,
	})
}

// ServeImage 通过内部 ID 302 重定向到 R2 直链。
func (h *ImageHandler) ServeImage(c *gin.Context) {
	db, r2Pool, _ := h.deps()
	if db == nil || r2Pool == nil {
		c.Redirect(http.StatusFound, "/setup/")
		return
	}

	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	ctx := c.Request.Context()

	img, err := models.GetImageByID(ctx, db, id)
	if err != nil || img.Status != string(moderation.DecisionApproved) {
		c.JSON(http.StatusNotFound, gin.H{"error": "image not found"})
		return
	}
	bucket, err := models.GetBucketByID(ctx, db, img.BucketID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "bucket not found"})
		return
	}

	r2Client, err := r2Pool.GetClientForBucket(ctx, bucket)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"error":  "r2_connect_failed",
			"detail": err.Error(),
		})
		return
	}

	url := r2Client.PublicURL(bucket.R2Bucket, img.ObjectKey)
	c.Redirect(http.StatusFound, url)
}

// 严格检查文件类型，防止伪造 Content-Type 绕过
func readAndDetectImage(f multipart.File, maxBytes int64) ([]byte, string, error) {
	var buf bytes.Buffer
	limited := io.LimitReader(f, maxBytes+1)

	n, err := io.Copy(&buf, limited)
	if err != nil {
		return nil, "", err
	}
	if n > maxBytes {
		return nil, "", errFileTooLarge
	}

	data := buf.Bytes()
	if len(data) == 0 {
		return nil, "", errEmptyFile
	}

	ct := http.DetectContentType(data)
	if !strings.HasPrefix(ct, "image/") {
		return nil, "", errNotImage
	}

	return data, ct, nil
}

var (
	errFileTooLarge = &uploadError{"file exceeds limit"}
	errEmptyFile    = &uploadError{"empty file"}
	errNotImage     = &uploadError{"not an image file"}
)

type uploadError struct {
	msg string
}

func (e *uploadError) Error() string { return e.msg }