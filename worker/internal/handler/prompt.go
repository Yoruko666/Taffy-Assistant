package handler

import (
	"strconv"
	"strings"
)

// BuildSystemPrompt 根据设备上下文动态构建 system prompt。
//
// 输出结构：
//
//	{basePrompt}
//	## 当前用户的设备列表
//	## 用户预设场景（可选）
//	## 重要规则（5 条调用规则）
func BuildSystemPrompt(basePrompt string, devices []DeviceContext, scenes []SceneContext) string {
	var b strings.Builder
	if basePrompt == "" {
		basePrompt = "你是「小菲」，一个智能家居语音助手。请用中文简短回答用户的问题。"
	}
	b.WriteString(basePrompt)

	b.WriteString("\n\n## 当前用户的设备列表\n")
	if len(devices) == 0 {
		b.WriteString("（暂无设备）\n")
	} else {
		for _, d := range devices {
			writeDeviceLine(&b, d)
		}
	}

	if len(scenes) > 0 {
		b.WriteString("\n## 用户预设场景\n")
		for _, s := range scenes {
			b.WriteString("- 场景ID:")
			b.WriteString(strconv.FormatInt(s.SceneID, 10))
			b.WriteString(" ")
			b.WriteString(s.Name)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n## 重要规则\n")
	b.WriteString("1. 当用户要求控制设备时，你必须调用 control_device 函数，不要在文本回复中描述操作。\n")
	b.WriteString("2. device_id 必须从上面的设备列表中选择，不能自行编造。\n")
	b.WriteString("3. 调用函数后，等待执行结果再回复用户。如果执行成功，简短确认；如果失败，告知用户。\n")
	b.WriteString("4. 如果用户的意图不是控制设备，直接文字回复即可，不需要调用任何函数。\n")
	b.WriteString("5. 回复要简洁自然，像一个语音助手在说话。\n")

	return b.String()
}

// writeDeviceLine 拼装一行设备描述。
func writeDeviceLine(b *strings.Builder, d DeviceContext) {
	status := "关闭"
	if d.Power {
		status = "开启"
	}
	b.WriteString("- ")
	b.WriteString(d.DeviceID)
	b.WriteString(" | ")
	b.WriteString(d.Room)
	b.WriteString(" ")
	b.WriteString(d.Name)
	b.WriteString(" (")
	b.WriteString(d.Type)
	b.WriteString(") 状态:")
	b.WriteString(status)
	if d.Brightness != nil {
		b.WriteString(" 亮度:")
		b.WriteString(strconv.Itoa(*d.Brightness))
		b.WriteString("%")
	}
	if d.Temperature != nil {
		b.WriteString(" 温度:")
		b.WriteString(strconv.Itoa(*d.Temperature))
		b.WriteString("°C")
	}
	if d.Mode != "" {
		b.WriteString(" 模式:")
		b.WriteString(d.Mode)
	}
	if d.Position != nil {
		b.WriteString(" 开合度:")
		b.WriteString(strconv.Itoa(*d.Position))
		b.WriteString("%")
	}
	b.WriteString("\n")
}
