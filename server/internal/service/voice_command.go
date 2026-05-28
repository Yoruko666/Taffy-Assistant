package service

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"taffy-server/internal/model"
)

// DeviceCommandResult 设备指令的执行结果，由 voice handler 透传回 worker。
type DeviceCommandResult struct {
	Success bool
	// Message 给 LLM 看的中文反馈句，例如 "已打开客厅的吊灯"。
	// 失败场景里也会写明原因，供模型生成回复。
	Message string
}

// controlDeviceParams 是 Worker LLM tool call 下发的 control_device 参数。
// 字段全部对齐 worker 端 tool schema，请勿轻易改动。
type controlDeviceParams struct {
	DeviceID    string `json:"device_id"`
	Action      string `json:"action"`
	Brightness  *int   `json:"brightness,omitempty"`
	Temperature *int   `json:"temperature,omitempty"`
	Mode        string `json:"mode,omitempty"`
	Position    *int   `json:"position,omitempty"`
}

// ExecuteControlDevice 执行 Worker 下发的 control_device 指令：
//  1. 解析参数
//  2. 校验设备存在 + 取当前状态
//  3. 按 action 计算新状态
//  4. UpdateDeviceState（含权限校验）
//  5. 拼装中文反馈
//
// 任何错误都不会向上 panic，统一通过 [DeviceCommandResult] 返回，
// 让 voice handler 不必关心具体业务分支。
func (s *DeviceService) ExecuteControlDevice(ctx context.Context, raw json.RawMessage) DeviceCommandResult {
	var p controlDeviceParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return DeviceCommandResult{Success: false, Message: "参数解析失败: " + err.Error()}
	}
	if p.DeviceID == "" {
		return DeviceCommandResult{Success: false, Message: "缺少 device_id 参数"}
	}

	// 业务上限制单次执行不要超过 5s（DB 慢查询 + 网络抖动余量）
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

	if err := s.UpdateDeviceState(cctx, device.OwnerID, state); err != nil {
		return DeviceCommandResult{Success: false, Message: "状态更新失败: " + err.Error()}
	}

	return DeviceCommandResult{Success: true, Message: formatSuccessMessage(device, p.Action, state)}
}

// ExecuteActivateScene 执行场景激活（M4 待实现）。
func (s *DeviceService) ExecuteActivateScene(_ context.Context, _ json.RawMessage) DeviceCommandResult {
	return DeviceCommandResult{Success: false, Message: "场景激活功能暂未实现"}
}

// applyAction 把 action 反映到 state 上。
// 返回非空字符串表示参数校验失败原因；返回 "" 表示成功。
func applyAction(state *model.DeviceState, p controlDeviceParams) string {
	switch p.Action {
	case "turn_on":
		state.Power = true
	case "turn_off":
		state.Power = false
	case "set_brightness":
		if p.Brightness == nil {
			return "缺少 brightness 参数"
		}
		state.Power = true
		state.Brightness = p.Brightness
	case "set_temperature":
		if p.Temperature == nil {
			return "缺少 temperature 参数"
		}
		state.Power = true
		state.Temperature = p.Temperature
	case "set_mode":
		if p.Mode == "" {
			return "缺少 mode 参数"
		}
		state.Power = true
		m := model.AirconMode(p.Mode)
		state.Mode = &m
	case "set_position":
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
// 例如 "已打开客厅的吊灯"、"卧室的空调温度已设为 26 度"。
func formatSuccessMessage(device *model.Device, action string, state *model.DeviceState) string {
	room := device.Room
	if room != "" {
		room += "的"
	}
	name := device.Name

	switch action {
	case "turn_on":
		return "已打开" + room + name
	case "turn_off":
		return "已关闭" + room + name
	case "set_brightness":
		if state.Brightness != nil {
			return room + name + "亮度已设为" + strconv.Itoa(*state.Brightness) + "%"
		}
		return room + name + "亮度已调整"
	case "set_temperature":
		if state.Temperature != nil {
			return room + name + "温度已设为" + strconv.Itoa(*state.Temperature) + "度"
		}
		return room + name + "温度已调整"
	case "set_mode":
		modeNames := map[string]string{
			"cool": "制冷", "heat": "制热", "auto": "自动",
			"fan": "送风", "dry": "除湿",
		}
		modeText := "未知"
		if state.Mode != nil {
			if n, ok := modeNames[string(*state.Mode)]; ok {
				modeText = n
			}
		}
		return room + name + "已切换为" + modeText + "模式"
	case "set_position":
		if state.Position != nil {
			return room + name + "开合度已设为" + strconv.Itoa(*state.Position) + "%"
		}
		return room + name + "开合度已调整"
	default:
		return room + name + "操作完成"
	}
}
