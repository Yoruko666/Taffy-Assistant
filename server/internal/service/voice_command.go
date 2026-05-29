package service

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"taffy.local/pkg/protocol"

	"taffy-server/internal/model"
)

// DeviceCommandResult 设备指令的执行结果。
// Message 是给 LLM 的中文反馈，用于二轮对话生成自然语言回复。
type DeviceCommandResult struct {
	Success bool
	Message string
}

// ExecuteControlDevice 执行 control_device 指令：
// 解析参数 → 校验设备 → 计算新状态 → UpdateDeviceState → MQTT 下发 → 拼装中文反馈。
//
// 任何错误都通过 DeviceCommandResult 返回，不会向上 panic。
// 若已通过 AttachMQTT 注入 publisher，会在 DB 更新成功后向 taffy/device/{id}/cmd
// 发布控制 JSON，MQTT 失败不影响主返回。
func (s *DeviceService) ExecuteControlDevice(ctx context.Context, raw json.RawMessage) DeviceCommandResult {
	var p protocol.ControlDeviceParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return DeviceCommandResult{Success: false, Message: "参数解析失败: " + err.Error()}
	}
	if p.DeviceID == "" {
		return DeviceCommandResult{Success: false, Message: "缺少 device_id 参数"}
	}

	// 单次执行硬上限 5s，避免 LLM 等待过久。
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	device, err := s.GetDevice(cctx, p.DeviceID)
	if err != nil {
		return DeviceCommandResult{Success: false, Message: "设备不存在: " + p.DeviceID}
	}
	state, err := s.GetDeviceState(cctx, p.DeviceID)
	if err != nil {
		return DeviceCommandResult{Success: false, Message: "获取设备状态失败: " + err.Error()}
	}

	if errMsg := applyAction(state, p); errMsg != "" {
		return DeviceCommandResult{Success: false, Message: errMsg}
	}

	if device.OwnerID == nil {
		return DeviceCommandResult{Success: false, Message: "该设备尚未绑定到任何用户，请先在 App 中绑定"}
	}
	if err := s.UpdateDeviceState(cctx, *device.OwnerID, state); err != nil {
		return DeviceCommandResult{Success: false, Message: "状态更新失败: " + err.Error()}
	}

	s.publishCommandBestEffort(cctx, p)

	return DeviceCommandResult{Success: true, Message: formatSuccessMessage(device, p.Action, state)}
}

// publishCommandBestEffort 把控制指令通过 MQTT 转发到物理设备。
// 仅在 publisher 已注入时执行，错误被吞掉以免影响 LLM 二轮调用。
func (s *DeviceService) publishCommandBestEffort(ctx context.Context, p protocol.ControlDeviceParams) {
	if s.mqttPub == nil {
		return
	}
	params := map[string]any{}
	if p.Brightness != nil {
		params["brightness"] = *p.Brightness
	}
	if p.Temperature != nil {
		params["temperature"] = *p.Temperature
	}
	if p.Mode != "" {
		params["mode"] = p.Mode
	}
	if p.Position != nil {
		params["position"] = *p.Position
	}
	_ = s.mqttPub.PublishCommand(ctx, p.DeviceID, MQTTCommandPayload{
		Action: p.Action,
		Params: params,
	})
}

// ExecuteActivateScene 执行场景激活（暂未实现）。
func (s *DeviceService) ExecuteActivateScene(_ context.Context, _ json.RawMessage) DeviceCommandResult {
	return DeviceCommandResult{Success: false, Message: "场景激活功能暂未实现"}
}

// applyAction 把 action 反映到 state 上。
// 返回非空字符串表示参数校验失败，"" 表示成功。
func applyAction(state *model.DeviceState, p protocol.ControlDeviceParams) string {
	switch p.Action {
	case protocol.ActionTurnOn:
		state.Power = true
	case protocol.ActionTurnOff:
		state.Power = false
	case protocol.ActionSetBrightness:
		if p.Brightness == nil {
			return "缺少 brightness 参数"
		}
		state.Power = true
		state.Brightness = p.Brightness
	case protocol.ActionSetTemperature:
		if p.Temperature == nil {
			return "缺少 temperature 参数"
		}
		state.Power = true
		state.Temperature = p.Temperature
	case protocol.ActionSetMode:
		if p.Mode == "" {
			return "缺少 mode 参数"
		}
		state.Power = true
		m := model.AirconMode(p.Mode)
		state.Mode = &m
	case protocol.ActionSetPosition:
		if p.Position == nil {
			return "缺少 position 参数"
		}
		state.Power = true
		state.Position = p.Position
	default:
		return "未知的 action: " + p.Action
	}
	return ""
}

// formatSuccessMessage 拼装 LLM 用的中文反馈句。
func formatSuccessMessage(device *model.Device, action string, state *model.DeviceState) string {
	room := device.Room
	if room != "" {
		room += "的"
	}
	name := device.Name

	switch action {
	case protocol.ActionTurnOn:
		return "已打开" + room + name
	case protocol.ActionTurnOff:
		return "已关闭" + room + name
	case protocol.ActionSetBrightness:
		if state.Brightness != nil {
			return room + name + "亮度已设为" + strconv.Itoa(*state.Brightness) + "%"
		}
		return room + name + "亮度已调整"
	case protocol.ActionSetTemperature:
		if state.Temperature != nil {
			return room + name + "温度已设为" + strconv.Itoa(*state.Temperature) + "度"
		}
		return room + name + "温度已调整"
	case protocol.ActionSetMode:
		modeText := "未知"
		if state.Mode != nil {
			if n, ok := protocol.AirconModeLabels()[string(*state.Mode)]; ok {
				modeText = n
			}
		}
		return room + name + "已切换为" + modeText + "模式"
	case protocol.ActionSetPosition:
		if state.Position != nil {
			return room + name + "开合度已设为" + strconv.Itoa(*state.Position) + "%"
		}
		return room + name + "开合度已调整"
	default:
		return room + name + "操作完成"
	}
}
