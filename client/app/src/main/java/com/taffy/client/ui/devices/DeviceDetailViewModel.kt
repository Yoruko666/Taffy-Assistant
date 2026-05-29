package com.taffy.client.ui.devices

import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import com.taffy.client.data.ApiClient
import com.taffy.client.data.ApiResult
import com.taffy.client.data.Device
import com.taffy.client.data.DeviceState
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** 详情页 UI 状态。*/
data class DeviceDetailUiState(
    val loading: Boolean = true,
    val device: Device? = null,
    val state: DeviceState? = null,
    /** 任一字段提交中：UI 显示进度。*/
    val submitting: Boolean = false,
    val errorMessage: String? = null,
    val unauthorized: Boolean = false,
)

/**
 * 设备详情页 VM。进入时从 GET /api/v1/devices + states 反查目标设备并拉状态。
 *
 * 滑块 / 开关 / 模式按钮统一走 [updateField]：
 *   - 立即乐观更新本地 state，UI 即时反馈；
 *   - 调 PUT /api/v1/devices/state（仅传变化字段）；
 *   - 失败回滚 state 并 surface errorMessage。
 */
class DeviceDetailViewModel(
    private val apiClient: ApiClient,
    private val deviceId: String,
) : ViewModel() {

    private val _uiState = MutableStateFlow(DeviceDetailUiState())
    val uiState: StateFlow<DeviceDetailUiState> = _uiState.asStateFlow()

    init {
        load()
    }

    fun load() {
        _uiState.update { it.copy(loading = true, errorMessage = null) }
        viewModelScope.launch {
            val devicesRes = apiClient.listDevices()
            val statesRes = apiClient.listDeviceStates()

            if ((devicesRes as? ApiResult.Failure)?.httpCode == 401 ||
                (statesRes as? ApiResult.Failure)?.httpCode == 401
            ) {
                _uiState.update { it.copy(loading = false, unauthorized = true) }
                return@launch
            }

            val devices = (devicesRes as? ApiResult.Success)?.value.orEmpty()
            val states = (statesRes as? ApiResult.Success)?.value.orEmpty()
            val device = devices.firstOrNull { it.deviceId == deviceId }
            val state = states.firstOrNull { it.deviceId == deviceId }
            val errMsg = listOfNotNull(
                (devicesRes as? ApiResult.Failure)?.message,
                (statesRes as? ApiResult.Failure)?.message,
            ).firstOrNull()

            _uiState.update {
                it.copy(
                    loading = false,
                    device = device,
                    state = state,
                    errorMessage = if (device == null && errMsg == null) "找不到该设备" else errMsg,
                )
            }
        }
    }

    /** 切换电源。 */
    fun setPower(power: Boolean) = updateField(
        optimistic = { it.copy(power = power) },
        request = { apiClient.updateDeviceState(deviceId = deviceId, power = power) },
    )

    /** 设置亮度（同时确保 power=ON）。*/
    fun setBrightness(value: Int) = updateField(
        optimistic = { it.copy(brightness = value, power = true) },
        request = { apiClient.updateDeviceState(deviceId = deviceId, brightness = value, power = true) },
    )

    /** 设置温度（同时确保 power=ON）。*/
    fun setTemperature(value: Int) = updateField(
        optimistic = { it.copy(temperature = value, power = true) },
        request = { apiClient.updateDeviceState(deviceId = deviceId, temperature = value, power = true) },
    )

    /** 设置空调模式。*/
    fun setMode(mode: String) = updateField(
        optimistic = { it.copy(mode = mode, power = true) },
        request = { apiClient.updateDeviceState(deviceId = deviceId, mode = mode, power = true) },
    )

    /** 设置窗帘开合度。*/
    fun setPosition(value: Int) = updateField(
        optimistic = { it.copy(position = value, power = true) },
        request = { apiClient.updateDeviceState(deviceId = deviceId, position = value, power = true) },
    )

    fun dismissError() {
        _uiState.update { it.copy(errorMessage = null) }
    }

    /**
     * 字段更新统一通道。
     * 用 [optimistic] 立刻在本地 state 上反映新值；调 [request] 失败时回滚为原值。
     */
    private fun updateField(
        optimistic: (DeviceState) -> DeviceState,
        request: suspend () -> ApiResult<Unit>,
    ) {
        val original = _uiState.value.state ?: return
        _uiState.update { it.copy(state = optimistic(original), submitting = true) }
        viewModelScope.launch {
            when (val res = request()) {
                is ApiResult.Success -> _uiState.update { it.copy(submitting = false) }
                is ApiResult.Failure -> _uiState.update {
                    it.copy(
                        state = original,
                        submitting = false,
                        errorMessage = res.message,
                        unauthorized = res.httpCode == 401 || it.unauthorized,
                    )
                }
            }
        }
    }

    class Factory(
        private val apiClient: ApiClient,
        private val deviceId: String,
    ) : ViewModelProvider.Factory {
        @Suppress("UNCHECKED_CAST")
        override fun <T : ViewModel> create(modelClass: Class<T>): T =
            DeviceDetailViewModel(apiClient, deviceId) as T
    }
}
