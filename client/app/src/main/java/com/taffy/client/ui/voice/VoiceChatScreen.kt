package com.taffy.client.ui.voice

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
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
import com.taffy.client.websocket.ServerWebSocket
import kotlinx.coroutines.launch

/**
 * 语音对话页：订阅 server WS 推送的事件流（asr_partial / asr_final /
 * llm_result / device_command_result / tts_audio …），用于联调期肉眼验证全链路。
 *
 * 后续会加入录音按钮 + 文本输入框走 /v1/voice 上行。
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun VoiceChatScreen(onBack: () -> Unit) {
    val scope = rememberCoroutineScope()
    val messages = remember { mutableStateListOf<MessageItem>() }
    var connectionState by remember { mutableStateOf(ServerWebSocket.ConnectionState.DISCONNECTED) }
    val listState = rememberLazyListState()

    val ws = remember {
        ServerWebSocket(
            onMessage = { json ->
                messages.add(MessageItem(json, parseEventType(json)))
                scope.launch { listState.animateScrollToItem(messages.size - 1) }
            },
            onConnectionChanged = { state -> connectionState = state },
        )
    }

    DisposableEffect(Unit) {
        ws.connect()
        onDispose { ws.disconnect() }
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = { Text("小菲对话流") },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(Icons.AutoMirrored.Filled.ArrowBack, contentDescription = "返回")
                    }
                },
                colors = TopAppBarDefaults.topAppBarColors(
                    containerColor = MaterialTheme.colorScheme.primary,
                    titleContentColor = MaterialTheme.colorScheme.onPrimary,
                    navigationIconContentColor = MaterialTheme.colorScheme.onPrimary,
                ),
                actions = { StatusIndicator(state = connectionState) },
            )
        },
    ) { padding ->
        Column(modifier = Modifier.fillMaxSize().padding(padding)) {
            LazyColumn(
                state = listState,
                modifier = Modifier
                    .fillMaxSize()
                    .padding(horizontal = 12.dp),
            ) {
                items(messages) { msg -> MessageBubble(msg) }
            }
            ConnectionBar(state = connectionState)
        }
    }
}

@Composable
private fun StatusIndicator(state: ServerWebSocket.ConnectionState) {
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
                modifier = Modifier.size(16.dp).padding(end = 4.dp),
                strokeWidth = 2.dp,
                color = color,
            )
        }
        Text(text, color = color, fontSize = 12.sp)
    }
}

@Composable
private fun MessageBubble(msg: MessageItem) {
    val bgColor = if (msg.type == "llm_result") {
        MaterialTheme.colorScheme.primaryContainer
    } else {
        MaterialTheme.colorScheme.surfaceVariant
    }
    val label = when (msg.type) {
        "asr_partial" -> "🎤 中间"
        "asr_final" -> "🎤 识别"
        "llm_result" -> "🤖 小菲"
        "llm_error" -> "⚠️ 错误"
        "tts_audio" -> "🔊 语音"
        "device_command" -> "⚙️ 控制"
        "device_command_result" -> "✅ 结果"
        "eos" -> "📄 结束"
        "error" -> "❌ 错误"
        "pong" -> "💓 心跳"
        else -> msg.type
    }
    Text(
        text = "$label  ${msg.rawJson}",
        fontSize = 13.sp,
        fontFamily = FontFamily.Monospace,
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 4.dp)
            .background(bgColor, shape = MaterialTheme.shapes.small)
            .padding(horizontal = 12.dp, vertical = 8.dp),
    )
}

@Composable
private fun ConnectionBar(state: ServerWebSocket.ConnectionState) {
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

private data class MessageItem(val rawJson: String, val type: String)

private fun parseEventType(json: String): String {
    val key = "\"type\":\""
    val idx = json.indexOf(key)
    if (idx < 0) return "unknown"
    val start = idx + key.length
    val end = json.indexOf('"', start)
    return if (end < 0) "unknown" else json.substring(start, end)
}
