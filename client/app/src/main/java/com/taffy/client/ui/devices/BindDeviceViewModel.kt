package com.taffy.client.ui.devices

import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import com.taffy.client.data.ApiClient
import com.taffy.client.data.ApiResult
import com.taffy.client.data.BindableDevice
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** UC-03 绑定页 UI 状态。*/
data class BindDeviceUiState(
    val loading: Boolean = false,
    val refreshing: Boolean = false,
    val submitting: Boolean = false,
    val bindable: List<BindableDevice> = emptyList(),
    /** 非空表示弹出绑定对话框。*/
    val dialogTarget: BindableDevice? = null,
    val errorMessage: String? = null,
    /** 一次性事件：true 时由 UI 触发返回 + 刷新主列表。*/
    val bindSuccess: Boolean = false,
)

class BindDeviceViewModel(
    private val apiClient: ApiClient,
) : ViewModel() {

    private val _uiState = MutableStateFlow(BindDeviceUiState())
    val uiState: StateFlow<BindDeviceUiState> = _uiState.asStateFlow()

    init {
        loadBindable(initial = true)
    }

    fun refresh() = loadBindable(initial = false)

    private fun loadBindable(initial: Boolean) {
        _uiState.update {
            if (initial) it.copy(loading = true, errorMessage = null)
            else it.copy(refreshing = true, errorMessage = null)
        }
        viewModelScope.launch {
            when (val res = apiClient.listBindableDevices(revealCode = true)) {
                is ApiResult.Success -> _uiState.update {
                    it.copy(loading = false, refreshing = false, bindable = res.value)
                }
                is ApiResult.Failure -> _uiState.update {
                    it.copy(
                        loading = false,
                        refreshing = false,
                        errorMessage = res.message,
                    )
                }
            }
        }
    }

    fun openBindDialog(target: BindableDevice) {
        _uiState.update { it.copy(dialogTarget = target) }
    }

    fun closeBindDialog() {
        _uiState.update { it.copy(dialogTarget = null) }
    }

    fun submitBind(deviceId: String, name: String, room: String, bindCode: String) {
        if (deviceId.isBlank() || bindCode.isBlank() || name.isBlank()) return
        _uiState.update { it.copy(submitting = true) }
        viewModelScope.launch {
            val res = apiClient.bindDevice(
                deviceId = deviceId,
                bindCode = bindCode,
                name = name,
                room = room,
            )
            when (res) {
                is ApiResult.Success -> _uiState.update {
                    it.copy(
                        submitting = false,
                        dialogTarget = null,
                        bindSuccess = true,
                    )
                }
                is ApiResult.Failure -> _uiState.update {
                    it.copy(
                        submitting = false,
                        errorMessage = humanize(res),
                    )
                }
            }
        }
    }

    fun dismissError() {
        _uiState.update { it.copy(errorMessage = null) }
    }

    /** 服务端 error code → 中文。*/
    private fun humanize(failure: ApiResult.Failure): String = when (failure.code) {
        "invalid_bind_code" -> "绑定码错误，或该设备已被绑定"
        "device_id_required", "bind_code_required" -> "请填写完整的设备 ID 与绑定码"
        "unauthorized" -> "登录态已过期，请重新登录"
        else -> failure.message
    }

    class Factory(private val apiClient: ApiClient) : ViewModelProvider.Factory {
        @Suppress("UNCHECKED_CAST")
        override fun <T : ViewModel> create(modelClass: Class<T>): T =
            BindDeviceViewModel(apiClient) as T
    }
}
