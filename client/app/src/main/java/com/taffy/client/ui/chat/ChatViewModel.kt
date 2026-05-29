package com.taffy.client.ui.chat

import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import com.taffy.client.websocket.ChatEvent
import com.taffy.client.websocket.ChatWebSocket
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import java.util.UUID

/** 一条聊天消息（用户 / 小菲 / 系统提示）。*/
data class ChatMessage(
    val id: String = UUID.randomUUID().toString(),
    val role: Role,
    val content: String,
) {
    enum class Role { USER, ASSISTANT, SYSTEM }
}

data class ChatUiState(
    val connection: ChatWebSocket.ConnectionState = ChatWebSocket.ConnectionState.DISCONNECTED,
    val messages: List<ChatMessage> = emptyList(),
    /** 用户已发送、等待小菲回复的状态——简单 boolean，避免与 connection 混淆。*/
    val waitingReply: Boolean = false,
    val errorMessage: String? = null,
)

/**
 * UC-05 文本对话 ViewModel。维护 WS 连接 + 消息列表 + 等待回复指示。
 *
 * 协议：上行只发 `text_input`，下行关心 asr_final（确认收到）、llm_result（回复）、llm_error（失败）。
 * device_command_result 不直接显示气泡，但会清掉"等待回复"状态以防 LLM 二次调用过程中卡住。
 */
class ChatViewModel : ViewModel() {

    private val _uiState = MutableStateFlow(ChatUiState())
    val uiState: StateFlow<ChatUiState> = _uiState.asStateFlow()

    private val ws = ChatWebSocket(
        onEvent = ::handleEvent,
        onConnectionChanged = { state -> _uiState.update { it.copy(connection = state) } },
    )

    fun connect() = ws.connect()

    fun disconnect() = ws.disconnect()

    fun sendUserMessage(text: String) {
        val trimmed = text.trim()
        if (trimmed.isEmpty()) return
        if (_uiState.value.connection != ChatWebSocket.ConnectionState.CONNECTED) {
            _uiState.update { it.copy(errorMessage = "尚未连接到服务器，请稍候") }
            return
        }
        _uiState.update {
            it.copy(
                messages = it.messages + ChatMessage(role = ChatMessage.Role.USER, content = trimmed),
                waitingReply = true,
                errorMessage = null,
            )
        }
        if (!ws.sendText(trimmed)) {
            _uiState.update { it.copy(waitingReply = false, errorMessage = "消息发送失败，请重试") }
        }
    }

    fun dismissError() {
        _uiState.update { it.copy(errorMessage = null) }
    }

    private fun handleEvent(event: ChatEvent) {
        when (event) {
            is ChatEvent.UserEcho -> {
                // server 已回显 asr_final，证明上行抵达 worker。这里不再显示重复的"用户"气泡，
                // 但若上行成功抵达却没有对应 USER 消息（极端情况），补一条避免空白。
                _uiState.update { state ->
                    val needsAppend = state.messages.lastOrNull()?.let {
                        it.role != ChatMessage.Role.USER || it.content != event.text
                    } ?: true
                    if (needsAppend) {
                        state.copy(
                            messages = state.messages + ChatMessage(
                                role = ChatMessage.Role.USER,
                                content = event.text,
                            ),
                        )
                    } else state
                }
            }

            is ChatEvent.AssistantReply -> _uiState.update {
                it.copy(
                    messages = it.messages + ChatMessage(
                        role = ChatMessage.Role.ASSISTANT,
                        content = event.text,
                    ),
                    waitingReply = false,
                )
            }

            is ChatEvent.Error -> _uiState.update {
                it.copy(
                    messages = it.messages + ChatMessage(
                        role = ChatMessage.Role.SYSTEM,
                        content = "⚠️ ${event.message}",
                    ),
                    waitingReply = false,
                )
            }

            // tool_call 已执行：保留状态，等 llm_result 二轮回复再清 waitingReply。
            is ChatEvent.CommandResult -> {
                if (!event.success) {
                    _uiState.update {
                        it.copy(
                            messages = it.messages + ChatMessage(
                                role = ChatMessage.Role.SYSTEM,
                                content = "❌ 设备控制失败：${event.message}",
                            ),
                        )
                    }
                }
            }
        }
    }

    override fun onCleared() {
        ws.disconnect()
        super.onCleared()
    }

    class Factory : ViewModelProvider.Factory {
        @Suppress("UNCHECKED_CAST")
        override fun <T : ViewModel> create(modelClass: Class<T>): T = ChatViewModel() as T
    }
}
