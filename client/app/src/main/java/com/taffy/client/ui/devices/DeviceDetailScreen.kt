package com.taffy.client.ui.devices

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.AcUnit
import androidx.compose.material.icons.filled.Lightbulb
import androidx.compose.material.icons.filled.Mic
import androidx.compose.material.icons.filled.Power
import androidx.compose.material.icons.filled.Tune
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Slider
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
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
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
import com.taffy.client.data.Device
import com.taffy.client.data.DeviceState

/**
 * 设备详情页：UC-04 体验补完，提供"手动调节"通道与语音控制并列的能力。
 *
 * 不同 [Device.type] 渲染不同控件组：
 *   - LIGHT   : 电源 + 亮度滑块（0~100）
 *   - AIRCON  : 电源 + 温度滑块（16~30）+ 模式 chip 五选一
 *   - CURTAIN : 电源 + 开合度滑块（0~100）
 *   - SOCKET  : 仅电源（与卡片一致）
 *   - SPEAKER : 仅显示信息（不可控）
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DeviceDetailScreen(
    deviceId: String,
    onBack: () -> Unit,
    onUnauthorized: () -> Unit,
) {
    val context = LocalContext.current
    val app = context.applicationContext as TaffyApplication
    val vm: DeviceDetailViewModel = viewModel(
        factory = DeviceDetailViewModel.Factory(app.apiClient, deviceId),
    )
    val state by vm.uiState.collectAsStateWithLifecycle()
    val snackbarHostState = remember { SnackbarHostState() }

    LaunchedEffect(state.unauthorized) {
        if (state.unauthorized) onUnauthorized()
    }
    LaunchedEffect(state.errorMessage) {
        val msg = state.errorMessage ?: return@LaunchedEffect
        snackbarHostState.showSnackbar(msg)
        vm.dismissError()
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text(state.device?.name ?: "设备详情") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                },
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.primary,
                    titleContentColor = MaterialTheme.colorScheme.onPrimary,
                    navigationIconContentColor = MaterialTheme.colorScheme.onPrimary,
                    actionIconContentColor = MaterialTheme.colorScheme.onPrimary,
                ),
                actions = {
                    if (state.submitting) {
                        CircularProgressIndicator(
                            strokeWidth = 2.dp,
                            color = MaterialTheme.colorScheme.onPrimary,
                            modifier = Modifier.size(20.dp).padding(end = 12.dp),
                        )
                    }
                },
            )
        },
        snackbarHost = { SnackbarHost(snackbarHostState) { Snackbar(snackbarData = it) } },
    ) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding)) {
            when {
                state.loading -> CircularProgressIndicator(modifier = Modifier.align(Alignment.Center))
                state.device == null -> Text(
                    "找不到该设备",
                    modifier = Modifier.align(Alignment.Center),
                    color = MaterialTheme.colorScheme.error,
                )
                else -> DetailContent(
                    device = state.device!!,
                    deviceState = state.state,
                    onPowerChange = vm::setPower,
                    onBrightnessChange = vm::setBrightness,
                    onTemperatureChange = vm::setTemperature,
                    onModeChange = vm::setMode,
                    onPositionChange = vm::setPosition,
                )
            }
        }
    }
}

@Composable
private fun DetailContent(
    device: Device,
    deviceState: DeviceState?,
    onPowerChange: (Boolean) -> Unit,
    onBrightnessChange: (Int) -> Unit,
    onTemperatureChange: (Int) -> Unit,
    onModeChange: (String) -> Unit,
    onPositionChange: (Int) -> Unit,
) {
    val online = device.status.equals("online", ignoreCase = true)
    val controlsEnabled = online

    Column(
        modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        HeaderCard(device = device, online = online)

        // 电源开关：所有可控类型通用
        if (device.type.uppercase() != "SPEAKER") {
            ControlCard(title = "电源") {
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(
                        text = if (deviceState?.power == true) "已开启" else "已关闭",
                        fontSize = 14.sp,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Switch(
                        checked = deviceState?.power == true,
                        onCheckedChange = onPowerChange,
                        enabled = controlsEnabled,
                    )
                }
            }
        }

        when (device.type.uppercase()) {
            "LIGHT" -> BrightnessCard(
                value = deviceState?.brightness ?: 0,
                enabled = controlsEnabled,
                onChangeFinished = onBrightnessChange,
            )

            "AIRCON" -> {
                TemperatureCard(
                    value = deviceState?.temperature ?: 26,
                    enabled = controlsEnabled,
                    onChangeFinished = onTemperatureChange,
                )
                ModeCard(
                    current = deviceState?.mode,
                    enabled = controlsEnabled,
                    onModeChange = onModeChange,
                )
            }

            "CURTAIN" -> PositionCard(
                value = deviceState?.position ?: 0,
                enabled = controlsEnabled,
                onChangeFinished = onPositionChange,
            )

            // SOCKET / SPEAKER 走电源开关 / 只读，不再渲染额外控件
            else -> Unit
        }

        if (!online) {
            Text(
                text = "设备离线，控制项已禁用。请确认家具端已上电、并检查网络。",
                fontSize = 12.sp,
                color = MaterialTheme.colorScheme.error,
            )
        }
    }
}

@Composable
private fun HeaderCard(device: Device, online: Boolean) {
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface)) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(16.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            DeviceAvatar(device.type)
            Spacer(Modifier.width(14.dp))
            Column(Modifier.weight(1f)) {
                Text(device.name, fontSize = 18.sp, fontWeight = FontWeight.SemiBold)
                Text(
                    text = "${device.room.ifBlank { "未分组" }} · ${device.type}",
                    fontSize = 12.sp,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            StatusChipDetail(online = online)
        }
    }
}

@Composable
private fun ControlCard(title: String, content: @Composable () -> Unit) {
    Card(colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface)) {
        Column(Modifier.padding(16.dp)) {
            Text(title, fontSize = 14.sp, fontWeight = FontWeight.SemiBold)
            Spacer(Modifier.height(8.dp))
            content()
        }
    }
}

@Composable
private fun BrightnessCard(value: Int, enabled: Boolean, onChangeFinished: (Int) -> Unit) {
    SliderControl(
        title = "亮度",
        valueRange = 0f..100f,
        steps = 9,
        initial = value.coerceIn(0, 100),
        unit = "%",
        enabled = enabled,
        onChangeFinished = onChangeFinished,
    )
}

@Composable
private fun TemperatureCard(value: Int, enabled: Boolean, onChangeFinished: (Int) -> Unit) {
    SliderControl(
        title = "温度",
        valueRange = 16f..30f,
        steps = 13, // 16~30 共 15 档，steps = 内部分隔数 = 15 - 2 = 13
        initial = value.coerceIn(16, 30),
        unit = "°C",
        enabled = enabled,
        onChangeFinished = onChangeFinished,
    )
}

@Composable
private fun PositionCard(value: Int, enabled: Boolean, onChangeFinished: (Int) -> Unit) {
    SliderControl(
        title = "开合度",
        valueRange = 0f..100f,
        steps = 9,
        initial = value.coerceIn(0, 100),
        unit = "%",
        enabled = enabled,
        onChangeFinished = onChangeFinished,
    )
}

/**
 * 通用滑块控件。
 * 拖动时只更新本地 [draft]，松手（onValueChangeFinished）才调上层 [onChangeFinished]，
 * 避免高频请求轰炸 server。
 *
 * `key1 = initial` 保证外部值变化（比如语音控制改了温度）能反映到滑块上。
 */
