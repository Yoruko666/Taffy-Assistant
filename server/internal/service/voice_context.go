package service

import (
	"context"
	"fmt"

	"taffy.local/pkg/protocol"

	"taffy-server/internal/model"
)

// DeviceContextItem 单台设备的对外快照，推给 Worker 让 LLM 在 system prompt 里"看见"用户的家具。
// 字段定义在共享的 [protocol.DeviceContext]，本地仅别名。
type DeviceContextItem = protocol.DeviceContext

// BuildDeviceContext 根据"当前在线的家具 device_id"反查其所属用户的所有设备 + 状态，
// 组装为 [DeviceContextItem] 列表，用于会话建立时推送给 Worker。
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
