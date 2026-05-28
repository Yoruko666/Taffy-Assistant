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
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/**
 * 设备列表页 UI 状态。
 *
 * 设计要点：
 * - 设备 + 状态来自两个独立接口，UI 只关心合并后的 [DeviceCard]，避免在 Compose 里做匹配；
 * - 单设备的开关/亮度等切换走"乐观更新 + 失败回滚"，体感更接近成熟 IoT App。
 */
data class DeviceListUiState(
    val loading: Boolean = false,
    val refreshing: Boolean = false,
    val cards: List<DeviceCard> = emptyList(),
    val errorMessage: String? = null,
    val unauthorized: Boolean = false,
    /** 正在切换中的 device_id 集合，UI 上显示 progress。 */
    val pendingDeviceIds: Set<String> = emptySet(),
)

class DeviceListViewModel(
    private val apiClient: ApiClient,
    private val tokenStore: TokenStore,
) : ViewModel() {

    private val _uiState = MutableStateFlow(DeviceListUiState())
    val uiState: StateFlow<DeviceListUiState> = _uiState.asStateFlow()

    init {
        refresh(initial = true)
    }

    fun refresh(initial: Boolean = false) {
        _uiState.update {
            if (initial) it.copy(loading = true, errorMessage = null)
            else it.copy(refreshing = true, errorMessage = null)
        }
        viewModelScope.launch {
            val devicesRes = apiClient.listDevices()
            val statesRes = apiClient.listDeviceStates()

            // 任意一个 401 表示登录态失效
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

    /** 切换电源（开/关）。乐观更新 + 失败回滚。 */
    fun togglePower(card: DeviceCard) {
        if (!card.online) {
            _uiState.update { it.copy(errorMessage = "${card.device.name} 已离线，无法控制") }
            return
        }
        val target = !card.power
        val deviceId = card.device.deviceId

        // 标记 pending + 乐观更新
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
                // 回滚
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

    private fun optimistic(card: DeviceCard, power: Boolean): DeviceCard {
        val newState = card.state?.copy(power = power)
            ?: DeviceState(deviceId = card.device.deviceId, power = power)
        return card.copy(state = newState)
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
