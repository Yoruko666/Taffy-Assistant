package repository

import (
	"context"
	"database/sql"
	"fmt"

	"taffy-server/internal/model"
)

// UserRepo 用户表数据访问。
type UserRepo struct {
	db *sql.DB
}

// NewUserRepo 创建 UserRepo。
func NewUserRepo(db *sql.DB) *UserRepo {
	return &UserRepo{db: db}
}

// Create 插入一条用户记录，返回自增 user_id。
func (r *UserRepo) Create(ctx context.Context, u *model.User) (int64, error) {
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO users (phone, email, password_hash, nickname, avatar_url)
		 VALUES (?, ?, ?, ?, ?)`,
		u.Phone, u.Email, u.PasswordHash, u.Nickname, u.AvatarURL,
	)
	if err != nil {
		return 0, fmt.Errorf("insert user: %w", err)
	}
	id, _ := result.LastInsertId()
	return id, nil
}

// GetByID 按 user_id 查询。
func (r *UserRepo) GetByID(ctx context.Context, id int64) (*model.User, error) {
	u := &model.User{}
	err := r.db.QueryRowContext(ctx,
		`SELECT user_id, phone, email, password_hash, nickname, avatar_url, created_at, updated_at
		 FROM users WHERE user_id = ?`, id,
	).Scan(&u.UserID, &u.Phone, &u.Email, &u.PasswordHash, &u.Nickname, &u.AvatarURL, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return u, nil
}

// GetByPhone 按手机号查询。
func (r *UserRepo) GetByPhone(ctx context.Context, phone string) (*model.User, error) {
	u := &model.User{}
	err := r.db.QueryRowContext(ctx,
		`SELECT user_id, phone, email, password_hash, nickname, avatar_url, created_at, updated_at
		 FROM users WHERE phone = ?`, phone,
	).Scan(&u.UserID, &u.Phone, &u.Email, &u.PasswordHash, &u.Nickname, &u.AvatarURL, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get user by phone: %w", err)
	}
	return u, nil
}

// Update 更新用户昵称、邮箱、头像。
func (r *UserRepo) Update(ctx context.Context, id int64, nickname, email, avatarURL string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET nickname = ?, email = ?, avatar_url = ? WHERE user_id = ?`,
		nickname, email, avatarURL, id,
	)
	if err != nil {
		return fmt.Errorf("update user: %w", err)
	}
	return nil
}

// UpdatePassword 更新用户密码哈希。
func (r *UserRepo) UpdatePassword(ctx context.Context, id int64, hash string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ? WHERE user_id = ?`,
		hash, id,
	)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
}

// Delete 删除用户。
func (r *UserRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM users WHERE user_id = ?`, id,
	)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

// GetByEmail 按邮箱查询。
func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*model.User, error) {
	u := &model.User{}
	err := r.db.QueryRowContext(ctx,
		`SELECT user_id, phone, email, password_hash, nickname, avatar_url, created_at, updated_at
		 FROM users WHERE email = ?`, email,
	).Scan(&u.UserID, &u.Phone, &u.Email, &u.PasswordHash, &u.Nickname, &u.AvatarURL, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return u, nil
}
