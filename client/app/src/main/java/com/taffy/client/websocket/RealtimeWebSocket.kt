package com.taffy.client.websocket

import android.util.Log
import com.taffy.client.BuildConfig
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.json.JSONObject
import java.util.concurrent.TimeUnit

/**
 * UC-10 实时状态推送 WebSocket：连接 server `/ws?token=<jwt>`，
 * 接收 `device_state_changed` 事件并通过回调向上层透传。
 *
 * 与 [ChatWebSocket] / [ServerWebSocket] 的区别：
 *   ① 鉴权使用 **JWT**（query string 携带），区别于设备 token；
 *   ② 仅下行——服务端推送，客户端不发任何业务帧；
 *   ③ 设备列表页前台时长连，离开页面立即断开（避免后台耗电）；
 *   ④ 失败 3s 后自动重连，但 401（鉴权失败）不重连。
 *
 * @param scope 用于内部协程（重连定时器 + 拉新 token）。建议传 ViewModel 的 viewModelScope。
 */
class RealtimeWebSocket(
    private val scope: CoroutineScope,
    private val tokenProvider: suspend () -> String?,
    private val onEvent: (RealtimeEvent) -> Unit,
    private val onConnectionChanged: (ConnectionState) -> Unit = {},
) {
    companion object {
        private const val TAG = "RealtimeWS"
        private const val RECONNECT_DELAY_MS = 3000L
    }

    enum class ConnectionState { DISCONNECTED, CONNECTING, CONNECTED }

    private val client = OkHttpClient.Builder()
        .readTimeout(0, TimeUnit.SECONDS)
        .pingInterval(15, TimeUnit.SECONDS)
        .build()

    @Volatile private var ws: WebSocket? = null
    @Volatile private var shouldReconnect = false

    /** 启动连接：内部异步取 token，连接建立失败也会自动重连。*/
    fun connect() {
        if (ws != null) return
        shouldReconnect = true
        scope.launch {
            val token = tokenProvider()
            if (token.isNullOrBlank()) {
                Log.w(TAG, "no token, skip connect")
                onConnectionChanged(ConnectionState.DISCONNECTED)
                return@launch
            }
            doConnect(token)
        }
    }

    private fun doConnect(token: String) {
        onConnectionChanged(ConnectionState.CONNECTING)
        val url = "ws://${BuildConfig.SERVER_HOST}:${BuildConfig.SERVER_PORT}/ws?token=$token"
        val request = Request.Builder().url(url).build()
        ws = client.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                Log.i(TAG, "connected")
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
                this@RealtimeWebSocket.ws = null
                onConnectionChanged(ConnectionState.DISCONNECTED)
                scheduleReconnect()
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                val httpCode = response?.code
                Log.w(TAG, "failure: code=$httpCode err=${t.message}", t)
                this@RealtimeWebSocket.ws = null
                onConnectionChanged(ConnectionState.DISCONNECTED)
                // 401 鉴权失败时不重连，避免无限刷
                if (httpCode != 401) scheduleReconnect()
            }
        })
    }

    fun disconnect() {
        shouldReconnect = false
        ws?.close(1000, "client shutdown")
        ws = null
        onConnectionChanged(ConnectionState.DISCONNECTED)
    }

    private fun scheduleReconnect() {
        if (!shouldReconnect) return
        scope.launch {
            delay(RECONNECT_DELAY_MS)
            if (!shouldReconnect) return@launch
            val token = tokenProvider()
            if (!token.isNullOrBlank()) doConnect(token)
        }
    }

    private fun parseAndDispatch(raw: String) {
        val obj = runCatching { JSONObject(raw) }.getOrNull() ?: return
        when (obj.optString("type")) {
            "device_state_changed" -> {
                val deviceId = obj.optString("device_id").takeIf { it.isNotBlank() } ?: return
                val stateObj = obj.optJSONObject("state")
                onEvent(
                    RealtimeEvent.DeviceStateChanged(
                        deviceId = deviceId,
                        power = stateObj?.optBoolean("power"),
                        brightness = stateObj?.takeIf { !it.isNull("brightness") }?.optInt("brightness"),
                        temperature = stateObj?.takeIf { !it.isNull("temperature") }?.optInt("temperature"),
                        mode = stateObj?.takeIf { !it.isNull("mode") }?.optString("mode")?.takeIf { it.isNotBlank() },
                        position = stateObj?.takeIf { !it.isNull("position") }?.optInt("position"),
                    )
                )
            }
        }
    }
}

/** 服务端实时推送给客户端的事件。*/
sealed interface RealtimeEvent {
    data class DeviceStateChanged(
        val deviceId: String,
        val power: Boolean?,
        val brightness: Int?,
        val temperature: Int?,
        val mode: String?,
        val position: Int?,
    ) : RealtimeEvent
}
