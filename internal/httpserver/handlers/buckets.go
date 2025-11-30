package handlers

import (
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jsw-teams/imagebed/internal/models"
	"github.com/jsw-teams/imagebed/internal/storage"
)

type BucketHandler struct {
	mu sync.RWMutex
	db *pgxpool.Pool
	r2 *storage.R2Client
}

func NewBucketHandler() *BucketHandler {
	return &BucketHandler{}
}

func (h *BucketHandler) SetDeps(db *pgxpool.Pool, r2 *storage.R2Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.db = db
	h.r2 = r2
}

func (h *BucketHandler) deps() (*pgxpool.Pool, *storage.R2Client) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.db, h.r2
}

type createBucketRequest struct {
	Name     string `json:"name" binding:"required"`
	R2Bucket string `json:"r2_bucket" binding:"required"`
	MaxBytes int64  `json:"max_bytes" binding:"required"`
}

func (h *BucketHandler) CreateBucket(c *gin.Context) {
	db, r2 := h.deps()
	if db == nil || r2 == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":     "service_not_ready",
			"setup_url": "/setup/",
		})
		return
	}

	var req createBucketRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || req.MaxBytes <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid name or max_bytes"})
		return
	}

	ctx := c.Request.Context()

	if err := r2.CreateBucket(ctx, req.R2Bucket); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to create R2 bucket"})
		return
	}

	b := &models.Bucket{
		ID:           uuid.New(),
		Name:         req.Name,
		R2Bucket:     req.R2Bucket,
		MaxBytes:     req.MaxBytes,
		CurrentBytes: 0,
		IsActive:     true,
	}

	if err := models.InsertBucket(ctx, db, b); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save bucket"})
		return
	}

	c.JSON(http.StatusCreated, b)
}

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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list buckets"})
		return
	}
	c.JSON(http.StatusOK, list)
}

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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	ctx := c.Request.Context()
	b, err := models.GetBucketByID(ctx, db, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "bucket not found"})
		return
	}
	c.JSON(http.StatusOK, b)
}

type updateBucketRequest struct {
	MaxBytes int64 `json:"max_bytes" binding:"required"`
}

func (h *BucketHandler) UpdateBucket(c *gin.Context) {
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}

	var req updateBucketRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.MaxBytes <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid max_bytes"})
		return
	}

	ctx := c.Request.Context()
	b, err := models.UpdateBucketMaxBytes(ctx, db, id, req.MaxBytes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update bucket"})
		return
	}

	c.JSON(http.StatusOK, b)
}

func (h *BucketHandler) DeleteBucket(c *gin.Context) {
	db, r2 := h.deps()
	if db == nil || r2 == nil {
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

	b, err := models.GetBucketByID(ctx, db, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "bucket not found"})
		return
	}

	if err := r2.DeleteBucket(ctx, b.R2Bucket); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to delete R2 bucket (ensure empty first)"})
		return
	}

	if err := models.DeleteBucket(ctx, db, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete bucket"})
		return
	}

	c.Status(http.StatusNoContent)
}