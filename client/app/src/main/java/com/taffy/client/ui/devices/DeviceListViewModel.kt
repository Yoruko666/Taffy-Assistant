package com.taffy.client.ui.devices

import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import com.taffy.client.data.ApiClient
import com.taffy.client.data.ApiResult
import com.taffy.client.data.Device
import com.taffy.client.data.DeviceCard
import com.taffy.client.data.DeviceState
import com.taffy.client.data.TokenStore
import com.taffy.client.websocket.RealtimeEvent
import com.taffy.client.websocket.RealtimeWebSocket
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/**
 * 设备列表页 UI 状态。
 * 设备 + 状态来自两个独立接口，UI 只关心合并后的 [DeviceCard]；
 * 单设备开关走"乐观更新 + 失败回滚"。
 */
data class DeviceListUiState(
    val loading: Boolean = false,
    val refreshing: Boolean = false,
    val cards: List<DeviceCard> = emptyList(),
    val errorMessage: String? = null,
    val unauthorized: Boolean = false,
    /** 正在切换中的 device_id 集合，UI 显示 progress。*/
    val pendingDeviceIds: Set<String> = emptySet(),
    /** UC-10 实时推送链路的连接状态。*/
    val realtimeConnection: RealtimeWebSocket.ConnectionState = RealtimeWebSocket.ConnectionState.DISCONNECTED,
)

