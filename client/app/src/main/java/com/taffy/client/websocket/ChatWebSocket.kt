package com.taffy.client.websocket

import android.util.Log
import com.taffy.client.BuildConfig
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONObject
import java.util.concurrent.TimeUnit

/**
 * 文本对话 WebSocket。复用 server `/v1/voice` 通路：
 *   ① 上行帧仅有 {"type":"text_input","text":"..."}（worker 跳过 ASR 直接进 LLM）；
 *   ② 下行帧关注 asr_final（用户消息回显）/ llm_result（小菲回复）/ llm_error / device_command_result。
 *
 * 设计上**不**像 [ServerWebSocket] 那样无限自动重连——文本对话页只在前台时维持连接，
 * 离开页面立即关闭，避免后台空闲长连接。
 *
 * 当前为开发期实现：硬编码 `device_id=dev1&token=t1`（user1 的演示设备），
 * 与 voice 通路的鉴权方式一致；后续 P2 拆出 client 专用 WS 时再换 JWT。
 */
class ChatWebSocket(
    private val onEvent: (event: ChatEvent) -> Unit,
    private val onConnectionChanged: (ConnectionState) -> Unit,
) {
    companion object {
        private const val TAG = "ChatWS"
        private const val DEVICE_ID = "dev1"
        private const val TOKEN = "t1"
    }

    enum class ConnectionState { DISCONNECTED, CONNECTING, CONNECTED }

    private val client = OkHttpClient.Builder()
        .readTimeout(0, TimeUnit.SECONDS)
        .pingInterval(15, TimeUnit.SECONDS)
        .build()

    @Volatile private var ws: WebSocket? = null

    private val serverUrl: String
        get() = "ws://${BuildConfig.SERVER_HOST}:${BuildConfig.SERVER_PORT}" +
                "/v1/voice?device_id=$DEVICE_ID&token=$TOKEN"

    fun connect() {
        if (ws != null) return
        onConnectionChanged(ConnectionState.CONNECTING)
        val request = Request.Builder().url(serverUrl).build()
        ws = client.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                Log.i(TAG, "connected: $serverUrl")
                onConnectionChanged(ConnectionState.CONNECTED)
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                Log.d(TAG, "recv: $text")
                parseAndDispatch(text)
            }

            override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                webSocket.close(code, reason)
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                Log.i(TAG, "closed: code=$code reason=$reason")
                this@ChatWebSocket.ws = null
                onConnectionChanged(ConnectionState.DISCONNECTED)
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                Log.w(TAG, "failure: ${t.message}", t)
                this@ChatWebSocket.ws = null
                onConnectionChanged(ConnectionState.DISCONNECTED)
            }
        })
    }

    /** 发送一句文本指令。返回 true 表示已成功放入发送队列。*/
    fun sendText(text: String): Boolean {
        val sock = ws ?: return false
        val frame = JSONObject().apply {
            put("type", "text_input")
            put("text", text)
        }.toString()
        return sock.send(frame)
    }

    fun disconnect() {
        ws?.close(1000, "client shutdown")
        ws = null
        onConnectionChanged(ConnectionState.DISCONNECTED)
    }

    private fun parseAndDispatch(raw: String) {
        val obj = runCatching { JSONObject(raw) }.getOrNull() ?: return
        val type = obj.optString("type")
        val event = when (type) {
            "asr_final" -> ChatEvent.UserEcho(obj.optString("text"))
            "llm_result" -> {
                val text = obj.optString("text")
                if (text.isNotBlank()) ChatEvent.AssistantReply(text) else null
            }
            "llm_error" -> ChatEvent.Error(obj.optString("message").ifBlank { "LLM 调用失败" })
            "device_command_result" -> ChatEvent.CommandResult(
                success = obj.optBoolean("success"),
                message = obj.optString("message"),
            )
            else -> null
        }
        if (event != null) onEvent(event)
    }
}

/** 聊天页关心的下行事件（其它 type 由 [ChatWebSocket] 静默丢弃）。*/
sealed interface ChatEvent {
    data class UserEcho(val text: String) : ChatEvent
    data class AssistantReply(val text: String) : ChatEvent
    data class CommandResult(val success: Boolean, val message: String) : ChatEvent
    data class Error(val message: String) : ChatEvent
}
