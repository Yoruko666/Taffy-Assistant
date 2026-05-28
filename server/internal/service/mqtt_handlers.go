package service

import (
	"context"
	"log/slog"
	"time"

	"taffy-server/internal/model"
	"taffy-server/internal/mqtt"
)

// 本文件把 mqtt.Bridge 注入的三个上行回调翻译成 service 层调用：
//
//	OnStatus    → SyncStateFromDevice  更新 device_states
//	OnResult    → 仅记录日志（commands 写入路径在 voice 链路）
//	OnHeartbeat → MarkOnline           刷新 devices.last_seen + status=online
//
// 依赖方向：service 单向依赖 mqtt 包，mqtt 不反向依赖 service。

// NewMQTTStatusHandler 在收到 taffy/device/{id}/status 时把字段同步到 device_states。
func NewMQTTStatusHandler(dev *DeviceService) func(mqtt.StatusPayload) {
	if dev == nil {
		return nil
	}
	return func(p mqtt.StatusPayload) {
		if p.DeviceID == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		if err := dev.SyncStateFromDevice(ctx, p); err != nil {
			slog.Warn("mqtt status sync failed", "device_id", p.DeviceID, "err", err)
		}
	}
}

// NewMQTTResultHandler 暂仅记录设备回执日志，预留与 commands 表的双向闭环。
func NewMQTTResultHandler(_ *ConversationService) func(mqtt.ResultPayload) {
	return func(p mqtt.ResultPayload) {
		slog.Info("mqtt device result",
			"device_id", p.DeviceID,
			"action", p.Action,
			"result", p.Result,
			"tool_id", p.ToolID,
		)
	}
}

// NewMQTTHeartbeatHandler 在心跳消息到达时刷新 devices.last_seen。
func NewMQTTHeartbeatHandler(dev *DeviceService) func(string) {
	if dev == nil {
		return nil
	}
	return func(deviceID string) {
		if deviceID == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := dev.MarkOnline(ctx, deviceID); err != nil {
			slog.Warn("mqtt heartbeat: mark online failed", "device_id", deviceID, "err", err)
		}
	}
}

// SyncStateFromDevice 把设备上行的状态字段合并进 device_states。
// 仅更新非 nil / 非空字段，避免覆盖到其他设备类型的字段。
func (s *DeviceService) SyncStateFromDevice(ctx context.Context, p mqtt.StatusPayload) error {
	current, err := s.GetDeviceState(ctx, p.DeviceID)
	if err != nil {
		return err
	}
	if p.Power != nil {
		current.Power = *p.Power
	}
	if p.Brightness != nil {
		current.Brightness = p.Brightness
	}
	if p.Temperature != nil {
		current.Temperature = p.Temperature
	}
	if p.Mode != "" {
		m := model.AirconMode(p.Mode)
		current.Mode = &m
	}
	if p.Position != nil {
		current.Position = p.Position
	}
	return s.stateRepo.Upsert(ctx, current)
}

// MarkOnline 刷新 devices.last_seen 并把 status 置为 online。
func (s *DeviceService) MarkOnline(ctx context.Context, deviceID string) error {
	return s.deviceRepo.UpdateLastSeen(ctx, deviceID)
}
