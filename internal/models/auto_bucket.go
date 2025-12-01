package models

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PickAutoBucketID 从 buckets 表中自动选一个桶的 ID 返回。
// 当前实现：简单选任意一个桶（LIMIT 1）；如果以后需要“按剩余空间选桶”，可以在这里升级 SQL。
func PickAutoBucketID(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	if pool == nil {
		return "", errors.New("nil db pool")
	}

	const q = `SELECT id::text FROM buckets LIMIT 1`

	var id string
	if err := pool.QueryRow(ctx, q).Scan(&id); err != nil {
		if err.Error() == "no rows in result set" {
			// 没有桶，返回空字符串，由调用方决定提示文案
			return "", nil
		}
		return "", err
	}

	return id, nil
}
