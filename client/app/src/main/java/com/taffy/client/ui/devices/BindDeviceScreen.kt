package com.taffy.client.ui.devices

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.AcUnit
import androidx.compose.material.icons.filled.Lightbulb
import androidx.compose.material.icons.filled.Mic
import androidx.compose.material.icons.filled.Power
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Tune
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Snackbar
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
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
import com.taffy.client.data.BindableDevice

/**
 * UC-03 设备绑定页：列出"待绑定池"设备 → 用户选中 → 输入名称/房间/绑定码 → 提交。
 *
 * 对应后端：
 *   - GET  /api/v1/devices/bindable?reveal=1
 *   - POST /api/v1/devices/bind
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun BindDeviceScreen(
    onBack: () -> Unit,
    onBound: () -> Unit,
) {
    val context = LocalContext.current
    val app = context.applicationContext as TaffyApplication
    val vm: BindDeviceViewModel = viewModel(
        factory = BindDeviceViewModel.Factory(app.apiClient),
    )
    val state by vm.uiState.collectAsStateWithLifecycle()
    val snackbarHostState = remember { SnackbarHostState() }

    LaunchedEffect(state.bindSuccess) {
        if (state.bindSuccess) {
            snackbarHostState.showSnackbar("绑定成功")
            onBound()
        }
    }
    LaunchedEffect(state.errorMessage) {
        val msg = state.errorMessage ?: return@LaunchedEffect
        snackbarHostState.showSnackbar(msg)
        vm.dismissError()
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("添加设备") },
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.primary,
                    titleContentColor = MaterialTheme.colorScheme.onPrimary,
                    navigationIconContentColor = MaterialTheme.colorScheme.onPrimary,
                    actionIconContentColor = MaterialTheme.colorScheme.onPrimary,
                ),
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                },
                actions = {
                    IconButton(onClick = vm::refresh, enabled = !state.refreshing) {
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
                },
            )
        },
        snackbarHost = { SnackbarHost(snackbarHostState) { Snackbar(snackbarData = it) } },
    ) { padding ->
        when {
            state.loading -> Box(
                modifier = Modifier.fillMaxSize().padding(padding),
                contentAlignment = Alignment.Center,
            ) { CircularProgressIndicator() }

            state.bindable.isEmpty() -> EmptyBindableHint(Modifier.padding(padding))

            else -> LazyColumn(
                modifier = Modifier.fillMaxSize().padding(padding),
                contentPadding = PaddingValues(12.dp),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                item {
                    Text(
                        text = "选择一台尚未绑定的设备，输入设备背面的 6 位绑定码完成绑定。",
                        fontSize = 12.sp,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(horizontal = 4.dp, vertical = 4.dp),
                    )
                }
                items(state.bindable, key = { it.deviceId }) { device ->
                    BindableDeviceRow(
                        device = device,
                        onClick = { vm.openBindDialog(device) },
                    )
                }
            }
        }
    }

    // 绑定弹窗
    state.dialogTarget?.let { target ->
        BindDialog(
            target = target,
            submitting = state.submitting,
            onDismiss = vm::closeBindDialog,
            onSubmit = { name, room, code -> vm.submitBind(target.deviceId, name, room, code) },
        )
    }
}

@Composable
private fun BindableDeviceRow(
    device: BindableDevice,
    onClick: () -> Unit,
) {
    Card(
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surface),
        modifier = Modifier.fillMaxWidth().clickable { onClick() },
    ) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 12.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            DeviceTypeAvatar(device.type)
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Text(device.name, fontSize = 16.sp, fontWeight = FontWeight.SemiBold)
                Text(
                    text = "ID: ${device.deviceId}",
                    fontSize = 12.sp,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                if (!device.bindCode.isNullOrBlank()) {
                    Text(
                        text = "绑定码: ${device.bindCode}（演示模式）",
                        fontSize = 12.sp,
                        color = MaterialTheme.colorScheme.primary,
                    )
                }
            }
            Text("绑定 ›", color = MaterialTheme.colorScheme.primary, fontSize = 14.sp)
        }
    }
}

@Composable
private fun DeviceTypeAvatar(type: String) {
    val (icon, tint) = iconForType(type)
    Surface(
        shape = CircleShape,
        color = tint.copy(alpha = 0.15f),
        modifier = Modifier.size(40.dp),
    ) {
        Box(contentAlignment = Alignment.Center) {
            Icon(imageVector = icon, contentDescription = type, tint = tint)
        }
    }
}

@Composable
private fun iconForType(type: String): Pair<ImageVector, Color> = when (type.uppercase()) {
    "LIGHT"   -> Icons.Filled.Lightbulb to Color(0xFFFFC107)
    "AIRCON"  -> Icons.Filled.AcUnit to Color(0xFF4FC3F7)
    "CURTAIN" -> Icons.Filled.Tune to Color(0xFF8D6E63)
    "SOCKET"  -> Icons.Filled.Power to Color(0xFF66BB6A)
    "SPEAKER" -> Icons.Filled.Mic to MaterialTheme.colorScheme.primary
    else      -> Icons.Filled.Power to MaterialTheme.colorScheme.primary
}

@Composable
private fun BindDialog(
    target: BindableDevice,
    submitting: Boolean,
    onDismiss: () -> Unit,
    onSubmit: (name: String, room: String, code: String) -> Unit,
) {
    var name by rememberSaveable(target.deviceId) { mutableStateOf(target.name) }
    var room by rememberSaveable(target.deviceId) { mutableStateOf("") }
    var code by rememberSaveable(target.deviceId) { mutableStateOf(target.bindCode.orEmpty()) }

    AlertDialog(
        onDismissRequest = { if (!submitting) onDismiss() },
        confirmButton = {
            Button(
                onClick = { onSubmit(name.trim(), room.trim(), code.trim()) },
                enabled = !submitting && name.isNotBlank() && code.isNotBlank(),
            ) {
                if (submitting) {
                    CircularProgressIndicator(
                        strokeWidth = 2.dp,
                        color = MaterialTheme.colorScheme.onPrimary,
                        modifier = Modifier.size(16.dp),
                    )
                    Spacer(Modifier.width(8.dp))
                }
                Text("确认绑定")
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss, enabled = !submitting) { Text("取消") }
        },
        title = { Text("绑定 ${target.name}") },
        text = {
            Column {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text("自定义名称") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                Spacer(Modifier.height(8.dp))
                OutlinedTextField(
                    value = room,
                    onValueChange = { room = it },
                    label = { Text("所在房间（可选）") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                Spacer(Modifier.height(8.dp))
                OutlinedTextField(
                    value = code,
                    onValueChange = { code = it.filter { c -> c.isDigit() }.take(16) },
                    label = { Text("绑定码（设备背面 6 位）") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        },
    )
}

@Composable
private fun EmptyBindableHint(modifier: Modifier = Modifier) {
    Box(
        modifier = modifier
            .fillMaxSize()
            .background(MaterialTheme.colorScheme.background),
        contentAlignment = Alignment.Center,
    ) {
        Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text("暂无可绑定设备", fontSize = 16.sp)
            Spacer(Modifier.height(8.dp))
            Text(
                text = "请确认家具端已上电，或执行 003_device_binding.sql 写入演示设备",
                fontSize = 12.sp,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}
