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
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Chat
import androidx.compose.material.icons.automirrored.filled.Logout
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import com.taffy.client.TaffyApplication
import com.taffy.client.data.DeviceCard
import com.taffy.client.websocket.RealtimeWebSocket

/**
 * 设备列表页：UC-04 主舞台。
 *
 * 顶部 AppBar 含手动刷新与"语音对话"入口；右下角 FAB 跳转 [BindDeviceScreen]（UC-03）；
 * 单卡片**长按**弹出"重命名 / 解绑"菜单。状态副标题 / 单卡片渲染拆到 [DeviceCard.kt] / [DeviceFormat.kt]。
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun DeviceListScreen(
    onLogout: () -> Unit,
    onOpenChat: () -> Unit,
    onAddDevice: () -> Unit,
    onOpenDetail: (deviceId: String) -> Unit,
    refreshTick: Int = 0,
) {
    val context = LocalContext.current
    val app = context.applicationContext as TaffyApplication
    val vm: DeviceListViewModel = viewModel(
        factory = DeviceListViewModel.Factory(app.apiClient, app.tokenStore),
    )
    val state by vm.uiState.collectAsStateWithLifecycle()
    val snackbarHostState = remember { SnackbarHostState() }

    // 由 BindDeviceScreen 返回时触发的列表刷新
    LaunchedEffect(refreshTick) {
        if (refreshTick > 0) vm.refresh(initial = false)
    }

    LaunchedEffect(state.unauthorized) {
        if (state.unauthorized) onLogout()
    }

    LaunchedEffect(state.errorMessage) {
        val msg = state.errorMessage ?: return@LaunchedEffect
        snackbarHostState.showSnackbar(msg)
        vm.dismissError()
    }

    // 长按菜单 / 重命名 / 解绑确认 三个对话框的状态
    var menuTarget by remember { mutableStateOf<DeviceCard?>(null) }
    var renameTarget by remember { mutableStateOf<DeviceCard?>(null) }
    var unbindTarget by remember { mutableStateOf<DeviceCard?>(null) }
    var showLogoutDialog by remember { mutableStateOf(false) }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Text("我的设备")
                        Spacer(Modifier.width(8.dp))
                        RealtimeBadge(state.realtimeConnection)
                    }
                },
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
                    IconButton(onClick = onOpenChat) {
                        Icon(Icons.AutoMirrored.Filled.Chat, contentDescription = "跟小菲聊天")
                    }
                    IconButton(onClick = { showLogoutDialog = true }) {
                        Icon(Icons.AutoMirrored.Filled.Logout, contentDescription = "登出")
                    }
                },
            )
        },
        floatingActionButton = {
            ExtendedFloatingActionButton(
                onClick = onAddDevice,
                icon = { Icon(Icons.Filled.Add, contentDescription = null) },
                text = { Text("添加设备") },
            )
        },
        snackbarHost = { SnackbarHost(snackbarHostState) { Snackbar(snackbarData = it) } },
    ) { padding ->
        when {
            state.loading -> CenterLoading(Modifier.padding(padding))
            state.cards.isEmpty() -> EmptyHint(Modifier.padding(padding))
            else -> LazyColumn(
                modifier = Modifier.fillMaxSize().padding(padding),
                contentPadding = PaddingValues(start = 12.dp, end = 12.dp, top = 12.dp, bottom = 88.dp),
                verticalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                items(state.cards, key = { it.device.deviceId }) { card ->
                    Box {
                        DeviceCardRow(
                            card = card,
                            pending = state.pendingDeviceIds.contains(card.device.deviceId),
                            onTogglePower = { vm.togglePower(card) },
                            onClick = { onOpenDetail(card.device.deviceId) },
                            onLongPress = { menuTarget = card },
                        )
                        // 长按菜单（与卡片 anchor）
                        if (menuTarget?.device?.deviceId == card.device.deviceId) {
                            DropdownMenu(
                                expanded = true,
                                onDismissRequest = { menuTarget = null },
                            ) {
                                DropdownMenuItem(
                                    text = { Text("重命名") },
                                    onClick = {
                                        renameTarget = card
                                        menuTarget = null
                                    },
                                )
                                DropdownMenuItem(
                                    text = { Text("解绑设备", color = MaterialTheme.colorScheme.error) },
                                    onClick = {
                                        unbindTarget = card
                                        menuTarget = null
                                    },
                                )
                            }
                        }
                    }
                }
            }
        }
    }

    // 重命名对话框
    renameTarget?.let { card ->
        RenameDialog(
            card = card,
            submitting = state.pendingDeviceIds.contains(card.device.deviceId),
            onDismiss = { renameTarget = null },
            onSubmit = { newName, newRoom ->
                vm.renameDevice(card.device.deviceId, newName, newRoom)
                renameTarget = null
            },
        )
    }

    // 解绑确认对话框
    unbindTarget?.let { card ->
        AlertDialog(
            onDismissRequest = { unbindTarget = null },
            title = { Text("解绑 ${card.device.name}？") },
            text = { Text("解绑后该设备将从你的账号中移除，需重新输入绑定码才能再次添加。") },
            confirmButton = {
                Button(
                    onClick = {
                        vm.unbindDevice(card.device.deviceId)
                        unbindTarget = null
                    },
                ) { Text("确定解绑") }
            },
            dismissButton = {
                TextButton(onClick = { unbindTarget = null }) { Text("取消") }
            },
        )
    }

    // 登出确认对话框
    if (showLogoutDialog) {
        AlertDialog(
            onDismissRequest = { showLogoutDialog = false },
            title = { Text("退出登录？") },
            text = { Text("退出后需要重新输入手机号 + 密码登录。") },
            confirmButton = {
                Button(onClick = {
                    showLogoutDialog = false
                    vm.logout()
                }) { Text("退出登录") }
            },
            dismissButton = {
                TextButton(onClick = { showLogoutDialog = false }) { Text("取消") }
            },
        )
    }
}

@Composable
private fun RenameDialog(
    card: DeviceCard,
    submitting: Boolean,
    onDismiss: () -> Unit,
    onSubmit: (name: String, room: String) -> Unit,
) {
    var name by rememberSaveable(card.device.deviceId) { mutableStateOf(card.device.name) }
    var room by rememberSaveable(card.device.deviceId) { mutableStateOf(card.device.room) }

    AlertDialog(
        onDismissRequest = { if (!submitting) onDismiss() },
        title = { Text("重命名设备") },
        text = {
            Column {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    label = { Text("名称") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
                Spacer(Modifier.height(8.dp))
                OutlinedTextField(
                    value = room,
                    onValueChange = { room = it },
                    label = { Text("房间（可选）") },
                    singleLine = true,
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        },
        confirmButton = {
            Button(
                onClick = { onSubmit(name.trim(), room.trim()) },
                enabled = !submitting && name.isNotBlank(),
            ) { Text("保存") }
        },
        dismissButton = {
            TextButton(onClick = onDismiss, enabled = !submitting) { Text("取消") }
        },
    )
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
                text = "点击右下角\"添加设备\"绑定一台新设备",
                fontSize = 12.sp,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/** 标题旁的实时连接状态小徽章。绿点=在线，黄点=连接中，红点=离线。*/
@Composable
private fun RealtimeBadge(state: RealtimeWebSocket.ConnectionState) {
    val (label, color) = when (state) {
        RealtimeWebSocket.ConnectionState.CONNECTED -> "实时" to Color(0xFF66BB6A)
        RealtimeWebSocket.ConnectionState.CONNECTING -> "连接中" to Color(0xFFFFB300)
        RealtimeWebSocket.ConnectionState.DISCONNECTED -> "离线" to Color(0xFFEF5350)
    }
    Surface(
        shape = MaterialTheme.shapes.small,
        color = color.copy(alpha = 0.18f),
    ) {
        Text(
            text = "● $label",
            color = color,
            fontSize = 11.sp,
            modifier = Modifier.padding(horizontal = 6.dp, vertical = 2.dp),
        )
    }
}
