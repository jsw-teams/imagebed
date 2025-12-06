package models

import (
	"context"
	"errors"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PickAutoBucketID 从 buckets 表中自动选出一个「可用」的桶 ID。
// 选择策略：
//  1. 仅考虑 is_active = TRUE 的桶；
//  2. max_bytes = 0 视为不限空间，优先级最高；
//  3. 对于 max_bytes > 0 的桶，如果 current_bytes >= max_bytes 视为已满，跳过；
//  4. 在所有「有空间」的桶中，按剩余空间从大到小选择一个；
//  5. 如果没有任何可用桶，返回空字符串 ""，不视为错误。
func PickAutoBucketID(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	if pool == nil {
		return "", errors.New("nil db pool")
	}

	const q = `
		SELECT
			id,
			max_bytes,
			current_bytes,
			is_active
		FROM buckets
	`

	rows, err := pool.Query(ctx, q)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var (
		bestID    uuid.UUID
		bestScore float64
		found     bool
	)

	for rows.Next() {
		var (
			id           uuid.UUID
			maxBytes     int64
			currentBytes int64
			isActive     bool
		)
		if err := rows.Scan(&id, &maxBytes, &currentBytes, &isActive); err != nil {
			return "", err
		}
		if !isActive {
			continue
		}

		// 计算“评分”：越大越优先
		var score float64

		if maxBytes == 0 {
			// 不限配额：给一个极大的分数，始终优先
			score = math.MaxFloat64 / 2
		} else {
			if currentBytes >= maxBytes {
				// 已满，跳过
				continue
			}
			remaining := maxBytes - currentBytes
			if remaining <= 0 {
				continue
			}
			score = float64(remaining)
		}

		if !found || score > bestScore {
			found = true
			bestScore = score
			bestID = id
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}

	if !found {
		// 没有找到可用桶，不作为错误，由上层返回「暂无可用存储桶」
		return "", nil
	}
	return bestID.String(), nil
}