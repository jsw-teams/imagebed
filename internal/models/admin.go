package models

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type Admin struct {
	ID           uuid.UUID `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func HasAnyAdmin(ctx context.Context, db *pgxpool.Pool) (bool, error) {
	var count int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM admins`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func CreateAdmin(ctx context.Context, db *pgxpool.Pool, username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	_, err = db.Exec(ctx, `
		INSERT INTO admins (id, username, password_hash)
		VALUES ($1, $2, $3)
	`,
		uuid.New(),
		username,
		string(hash),
	)
	return err
}

// EnsureInitialAdmin：如果还没有管理员，就创建一个
func EnsureInitialAdmin(ctx context.Context, db *pgxpool.Pool, username, password string) error {
	has, err := HasAnyAdmin(ctx, db)
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	return CreateAdmin(ctx, db, username, password)
}
