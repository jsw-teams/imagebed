package storage

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/jsw-teams/imagebed/internal/models"
)

// R2Pool 按 bucket 维度缓存 R2Client，支持多账号、多 endpoint。
type R2Pool struct {
	mu      sync.RWMutex
	clients map[uuid.UUID]*R2Client
}

// NewR2Pool 创建一个空的 R2 客户端池。
func NewR2Pool() *R2Pool {
	return &R2Pool{
		clients: make(map[uuid.UUID]*R2Client),
	}
}

// GetClientForBucket 返回指定 bucket 的 R2Client。
// 如果该 bucket 尚未创建 client，则根据 bucket 的 R2 配置新建并缓存。
func (p *R2Pool) GetClientForBucket(ctx context.Context, b *models.Bucket) (*R2Client, error) {
	if b == nil {
		return nil, fmt.Errorf("r2pool: nil bucket")
	}

	id := b.ID

	// 先尝试读缓存
	p.mu.RLock()
	if c, ok := p.clients[id]; ok {
		p.mu.RUnlock()
		return c, nil
	}
	p.mu.RUnlock()

	// 未命中缓存，创建新的 client
	client, err := NewR2ClientFromParams(
		ctx,
		b.R2AccountID,
		b.R2AccessKeyID,
		b.R2SecretAccessKey,
		b.R2Region,
		b.R2Endpoint,
	)
	if err != nil {
		return nil, err
	}

	// 写入缓存（双检，防止并发重复创建）
	p.mu.Lock()
	if existing, ok := p.clients[id]; ok {
		p.mu.Unlock()
		return existing, nil
	}
	p.clients[id] = client
	p.mu.Unlock()

	return client, nil
}

// InvalidateBucket 在更新桶的 R2 配置后调用，用于丢弃旧的 client。
func (p *R2Pool) InvalidateBucket(bucketID uuid.UUID) {
	p.mu.Lock()
	delete(p.clients, bucketID)
	p.mu.Unlock()
}

// Clear 清空所有缓存，主要用于测试或重置。
func (p *R2Pool) Clear() {
	p.mu.Lock()
	p.clients = make(map[uuid.UUID]*R2Client)
	p.mu.Unlock()
}
