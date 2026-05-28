package com.taffy.client.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.platform.LocalContext
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.taffy.client.TaffyApplication
import com.taffy.client.ui.devices.DeviceListScreen
import com.taffy.client.ui.login.LoginScreen
import com.taffy.client.ui.voice.VoiceChatScreen

/**
 * 顶层导航：根据持久化的 token 决定起点路由。
 *
 * 路由表：
 *   - login   登录 / 注册（未登录默认）
 *   - devices 设备列表（已登录默认，UC-03）
 *   - voice   语音对话（联调期 WS 消息流诊断窗口）
 */
object Routes {
    const val LOGIN = "login"
    const val DEVICES = "devices"
    const val VOICE = "voice"
}

@Composable
fun AppNav() {
    val context = LocalContext.current
    val app = context.applicationContext as TaffyApplication

    // 用 token 是否存在决定 startDestination；只在首次进入时计算一次。
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
        popUpTo(0) { inclusive = true } // 清空回退栈
        launchSingleTop = true
    }
}
