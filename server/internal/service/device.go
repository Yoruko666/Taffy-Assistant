package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"taffy-server/internal/model"
	"taffy-server/internal/repository"
)

// ErrDeviceNotFound 设备不存在。
var ErrDeviceNotFound = errors.New("device not found")

// ErrNotDeviceOwner 非设备拥有者。
var ErrNotDeviceOwner = errors.New("not device owner")

// ErrInvalidDeviceToken 设备 token 校验失败（不存在 / 不匹配 / 设备未注册）。
var ErrInvalidDeviceToken = errors.New("invalid device token")

// ErrInvalidBindCode 绑定码错误 / 设备已被绑定 / 设备不存在。
// 三种场景统一为同一错误，避免暴露设备是否存在的信息（轻量防枚举）。
var ErrInvalidBindCode = errors.New("invalid bind code or device already bound")

// DeviceService 设备业务逻辑。
type DeviceService struct {
	deviceRepo *repository.DeviceRepo
	stateRepo  *repository.DeviceStateRepo

	// mqttPub 非 nil 时 ExecuteControlDevice 会向 taffy/device/{id}/cmd 下发指令。
	mqttPub MQTTPublisher

	// sceneSvc 非 nil 时 ExecuteActivateScene 可用。
	sceneSvc *SceneService
}

// MQTTPublisher 把控制指令下发到物理 / 模拟设备的最小接口，由 mqtt.Bridge 实现。
type MQTTPublisher interface {
	PublishCommand(ctx context.Context, deviceID string, payload MQTTCommandPayload) error
}

// MQTTCommandPayload 与 mqtt.CommandPayload 字段一致。
// 单独放在 service 包是为了切断对 mqtt 包的反向依赖。
type MQTTCommandPayload struct {
	ToolID string         `json:"tool_id,omitempty"`
	Action string         `json:"action"`
	Params map[string]any `json:"params,omitempty"`
}

// NewDeviceService 创建 DeviceService。
func NewDeviceService(deviceRepo *repository.DeviceRepo, stateRepo *repository.DeviceStateRepo) *DeviceService {
	return &DeviceService{deviceRepo: deviceRepo, stateRepo: stateRepo}
}

// AttachMQTT 注入 mqtt publisher，让 ExecuteControlDevice 同步下发到物理设备。
// AttachSceneService 注入场景服务，启用 ExecuteActivateScene。
func (s *DeviceService) AttachSceneService(svc *SceneService) {
	s.sceneSvc = svc
}

func (s *DeviceService) AttachMQTT(p MQTTPublisher) {
	s.mqttPub = p
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
	if d.OwnerID == nil || *d.OwnerID != userID {
		return ErrNotDeviceOwner
	}
	return s.stateRepo.Upsert(ctx, state)
}

// ListBindable 列出待绑定池中的所有设备（owner_id IS NULL 且 status=waiting_bind）。
// 演示期返回全部池设备；生产期可按"用户附近 / 同局域网"等维度收敛。
func (s *DeviceService) ListBindable(ctx context.Context) ([]*model.Device, error) {
	return s.deviceRepo.ListBindable(ctx)
}

// BindDevice 用绑定码把池设备绑定到指定用户：
//   - device_id + bind_code 必须匹配；
//   - 设备必须仍处于"未归属"状态（owner_id IS NULL）；
//   - name 为空时回退为设备原名；room 允许为空。
//
// 返回更新后的设备实体。
func (s *DeviceService) BindDevice(
	ctx context.Context, userID int64, deviceID, bindCode, name, room string,
) (*model.Device, error) {
	if deviceID == "" || bindCode == "" {
		return nil, ErrInvalidBindCode
	}

	// 查原记录拿默认 name，避免用户没填名字时把设备名改成空串。
	original, err := s.deviceRepo.GetByID(ctx, deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidBindCode
		}
		return nil, fmt.Errorf("get device: %w", err)
	}
	if name == "" {
		name = original.Name
	}

	if err := s.deviceRepo.BindToOwner(ctx, deviceID, bindCode, userID, name, room); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidBindCode
		}
		return nil, fmt.Errorf("bind device: %w", err)
	}

	// 重新查一遍返回最新状态。
	bound, err := s.deviceRepo.GetByID(ctx, deviceID)
	if err != nil {
		return nil, fmt.Errorf("reload device after bind: %w", err)
	}
	return bound, nil
}

// RenameDevice 修改设备的展示名和房间，仅 owner 可改。
func (s *DeviceService) RenameDevice(
	ctx context.Context, userID int64, deviceID, name, room string,
) error {
	if deviceID == "" {
		return ErrDeviceNotFound
	}
	d, err := s.deviceRepo.GetByID(ctx, deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDeviceNotFound
		}
		return fmt.Errorf("get device: %w", err)
	}
	if d.OwnerID == nil || *d.OwnerID != userID {
		return ErrNotDeviceOwner
	}
	if err := s.deviceRepo.Rename(ctx, deviceID, userID, name, room); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotDeviceOwner
		}
		return fmt.Errorf("rename device: %w", err)
	}
	return nil
}

// UnbindDevice 解绑设备 = 物理删除设备记录（级联清除 device_states / commands）。
// 对应用户视角的"移除设备"。仅 owner 可解绑。
func (s *DeviceService) UnbindDevice(ctx context.Context, userID int64, deviceID string) error {
	if deviceID == "" {
		return ErrDeviceNotFound
	}
	d, err := s.deviceRepo.GetByID(ctx, deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDeviceNotFound
		}
		return fmt.Errorf("get device: %w", err)
	}
	if d.OwnerID == nil || *d.OwnerID != userID {
		return ErrNotDeviceOwner
	}
	return s.deviceRepo.Delete(ctx, deviceID)
}

// DeleteDevice 删除设备（级联删状态）。无权限校验，仅供运维 / 测试使用。
func (s *DeviceService) DeleteDevice(ctx context.Context, deviceID string) error {
	return s.deviceRepo.Delete(ctx, deviceID)
}

// ValidateDeviceCredential 校验家具端 (device_id, token) 凭据。
// 设备必须存在、token 完全匹配、status != unregistered。
// 任一不满足返回 ErrInvalidDeviceToken；DB 查询错误原样返回。
func (s *DeviceService) ValidateDeviceCredential(ctx context.Context, deviceID, token string) (*model.Device, error) {
	if deviceID == "" || token == "" {
		return nil, ErrInvalidDeviceToken
	}
	d, err := s.deviceRepo.GetByID(ctx, deviceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidDeviceToken
		}
		return nil, fmt.Errorf("validate device: %w", err)
	}
	if d.Token == "" || d.Token != token {
		return nil, ErrInvalidDeviceToken
	}
	if d.Status == model.StatusUnregistered {
		return nil, ErrInvalidDeviceToken
	}
	return d, nil
}
