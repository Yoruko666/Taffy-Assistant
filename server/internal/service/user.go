package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	"taffy-server/internal/model"
	"taffy-server/internal/repository"
)

// ErrDuplicatePhone 手机号已注册。
var ErrDuplicatePhone = errors.New("phone already registered")

// ErrDuplicateEmail 邮箱已注册。
var ErrDuplicateEmail = errors.New("email already registered")

// ErrUserNotFound 用户不存在。
var ErrUserNotFound = errors.New("user not found")

// ErrInvalidPassword 密码错误。
var ErrInvalidPassword = errors.New("invalid password")

// ErrWeakPassword 新密码过短。
var ErrWeakPassword = errors.New("password too short")

// UserService 用户业务逻辑。
type UserService struct {
	repo *repository.UserRepo
}

// NewUserService 创建 UserService。
func NewUserService(repo *repository.UserRepo) *UserService {
	return &UserService{repo: repo}
}

// Register 用户注册，返回新用户 ID。
func (s *UserService) Register(ctx context.Context, phone, email, plainPassword, nickname string) (int64, error) {
	// 检查手机号是否已注册
	if phone != "" {
		if _, err := s.repo.GetByPhone(ctx, phone); err == nil {
			return 0, ErrDuplicatePhone
		} else if !errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("check phone: %w", err)
		}
	}
	// 检查邮箱是否已注册
	if email != "" {
		if _, err := s.repo.GetByEmail(ctx, email); err == nil {
			return 0, ErrDuplicateEmail
		} else if !errors.Is(err, sql.ErrNoRows) {
			return 0, fmt.Errorf("check email: %w", err)
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(plainPassword), bcrypt.DefaultCost)
	if err != nil {
		return 0, fmt.Errorf("hash password: %w", err)
	}

	u := &model.User{
		Phone:        phone,
		Email:        email,
		PasswordHash: string(hash),
		Nickname:     nickname,
	}
	if u.Nickname == "" {
		u.Nickname = "用户" + phone[len(phone)-4:]
	}

	return s.repo.Create(ctx, u)
}

// Login 用户登录（按手机号），返回用户信息。
func (s *UserService) Login(ctx context.Context, phone, plainPassword string) (*model.User, error) {
	u, err := s.repo.GetByPhone(ctx, phone)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("get user: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(plainPassword)); err != nil {
		return nil, ErrInvalidPassword
	}
	return u, nil
}

// UpdateUser 更新用户昵称、邮箱、头像。
func (s *UserService) UpdateUser(ctx context.Context, id int64, nickname, email, avatarURL string) error {
	if nickname == "" {
		return errors.New("nickname is required")
	}
	return s.repo.Update(ctx, id, nickname, email, avatarURL)
}

// ChangePassword 修改密码（需校验旧密码）。
func (s *UserService) ChangePassword(ctx context.Context, id int64, oldPassword, newPassword string) error {
	if newPassword == "" || len(newPassword) < 6 {
		return ErrWeakPassword
	}

	u, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(oldPassword)); err != nil {
		return ErrInvalidPassword
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return s.repo.UpdatePassword(ctx, id, string(hash))
}

// DeleteUser 删除用户。
func (s *UserService) DeleteUser(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

// VerifyPassword 按 ID 获取用户后校验密码，成功返回用户信息。
func (s *UserService) VerifyPassword(ctx context.Context, id int64, plainPassword string) (*model.User, error) {
	u, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, ErrUserNotFound
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(plainPassword)); err != nil {
		return nil, ErrInvalidPassword
	}
	return u, nil
}

// GetUser 按 ID 获取用户信息。
func (s *UserService) GetUser(ctx context.Context, id int64) (*model.User, error) {
	u, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}
