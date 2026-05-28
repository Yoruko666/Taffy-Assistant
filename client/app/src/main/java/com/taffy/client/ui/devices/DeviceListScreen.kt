package com.taffy.client.ui.devices

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.AcUnit
import androidx.compose.material.icons.filled.Lightbulb
import androidx.compose.material.icons.filled.Mic
import androidx.compose.material.icons.filled.Power
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Tune
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Snackbar
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.taffy.client.TaffyApplication
import com.taffy.client.data.DeviceCard

/**
 * 设备列表页：UC-03 主舞台。
 *
 * 顶部 AppBar 含手动刷新；中部 LazyColumn 渲染卡片，每张卡片可直接开关；
 * 底部预留"语音对话"入口。
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DeviceListScreen(
    onLogout: () -> Unit,
    onOpenVoice: () -> Unit,
) {
    val context = LocalContext.current
    val app = context.applicationContext as TaffyApplication
    val vm: DeviceListViewModel = viewModel(
        factory = DeviceListViewModel.Factory(app.apiClient, app.tokenStore),
    )
    val state by vm.uiState.collectAsStateWithLifecycle()
    val snackbarHostState = remember { SnackbarHostState() }

    // 401 → 自动跳回登录页
    LaunchedEffect(state.unauthorized) {
        if (state.unauthorized) onLogout()
    }

    // 错误消息推到 Snackbar
    LaunchedEffect(state.errorMessage) {
        val msg = state.errorMessage ?: return@LaunchedEffect
        snackbarHostState.showSnackbar(msg)
        vm.dismissError()
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("我的设备") },
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.primary,
                    titleContentColor = MaterialTheme.colorScheme.onPrimary,
                    actionIconContentColor = MaterialTheme.colorScheme.onPrimary,
                ),
                actions = {
                    IconButton(onClick = { vm.refresh(initial = false) }, enabled = !state.refreshing) {
                        if (state.refreshing) {
                            CircularProgressIndicator(
                                strokeWidth = 2.dp,
                                color = MaterialTheme.colorScheme.onPrimary,
                                modifier = Modifier.size(20.dp),
                            )
                        } else {
                            Icon(Icons.Filled.Refresh, contentDescription = "刷新")
                        }
                    }
                    IconButton(onClick = onOpenVoice) {
                        Icon(Icons.Filled.Mic, contentDescription = "语音对话")
                    }
                },
            )
        },
        snackbarHost = { SnackbarHost(snackbarHostState) { Snackbar(snackbarData = it) } },
    ) { padding ->
        when {
            state.loading -> CenterLoading(Modifier.padding(padding))
            state.cards.isEmpty() -> EmptyHint(Modifier.padding(padding))
            else -> LazyColumn(
                modifier = Modifier.fillMaxSize().padding(padding),
                contentPadding = PaddingValues(12.dp),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                items(state.cards, key = { it.device.deviceId }) { card ->
                    DeviceRow(
                        card = card,
                        pending = state.pendingDeviceIds.contains(card.device.deviceId),
                        onTogglePower = { vm.togglePower(card) },
                    )
                }
            }
        }
    }
}

@Composable
private fun DeviceRow(
    card: DeviceCard,
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
                val sub = buildString {
                    append(card.device.room.ifBlank { "未分组" })
                    val attr = describeAttributes(card)
                    if (attr.isNotBlank()) {
                        append(" · ")
                        append(attr)
                    }
                }
                Text(
                    text = sub,
                    fontSize = 12.sp,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            // 右侧：进度 / 开关
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
private fun DeviceIcon(card: DeviceCard) {
    val (icon, tint) = when (card.device.type.uppercase()) {
        "LIGHT" -> Icons.Filled.Lightbulb to Color(0xFFFFC107)
        "AIRCON" -> Icons.Filled.AcUnit to Color(0xFF4FC3F7)
        "CURTAIN" -> Icons.Filled.Tune to Color(0xFF8D6E63)
        "SOCKET" -> Icons.Filled.Power to Color(0xFF66BB6A)
        "SPEAKER" -> Icons.Filled.Mic to MaterialTheme.colorScheme.primary
        else -> (Icons.Filled.Power as ImageVector) to MaterialTheme.colorScheme.primary
    }
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

@Composable
private fun CenterLoading(modifier: Modifier = Modifier) {
    Box(modifier = modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
        CircularProgressIndicator()
    }
}

@Composable
private fun EmptyHint(modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background),
        contentAlignment = Alignment.Center,
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text("还没有设备", fontSize = 16.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.height(8.dp))
            Text(
                text = "请联系管理员绑定设备，或检查 server 端 002_seed.sql 是否已执行",
                fontSize = 12.sp,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/** 把状态字段转成"亮度80% / 26°C 制冷 / 开合度70%"这样的描述。*/
private fun describeAttributes(card: DeviceCard): String {
    val s = card.state ?: return ""
    val parts = mutableListOf<String>()
    s.brightness?.let { parts += "亮度 $it%" }
    s.temperature?.let { parts += "${it}°C" }
    s.mode?.let { parts += modeText(it) }
    s.position?.let { parts += "开合度 $it%" }
    return parts.joinToString("  ")
}

private fun modeText(mode: String): String = when (mode.lowercase()) {
    "cool" -> "制冷"
    "heat" -> "制热"
    "auto" -> "自动"
    "fan" -> "送风"
    "dry" -> "除湿"
    else -> mode
}