@Composable
private fun SliderControl(
    title: String,
    valueRange: ClosedFloatingPointRange<Float>,
    steps: Int,
    initial: Int,
    unit: String,
    enabled: Boolean,
    onChangeFinished: (Int) -> Unit,
) {
    var draft by remember(initial) { mutableFloatStateOf(initial.toFloat()) }

    ControlCard(title = title) {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text("${valueRange.start.toInt()}$unit", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Text(
                text = "${draft.toInt()}$unit",
                fontSize = 18.sp,
                fontWeight = FontWeight.SemiBold,
                color = MaterialTheme.colorScheme.primary,
            )
            Text("${valueRange.endInclusive.toInt()}$unit", fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Slider(
            value = draft,
            onValueChange = { draft = it },
            valueRange = valueRange,
            steps = steps,
            enabled = enabled,
            onValueChangeFinished = { onChangeFinished(draft.toInt()) },
        )
    }
}

@Composable
private fun ModeCard(current: String?, enabled: Boolean, onModeChange: (String) -> Unit) {
    val modes = listOf(
        "cool" to "制冷",
        "heat" to "制热",
        "auto" to "自动",
        "fan" to "送风",
        "dry" to "除湿",
    )
    ControlCard(title = "模式") {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            modes.forEach { (key, label) ->
                FilterChip(
                    selected = current.equals(key, ignoreCase = true),
                    onClick = { onModeChange(key) },
                    enabled = enabled,
                    label = { Text(label, fontSize = 12.sp) },
                )
            }
        }
    }
}

@Composable
private fun DeviceAvatar(type: String) {
    val (icon, tint) = iconForDetailType(type)
    Surface(
        shape = CircleShape,
        color = tint.copy(alpha = 0.15f),
        modifier = Modifier.size(56.dp),
    ) {
        Box(contentAlignment = Alignment.Center) {
            Icon(imageVector = icon, contentDescription = type, tint = tint)
        }
    }
}

@Composable
private fun StatusChipDetail(online: Boolean) {
    val color = if (online) Color(0xFF43A047) else MaterialTheme.colorScheme.error
    Surface(shape = MaterialTheme.shapes.small, color = color.copy(alpha = 0.12f)) {
        Text(
            text = if (online) "在线" else "离线",
            color = color,
            fontSize = 12.sp,
            modifier = Modifier
                .background(color.copy(alpha = 0.0f))
                .padding(horizontal = 8.dp, vertical = 3.dp),
        )
    }
}

@Composable
private fun iconForDetailType(type: String): Pair<ImageVector, Color> = when (type.uppercase()) {
    "LIGHT" -> Icons.Filled.Lightbulb to Color(0xFFFFC107)
    "AIRCON" -> Icons.Filled.AcUnit to Color(0xFF4FC3F7)
    "CURTAIN" -> Icons.Filled.Tune to Color(0xFF8D6E63)
    "SOCKET" -> Icons.Filled.Power to Color(0xFF66BB6A)
    "SPEAKER" -> Icons.Filled.Mic to MaterialTheme.colorScheme.primary
    else -> Icons.Filled.Power to MaterialTheme.colorScheme.primary
}
