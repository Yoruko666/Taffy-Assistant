package service

import (
	"context"
	"fmt"

	"taffy.local/pkg/protocol"

	"taffy-server/internal/model"
)

// DeviceContextItem 单台设备的对外快照，
// 用于推送给 Worker 让 LLM 在 system prompt 里"看见"用户的家具。
//
// 是 [protocol.DeviceContext] 的本地别名——真实字段定义在共享 protocol 包中，
// 改动字段必须同时关注 worker 侧。
type DeviceContextItem = protocol.DeviceContext

// BuildDeviceContext 根据"当前在线的家具端 device_id"反查它所属用户的全部设备 +
// 状态，组装为 [DeviceContextItem]。
//
// 流程：
//  1. GetDevice(deviceID)  → 拿到 owner_id
//  2. ListDevicesByOwner(owner_id)
//  3. ListDeviceStatesByOwner(owner_id)
//  4. 用 device_id 做 join，输出统一结构
//
// 任何步骤出错都直接返回 err（含未找到设备）。
//
// 调用方典型场景：voice handler 在 WS 会话建立后立刻同步推送此列表给 worker。
func (s *DeviceService) BuildDeviceContext(ctx context.Context, deviceID string) ([]DeviceContextItem, error) {
	device, err := s.GetDevice(ctx, deviceID)
	if err != nil {
		return nil, fmt.Errorf("get device: %w", err)
	}

	devices, err := s.ListDevicesByOwner(ctx, device.OwnerID)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}

	states, err := s.ListDeviceStatesByOwner(ctx, device.OwnerID)
	if err != nil {
		return nil, fmt.Errorf("list states: %w", err)
	}

	stateMap := make(map[string]*model.DeviceState, len(states))
	for _, st := range states {
		stateMap[st.DeviceID] = st
	}

	out := make([]DeviceContextItem, 0, len(devices))
	for _, d := range devices {
		item := DeviceContextItem{
			DeviceID: d.DeviceID,
			Name:     d.Name,
			Type:     string(d.Type),
			Room:     d.Room,
		}
		if st, ok := stateMap[d.DeviceID]; ok {
			item.Power = st.Power
			item.Brightness = st.Brightness
			item.Temperature = st.Temperature
			if st.Mode != nil {
				item.Mode = string(*st.Mode)
			}
			item.Position = st.Position
		}
		out = append(out, item)
	}
	return out, nil
}
