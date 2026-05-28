package service

import (
	"context"
	"fmt"

	"taffy.local/pkg/protocol"

	"taffy-server/internal/model"
)

// DeviceContextItem 推给 Worker 的单台设备快照，本地别名 protocol.DeviceContext。
type DeviceContextItem = protocol.DeviceContext

// BuildDeviceContext 根据家具 device_id 反查所属用户的所有设备 + 状态，
// 用于会话建立时推送给 Worker，注入 LLM system prompt。
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
