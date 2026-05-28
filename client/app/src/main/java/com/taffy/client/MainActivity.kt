package com.taffy.client

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import com.taffy.client.ui.AppNav
import com.taffy.client.ui.theme.TaffyTheme

/**
 * 入口 Activity。
 * 唯一职责：启动 Compose + 套主题 + 交给 [AppNav] 决定路由。
 */
class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            TaffyTheme {
                AppNav()
            }
        }
    }
}
