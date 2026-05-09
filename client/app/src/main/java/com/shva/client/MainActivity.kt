package com.shva.client

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.shva.client.ui.theme.SHVATheme
import com.shva.client.websocket.ServerWebSocket
import kotlinx.coroutines.launch

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContent {
            SHVATheme {
                MainScreen()
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MainScreen() {
    val scope = rememberCoroutineScope()
    val messages = remember { mutableStateListOf<MessageItem>() }
    var connectionState by remember { mutableStateOf(ServerWebSocket.ConnectionState.DISCONNECTED) }
    val listState = rememberLazyListState()

    // WebSocket 实例（记住生命周期）
    val ws = remember {
        ServerWebSocket(
            onMessage = { json ->
                messages.add(MessageItem(json, parseEventType(json)))
                // 自动滚动到底部
                scope.launch { listState.animateScrollToItem(messages.size - 1) }
            },
            onConnectionChanged = { state -> connectionState = state },
        )
    }

    // 进入时连接，离开时断开
    DisposableEffect(Unit) {
        ws.connect()
        onDispose { ws.disconnect() }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("小菲（SHVA）") },
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.primary,
                    titleContentColor = MaterialTheme.colorScheme.onPrimary,
                ),
                actions = {
                    // 连接状态指示
                    StatusIndicator(state = connectionState)
                },
            )
        },
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                .statusBarsPadding(),
        ) {
            // 消息列表
            LazyColumn(
                state = listState,
                modifier = Modifier
                    .fillMaxSize()
                    .weight(1f)
                    .padding(horizontal = 12.dp),
            ) {
                items(messages) { msg ->
                    MessageBubble(msg)
                }
            }

            // 底部连接栏
            ConnectionBar(state = connectionState)
        }
    }
}

@Composable
fun StatusIndicator(state: ServerWebSocket.ConnectionState) {
    val color = when (state) {
        ServerWebSocket.ConnectionState.CONNECTED -> MaterialTheme.colorScheme.secondary
        ServerWebSocket.ConnectionState.CONNECTING -> MaterialTheme.colorScheme.tertiary
        ServerWebSocket.ConnectionState.DISCONNECTED -> MaterialTheme.colorScheme.error
    }
    val text = when (state) {
        ServerWebSocket.ConnectionState.CONNECTED -> "已连接"
        ServerWebSocket.ConnectionState.CONNECTING -> "连接中"
        ServerWebSocket.ConnectionState.DISCONNECTED -> "未连接"
    }
    Box(
        modifier = Modifier.padding(end = 8.dp),
        contentAlignment = Alignment.Center,
    ) {
        if (state == ServerWebSocket.ConnectionState.CONNECTING) {
            CircularProgressIndicator(
                modifier = Modifier.padding(end = 8.dp),
                strokeWidth = 2.dp,
                color = color,
            )
        }
        Text(
            text = text,
            color = color,
            fontSize = 12.sp,
            modifier = Modifier.padding(end = 8.dp),
        )
    }
}

@Composable
fun MessageBubble(msg: MessageItem) {
    val bgColor = if (msg.type == "llm_result") {
        MaterialTheme.colorScheme.primaryContainer
    } else {
        MaterialTheme.colorScheme.surfaceVariant
    }
    val label = when (msg.type) {
        "asr_final" -> "🎤 识别"
        "llm_result" -> "🤖 小菲"
        "llm_error" -> "⚠️ 错误"
        "eos" -> "📄 结束"
        "error" -> "❌ 错误"
        "pong" -> "💓 心跳"
        else -> msg.type
    }
    Text(
        text = "$label  $msg",
        fontSize = 14.sp,
        fontFamily = FontFamily.Monospace,
        modifier = Modifier
            .fillMaxWidth()
            .background(bgColor, shape = MaterialTheme.shapes.small)
            .padding(horizontal = 12.dp, vertical = 8.dp),
    )
}

@Composable
fun ConnectionBar(state: ServerWebSocket.ConnectionState) {
    val text = when (state) {
        ServerWebSocket.ConnectionState.CONNECTED -> "🟢 已连接到服务器"
        ServerWebSocket.ConnectionState.CONNECTING -> "🟡 正在连接服务器..."
        ServerWebSocket.ConnectionState.DISCONNECTED -> "🔴 未连接（自动重连中）"
    }
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .background(MaterialTheme.colorScheme.surface)
            .padding(8.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(text, fontSize = 12.sp, color = MaterialTheme.colorScheme.onSurface)
    }
}

/** 消息数据类，用于显示。*/
data class MessageItem(val rawJson: String, val type: String) {
    override fun toString(): String = rawJson
}

/** 从 JSON 中快速提取 type 字段。*/
private fun parseEventType(json: String): String {
    // 简单解析，不引入额外 JSON 库
    val key = "\"type\":\""
    val idx = json.indexOf(key)
    if (idx < 0) return "unknown"
    val start = idx + key.length
    val end = json.indexOf('"', start)
    return if (end < 0) "unknown" else json.substring(start, end)
}
