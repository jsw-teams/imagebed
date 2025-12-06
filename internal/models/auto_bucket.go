package models

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PickAutoBucketID 从 buckets 表中自动选一个桶的 ID 返回。
// 新实现：只选择 is_active=true 且尚未“用满”的桶：
//   - max_bytes <= 0 表示不限配额，一直可用；
//   - 否则要求 current_bytes < max_bytes。
// 选择策略：
//   1) 优先不限配额的桶；
//   2) 其次按剩余空间 (max_bytes - current_bytes) 从大到小；
//   3) 再按 created_at 由早到晚，保证结果相对稳定。
func PickAutoBucketID(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	if pool == nil {
		return "", errors.New("nil db pool")
	}

	const q = `
SELECT id::text
FROM buckets
WHERE
    is_active = TRUE
    AND (
        max_bytes <= 0
        OR current_bytes < max_bytes
    )
ORDER BY
    -- 不限配额的桶优先
    CASE WHEN max_bytes <= 0 THEN 0 ELSE 1 END,
    -- 其次剩余空间从大到小
    CASE
        WHEN max_bytes <= 0 THEN NULL
        ELSE (max_bytes - current_bytes)
    END DESC,
    -- 最后按创建时间，保证一定稳定性
    created_at ASC
LIMIT 1
`

	var id string
	err := pool.QueryRow(ctx, q).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 没有符合条件的桶，返回空 ID，由调用方决定提示文案
			return "", nil
		}
		return "", err
	}

	return id, nil
}