class DeviceListViewModel(
    private val apiClient: ApiClient,
    private val tokenStore: TokenStore,
) : ViewModel() {

    private val _uiState = MutableStateFlow(DeviceListUiState())
    val uiState: StateFlow<DeviceListUiState> = _uiState.asStateFlow()

    /** UC-10 实时状态推送：在 ViewModel 生命周期内长连，监听 device_state_changed。*/
    private val realtime = RealtimeWebSocket(
        scope = viewModelScope,
        tokenProvider = { tokenStore.tokenOnce() },
        onEvent = ::onRealtimeEvent,
        onConnectionChanged = { state ->
            _uiState.update { it.copy(realtimeConnection = state) }
        },
    )

    init {
        refresh(initial = true)
        realtime.connect()
    }

    override fun onCleared() {
        realtime.disconnect()
        super.onCleared()
    }

    fun refresh(initial: Boolean = false) {
        _uiState.update {
            if (initial) it.copy(loading = true, errorMessage = null)
            else it.copy(refreshing = true, errorMessage = null)
        }
        viewModelScope.launch {
            val devicesRes = apiClient.listDevices()
            val statesRes = apiClient.listDeviceStates()

            if (isUnauthorized(devicesRes) || isUnauthorized(statesRes)) {
                tokenStore.clear()
                _uiState.update { it.copy(loading = false, refreshing = false, unauthorized = true) }
                return@launch
            }

            val devices = (devicesRes as? ApiResult.Success)?.value ?: emptyList()
            val states = (statesRes as? ApiResult.Success)?.value ?: emptyList()
            val errMsg = listOfNotNull(
                (devicesRes as? ApiResult.Failure)?.message,
                (statesRes as? ApiResult.Failure)?.message,
            ).firstOrNull()

            val cards = mergeCards(devices, states)
            _uiState.update {
                it.copy(
                    loading = false,
                    refreshing = false,
                    cards = cards,
                    errorMessage = errMsg,
                )
            }
        }
    }

    /** 切换电源开关。乐观更新 + 失败回滚。*/
    fun togglePower(card: DeviceCard) {
        if (!card.online) {
            _uiState.update { it.copy(errorMessage = "${card.device.name} 已离线，无法控制") }
            return
        }
        val target = !card.power
        val deviceId = card.device.deviceId

        _uiState.update { s ->
            s.copy(
                pendingDeviceIds = s.pendingDeviceIds + deviceId,
                cards = s.cards.map { if (it.device.deviceId == deviceId) optimistic(it, power = target) else it },
                errorMessage = null,
            )
        }

        viewModelScope.launch {
            val res = apiClient.updateDeviceState(deviceId = deviceId, power = target)
            if (res is ApiResult.Failure) {
                if (res.httpCode == 401) {
                    tokenStore.clear()
                    _uiState.update { it.copy(unauthorized = true, pendingDeviceIds = it.pendingDeviceIds - deviceId) }
                    return@launch
                }
                _uiState.update { s ->
                    s.copy(
                        pendingDeviceIds = s.pendingDeviceIds - deviceId,
                        cards = s.cards.map { if (it.device.deviceId == deviceId) optimistic(it, power = !target) else it },
                        errorMessage = res.message,
                    )
                }
            } else {
                _uiState.update { it.copy(pendingDeviceIds = it.pendingDeviceIds - deviceId) }
            }
        }
    }

    fun dismissError() {
        _uiState.update { it.copy(errorMessage = null) }
    }

    /** 登出：清掉本地 JWT，触发 unauthorized 状态使 UI 跳回登录页。*/
    fun logout() {
        viewModelScope.launch {
            tokenStore.clear()
            _uiState.update { it.copy(unauthorized = true) }
        }
    }

    /** 重命名设备（修改 name / room）。成功后局部刷新该卡片，避免整体 reload 抖动。*/
    fun renameDevice(deviceId: String, name: String, room: String) {
        if (name.isBlank()) {
            _uiState.update { it.copy(errorMessage = "名称不能为空") }
            return
        }
        _uiState.update { s -> s.copy(pendingDeviceIds = s.pendingDeviceIds + deviceId) }
        viewModelScope.launch {
            val res = apiClient.renameDevice(deviceId, name, room)
            when (res) {
                is ApiResult.Success -> _uiState.update { s ->
                    s.copy(
                        pendingDeviceIds = s.pendingDeviceIds - deviceId,
                        cards = s.cards.map { c ->
                            if (c.device.deviceId == deviceId)
                                c.copy(device = c.device.copy(name = name, room = room))
                            else c
                        },
                    )
                }
                is ApiResult.Failure -> _uiState.update { s ->
                    s.copy(
                        pendingDeviceIds = s.pendingDeviceIds - deviceId,
                        errorMessage = res.message,
                        unauthorized = res.httpCode == 401 || s.unauthorized,
                    )
                }
            }
        }
    }

    /** 解绑设备：成功后从列表移除。*/
    fun unbindDevice(deviceId: String) {
        _uiState.update { s -> s.copy(pendingDeviceIds = s.pendingDeviceIds + deviceId) }
        viewModelScope.launch {
            val res = apiClient.unbindDevice(deviceId)
            when (res) {
                is ApiResult.Success -> _uiState.update { s ->
                    s.copy(
                        pendingDeviceIds = s.pendingDeviceIds - deviceId,
                        cards = s.cards.filterNot { it.device.deviceId == deviceId },
                    )
                }
                is ApiResult.Failure -> _uiState.update { s ->
                    s.copy(
                        pendingDeviceIds = s.pendingDeviceIds - deviceId,
                        errorMessage = res.message,
                        unauthorized = res.httpCode == 401 || s.unauthorized,
                    )
                }
            }
        }
    }

    private fun optimistic(card: DeviceCard, power: Boolean): DeviceCard {
        val newState = card.state?.copy(power = power)
            ?: DeviceState(deviceId = card.device.deviceId, power = power)
        return card.copy(state = newState)
    }

    /**
     * 处理服务端推来的实时事件。
     * 当前仅有 device_state_changed —— 把卡片中对应字段就地替换（保留 device 元数据）。
     * 推送以"全状态"形式下发，所以直接覆盖；任何非 null 字段都按"已设值"处理，
     * null 字段保持原值（避免清零本地有意义的数据）。
     */
    private fun onRealtimeEvent(event: RealtimeEvent) {
        when (event) {
            is RealtimeEvent.DeviceStateChanged -> _uiState.update { s ->
                val newCards = s.cards.map { card ->
                    if (card.device.deviceId != event.deviceId) return@map card
                    val base = card.state
                        ?: DeviceState(deviceId = event.deviceId, power = event.power == true)
                    val merged = base.copy(
                        power = event.power ?: base.power,
                        brightness = event.brightness ?: base.brightness,
                        temperature = event.temperature ?: base.temperature,
                        mode = event.mode ?: base.mode,
                        position = event.position ?: base.position,
                    )
                    card.copy(state = merged)
                }
                s.copy(cards = newCards)
            }
        }
    }

    private fun mergeCards(devices: List<Device>, states: List<DeviceState>): List<DeviceCard> {
        val byId = states.associateBy { it.deviceId }
        return devices.map { DeviceCard(device = it, state = byId[it.deviceId]) }
    }

    private fun isUnauthorized(r: ApiResult<*>): Boolean =
        r is ApiResult.Failure && r.httpCode == 401

    class Factory(
        private val apiClient: ApiClient,
        private val tokenStore: TokenStore,
    ) : ViewModelProvider.Factory {
        @Suppress("UNCHECKED_CAST")
        override fun <T : ViewModel> create(modelClass: Class<T>): T =
            DeviceListViewModel(apiClient, tokenStore) as T
    }
}
