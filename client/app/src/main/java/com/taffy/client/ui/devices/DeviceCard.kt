package com.taffy.client.ui.devices

import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.AcUnit
import androidx.compose.material.icons.filled.Lightbulb
import androidx.compose.material.icons.filled.Mic
import androidx.compose.material.icons.filled.Power
import androidx.compose.material.icons.filled.Tune
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.taffy.client.data.DeviceCard as DeviceCardModel

/**
 * 单设备卡片：图标 + 名称 + 状态描述 + 开关。
 * pending=true 时右侧显示进度圈；离线时背景变灰、开关禁用。
 */
@Composable
fun DeviceCardRow(
    card: DeviceCardModel,
    pending: Boolean,
    onTogglePower: () -> Unit,
) {
    val containerColor =
        if (card.online) MaterialTheme.colorScheme.surface
        else MaterialTheme.colorScheme.surfaceVariant

    Card(
        colors = CardDefaults.cardColors(containerColor = containerColor),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 16.dp, vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            DeviceIcon(card)
            Spacer(Modifier.width(12.dp))

            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(card.device.name, fontSize = 16.sp, fontWeight = FontWeight.SemiBold)
                    Spacer(Modifier.width(8.dp))
                    StatusChip(online = card.online)
                }
                Text(
                    text = describeSubtitle(card),
                    fontSize = 12.sp,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            Box(contentAlignment = Alignment.Center) {
                if (pending) {
                    CircularProgressIndicator(strokeWidth = 2.dp, modifier = Modifier.size(20.dp))
                } else {
                    Switch(
                        checked = card.power,
                        onCheckedChange = { onTogglePower() },
                        enabled = card.online,
                    )
                }
            }
        }
    }
}

@Composable
private fun DeviceIcon(card: DeviceCardModel) {
    val (icon, tint) = iconFor(card.device.type)
    Surface(
        shape = CircleShape,
        color = tint.copy(alpha = 0.15f),
        modifier = Modifier.size(40.dp),
    ) {
        Box(contentAlignment = Alignment.Center) {
            Icon(
                imageVector = icon,
                contentDescription = card.device.type,
                tint = tint,
            )
        }
    }
}

@Composable
private fun StatusChip(online: Boolean) {
    val color = if (online) Color(0xFF43A047) else MaterialTheme.colorScheme.error
    Surface(
        shape = MaterialTheme.shapes.small,
        color = color.copy(alpha = 0.12f),
    ) {
        Text(
            text = if (online) "在线" else "离线",
            color = color,
            fontSize = 11.sp,
            modifier = Modifier.padding(horizontal = 6.dp, vertical = 2.dp),
        )
    }
}

/** 按设备类型选 Material icon + 主题色。*/
@Composable
private fun iconFor(type: String): Pair<ImageVector, Color> = when (type.uppercase()) {
    "LIGHT"   -> Icons.Filled.Lightbulb to Color(0xFFFFC107)
    "AIRCON"  -> Icons.Filled.AcUnit to Color(0xFF4FC3F7)
    "CURTAIN" -> Icons.Filled.Tune to Color(0xFF8D6E63)
    "SOCKET"  -> Icons.Filled.Power to Color(0xFF66BB6A)
    "SPEAKER" -> Icons.Filled.Mic to MaterialTheme.colorScheme.primary
    else      -> Icons.Filled.Power to MaterialTheme.colorScheme.primary
}
