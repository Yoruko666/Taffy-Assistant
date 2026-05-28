package com.taffy.client.ui.login

import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.viewModelScope
import com.taffy.client.data.ApiClient
import com.taffy.client.data.ApiResult
import com.taffy.client.data.TokenStore
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/**
 * 登录/注册页 UI 状态。
 *
 * 单一 [LoginUiState] + 几个意图函数，避免散落的 mutableStateOf。
 */
data class LoginUiState(
    val mode: Mode = Mode.LOGIN,
    val phone: String = "",
    val password: String = "",
    val nickname: String = "",
    val email: String = "",
    val loading: Boolean = false,
    val errorMessage: String? = null,
    val loggedIn: Boolean = false,
) {
    enum class Mode { LOGIN, REGISTER }

    val canSubmit: Boolean
        get() = !loading && phone.isNotBlank() && password.length >= 6
}

class LoginViewModel(
    private val apiClient: ApiClient,
    private val tokenStore: TokenStore,
) : ViewModel() {

    private val _uiState = MutableStateFlow(LoginUiState())
    val uiState: StateFlow<LoginUiState> = _uiState.asStateFlow()

    fun toggleMode() {
        _uiState.update {
            it.copy(
                mode = if (it.mode == LoginUiState.Mode.LOGIN) LoginUiState.Mode.REGISTER
                else LoginUiState.Mode.LOGIN,
                errorMessage = null,
            )
        }
    }

    fun onPhoneChange(v: String) = _uiState.update { it.copy(phone = v.trim(), errorMessage = null) }
    fun onPasswordChange(v: String) = _uiState.update { it.copy(password = v, errorMessage = null) }
    fun onNicknameChange(v: String) = _uiState.update { it.copy(nickname = v) }
    fun onEmailChange(v: String) = _uiState.update { it.copy(email = v.trim()) }

    fun submit() {
        val s = _uiState.value
        if (!s.canSubmit) return
        _uiState.update { it.copy(loading = true, errorMessage = null) }

        viewModelScope.launch {
            val result = when (s.mode) {
                LoginUiState.Mode.LOGIN -> apiClient.login(s.phone, s.password)
                LoginUiState.Mode.REGISTER -> apiClient.register(
                    phone = s.phone,
                    password = s.password,
                    nickname = s.nickname.ifBlank { null },
                    email = s.email.ifBlank { null },
                )
            }
            when (result) {
                is ApiResult.Success -> {
                    tokenStore.save(result.value.accessToken, s.phone)
                    _uiState.update { it.copy(loading = false, loggedIn = true) }
                }
                is ApiResult.Failure -> {
                    _uiState.update {
                        it.copy(loading = false, errorMessage = humanize(result.message, result.httpCode))
                    }
                }
            }
        }
    }

    /** 把后端原始错误转成中文。*/
    private fun humanize(raw: String, code: Int): String = when {
        raw.contains("invalid phone or password", ignoreCase = true) -> "手机号或密码错误"
        raw.contains("phone already registered", ignoreCase = true) -> "该手机号已注册"
        raw.contains("email already registered", ignoreCase = true) -> "该邮箱已注册"
        raw.contains("password must be at least", ignoreCase = true) -> "密码至少 6 位"
        raw.contains("phone or email is required", ignoreCase = true) -> "手机号不能为空"
        code in 500..599 -> "服务器内部错误，请稍后重试"
        else -> raw
    }

    class Factory(
        private val apiClient: ApiClient,
        private val tokenStore: TokenStore,
    ) : ViewModelProvider.Factory {
        @Suppress("UNCHECKED_CAST")
        override fun <T : ViewModel> create(modelClass: Class<T>): T =
            LoginViewModel(apiClient, tokenStore) as T
    }
}
