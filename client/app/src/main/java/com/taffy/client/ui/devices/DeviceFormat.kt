package com.taffy.client.ui.devices

import com.taffy.client.data.DeviceCard

/** 把状态字段拼成"亮度 80%  26°C 制冷  开合度 70%"这样的副标题。*/
internal fun describeSubtitle(card: DeviceCard): String = buildString {
    append(card.device.room.ifBlank { "未分组" })
    val attr = describeAttributes(card)
    if (attr.isNotBlank()) {
        append(" · ")
        append(attr)
    }
}

private fun describeAttributes(card: DeviceCard): String {
    val s = card.state ?: return ""
    val parts = mutableListOf<String>()
    s.brightness?.let { parts += "亮度 $it%" }
    s.temperature?.let { parts += "${it}°C" }
    s.mode?.let { parts += modeText(it) }
    s.position?.let { parts += "开合度 $it%" }
    return parts.joinToString("  ")
}

/** 空调模式英文 → 中文，与 server protocol.AirconModeLabels() 对齐。*/
private fun modeText(mode: String): String = when (mode.lowercase()) {
    "cool" -> "制冷"
    "heat" -> "制热"
    "auto" -> "自动"
    "fan"  -> "送风"
    "dry"  -> "除湿"
    else   -> mode
}
