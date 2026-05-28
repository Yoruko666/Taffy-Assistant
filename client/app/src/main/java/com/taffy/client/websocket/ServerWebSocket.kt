package com.taffy.client.websocket

import android.util.Log
import com.taffy.client.BuildConfig
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.util.concurrent.TimeUnit

/**
 * 服务端 WebSocket 连接管理器。
 *
 * 连接到 Go Server 的 /v1/voice 端点，被动接收服务端推送的消息
 * （asr_partial / asr_final / llm_result / llm_error / eos 等）。
 */
class ServerWebSocket(
    private val onMessage: (String) -> Unit,
    private val onConnectionChanged: (ConnectionState) -> Unit,
) {
    companion object {
        private const val TAG = "ServerWS"
        private const val DEVICE_ID = "android_client"
        private const val TOKEN = "t1"
        // 重连间隔
        private const val RECONNECT_DELAY_MS = 3000L
    }

    enum class ConnectionState {
        DISCONNECTED,
        CONNECTING,
        CONNECTED,
    }

    private val client = OkHttpClient.Builder()
        .readTimeout(0, TimeUnit.SECONDS)  // 长连接不超时
        .pingInterval(15, TimeUnit.SECONDS) // 自动心跳
        .build()

    private var webSocket: WebSocket? = null
    private var shouldReconnect = true

    /** 连接地址: ws://host:port/v1/voice?device_id=...&token=... */
    private val serverUrl: String
        get() = "ws://${BuildConfig.SERVER_HOST}:${BuildConfig.SERVER_PORT}" +
                "/v1/voice?device_id=$DEVICE_ID&token=$TOKEN"

    /** 建立连接。*/
    fun connect() {
        if (webSocket != null) return
        shouldReconnect = true
        doConnect()
    }

    private fun doConnect() {
        onConnectionChanged(ConnectionState.CONNECTING)
        val request = Request.Builder()
            .url(serverUrl)
            .build()
        webSocket = client.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                Log.i(TAG, "connected: $serverUrl")
                onConnectionChanged(ConnectionState.CONNECTED)
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                Log.d(TAG, "recv: $text")
                onMessage(text)
            }

            override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                Log.d(TAG, "closing: code=$code reason=$reason")
                webSocket.close(code, reason)
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                Log.i(TAG, "closed: code=$code reason=$reason")
                this@ServerWebSocket.webSocket = null
                onConnectionChanged(ConnectionState.DISCONNECTED)
                scheduleReconnect()
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                Log.w(TAG, "failure: ${t.message}", t)
                this@ServerWebSocket.webSocket = null
                onConnectionChanged(ConnectionState.DISCONNECTED)
                scheduleReconnect()
            }
        })
    }

    /** 断开连接。*/
    fun disconnect() {
        shouldReconnect = false
        webSocket?.close(1000, "client shutdown")
        webSocket = null
        onConnectionChanged(ConnectionState.DISCONNECTED)
    }

    private fun scheduleReconnect() {
        if (!shouldReconnect) return
        Log.i(TAG, "reconnect in ${RECONNECT_DELAY_MS}ms")
        android.os.Handler(android.os.Looper.getMainLooper()).postDelayed({
            if (shouldReconnect) doConnect()
        }, RECONNECT_DELAY_MS)
    }
}
