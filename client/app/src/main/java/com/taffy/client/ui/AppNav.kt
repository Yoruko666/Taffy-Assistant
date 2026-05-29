package com.taffy.client.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import com.taffy.client.TaffyApplication
import com.taffy.client.ui.chat.TextChatScreen
import com.taffy.client.ui.devices.BindDeviceScreen
import com.taffy.client.ui.devices.DeviceDetailScreen
import com.taffy.client.ui.devices.DeviceListScreen
import com.taffy.client.ui.login.LoginScreen
import com.taffy.client.ui.voice.VoiceChatScreen

/**
 * 顶层路由。
 *   login         登录 / 注册（未登录默认）
 *   devices       设备列表（已登录默认，UC-04）
 *   bind          设备绑定页（UC-03）
 *   chat          文本对话页（UC-05）
 *   detail/{id}   设备详情页（亮度/温度/模式/开合度滑块）
 *   voice         语音事件流监视器（联调期诊断窗口，主路径不再链接）
 *
 * 起点路由由持久化 token 决定，仅在首次进入时计算。
 */
object Routes {
    const val LOGIN = "login"
    const val DEVICES = "devices"
    const val BIND = "bind"
    const val CHAT = "chat"
    const val VOICE = "voice"

    /** 详情页路径形如 `detail/light-001`。*/
    const val DETAIL_PATTERN = "detail/{deviceId}"
    fun detailRoute(deviceId: String): String = "detail/$deviceId"
}

@Composable
fun AppNav() {
    val context = LocalContext.current
    val app = context.applicationContext as TaffyApplication

    val token by app.tokenStore.tokenFlow.collectAsState(initial = null)
    var startResolved by remember { mutableStateOf(false) }
    var start by remember { mutableStateOf(Routes.LOGIN) }

    LaunchedEffect(token) {
        if (!startResolved) {
            start = if (token.isNullOrBlank()) Routes.LOGIN else Routes.DEVICES
            startResolved = true
        }
    }

    if (!startResolved) return

    val nav = rememberNavController()
    // 用一个简单的"刷新计数器"在 BindDeviceScreen 绑定成功后通知设备列表 reload。
    var devicesRefreshTick by remember { mutableIntStateOf(0) }

    NavHost(navController = nav, startDestination = start) {
        composable(Routes.LOGIN) {
            LoginScreen(
                onLoggedIn = { nav.toDevices() },
            )
        }
        composable(Routes.DEVICES) {
            DeviceListScreen(
                onLogout = { nav.toLogin() },
                onOpenChat = { nav.navigate(Routes.CHAT) },
                onAddDevice = { nav.navigate(Routes.BIND) },
                onOpenDetail = { id -> nav.navigate(Routes.detailRoute(id)) },
                refreshTick = devicesRefreshTick,
            )
        }
        composable(
            route = Routes.DETAIL_PATTERN,
            arguments = listOf(navArgument("deviceId") { type = NavType.StringType }),
        ) { backStackEntry ->
            val id = backStackEntry.arguments?.getString("deviceId").orEmpty()
            DeviceDetailScreen(
                deviceId = id,
                onBack = {
                    devicesRefreshTick += 1
                    nav.popBackStack()
                },
                onUnauthorized = { nav.toLogin() },
            )
        }
        composable(Routes.BIND) {
            BindDeviceScreen(
                onBack = { nav.popBackStack() },
                onBound = {
                    devicesRefreshTick += 1
                    nav.popBackStack()
                },
            )
        }
        composable(Routes.CHAT) {
            TextChatScreen(onBack = {
                // 聊天可能改变了设备状态，回主列表时刷新一次
                devicesRefreshTick += 1
                nav.popBackStack()
            })
        }
        composable(Routes.VOICE) {
            VoiceChatScreen(onBack = { nav.popBackStack() })
        }
    }
}

private fun NavHostController.toDevices() {
    navigate(Routes.DEVICES) {
        popUpTo(Routes.LOGIN) { inclusive = true }
        launchSingleTop = true
    }
}

private fun NavHostController.toLogin() {
    navigate(Routes.LOGIN) {
        popUpTo(0) { inclusive = true }
        launchSingleTop = true
    }
}
