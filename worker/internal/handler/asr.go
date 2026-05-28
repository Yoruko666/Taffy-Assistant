package handler

import "encoding/json"

// translateASREvent 把 ASR 事件 type 映射为家具协议：partial→asr_partial，final→asr_final。
// 解析失败或非以上两种类型时原样返回。
func translateASREvent(raw []byte) []byte {
	var ev map[string]any
	if err := json.Unmarshal(raw, &ev); err != nil {
		return raw
	}
	t, _ := ev["type"].(string)
	switch t {
	case "partial":
		ev["type"] = "asr_partial"
	case "final":
		ev["type"] = "asr_final"
	default:
		return raw
	}
	out, err := json.Marshal(ev)
	if err != nil {
		return raw
	}
	return out
}
