package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"system/internal/model"
	"system/internal/repository"
)

// ErrDeviceNotFound 设备不存在。
var ErrDeviceNotFound = errors.New("device not found")

// ErrNotDeviceOwner 非设备拥有者。
var ErrNotDeviceOwner = errors.New("not device owner")

// DeviceService 设备业务逻辑。
type DeviceService struct {
	deviceRepo *repository.DeviceRepo
	stateRepo  *repository.DeviceStateRepo
}

// NewDeviceService 创建 DeviceService。
func NewDeviceService(deviceRepo *repository.DeviceRepo, stateRepo *repository.DeviceStateRepo) *DeviceService {
	return &DeviceService{deviceRepo: deviceRepo, stateRepo: stateRepo}
}

// CreateDevice 创建设备并初始化其状态。
func (s *DeviceService) CreateDevice(ctx context.Context, d *model.Device) error {
	if err := s.deviceRepo.Create(ctx, d); err != nil {
		return fmt.Errorf("create device: %w", err)
	}
	// 初始化设备状态（默认关机）
	initState := &model.DeviceState{
		DeviceID: d.DeviceID,
		Power:    false,
	}
	if err := s.stateRepo.Upsert(ctx, initState); err != nil {
		return fmt.Errorf("init device state: %w", err)
	}
	return nil
}

// GetDevice 获取设备信息。
func (s *DeviceService) GetDevice(ctx context.Context, deviceID string) (*model.Device, error) {
	d, err := s.deviceRepo.GetByID(ctx, deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrDeviceNotFound
		}
		return nil, fmt.Errorf("get device: %w", err)
	}
	return d, nil
}

// ListDevicesByOwner 查询某用户的所有设备。
func (s *DeviceService) ListDevicesByOwner(ctx context.Context, ownerID int64) ([]*model.Device, error) {
	return s.deviceRepo.ListByOwner(ctx, ownerID)
}

// GetDeviceState 获取设备状态。
func (s *DeviceService) GetDeviceState(ctx context.Context, deviceID string) (*model.DeviceState, error) {
	state, err := s.stateRepo.Get(ctx, deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrDeviceNotFound
		}
		return nil, fmt.Errorf("get device state: %w", err)
	}
	return state, nil
}

// ListDeviceStatesByOwner 查询某用户所有设备的状态。
func (s *DeviceService) ListDeviceStatesByOwner(ctx context.Context, ownerID int64) ([]*model.DeviceState, error) {
	return s.stateRepo.ListByOwner(ctx, ownerID)
}

// UpdateDeviceState 更新设备状态（含权限校验）。
func (s *DeviceService) UpdateDeviceState(ctx context.Context, userID int64, state *model.DeviceState) error {
	// 校验设备归属
	d, err := s.deviceRepo.GetByID(ctx, state.DeviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDeviceNotFound
		}
		return fmt.Errorf("get device: %w", err)
	}
	if d.OwnerID != userID {
		return ErrNotDeviceOwner
	}
	return s.stateRepo.Upsert(ctx, state)
}

// DeleteDevice 删除设备（级联删状态）。
func (s *DeviceService) DeleteDevice(ctx context.Context, deviceID string) error {
	return s.deviceRepo.Delete(ctx, deviceID)
}
