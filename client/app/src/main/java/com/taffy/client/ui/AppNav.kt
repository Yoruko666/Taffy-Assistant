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
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.taffy.client.TaffyApplication
import com.taffy.client.ui.devices.BindDeviceScreen
import com.taffy.client.ui.devices.DeviceListScreen
import com.taffy.client.ui.login.LoginScreen
import com.taffy.client.ui.voice.VoiceChatScreen

/**
 * 顶层路由。
 *   login    登录 / 注册（未登录默认）
 *   devices  设备列表（已登录默认，UC-04）
 *   bind     设备绑定页（UC-03）
 *   voice    语音对话（联调期 WS 消息流诊断窗口）
 *
 * 起点路由由持久化 token 决定，仅在首次进入时计算。
 */
object Routes {
    const val LOGIN = "login"
    const val DEVICES = "devices"
    const val BIND = "bind"
    const val VOICE = "voice"
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
                onOpenVoice = { nav.navigate(Routes.VOICE) },
                onAddDevice = { nav.navigate(Routes.BIND) },
                refreshTick = devicesRefreshTick,
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
