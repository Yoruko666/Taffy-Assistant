package com.shva.client.data

/**
 * 设备元信息（来自 GET /api/v1/devices）。
 */
data class Device(
    val deviceId: String,
    val ownerId: Long,
    val type: String,     // SPEAKER / LIGHT / AIRCON / CURTAIN / SOCKET
    val name: String,
    val room: String,
    val status: String,   // online / offline
)

/**
 * 设备状态（来自 GET /api/v1/devices/states）。
 * 各字段按设备类型按需出现：灯有 brightness、空调有 temperature/mode、窗帘有 position。
 */
data class DeviceState(
    val deviceId: String,
    val power: Boolean,
    val brightness: Int? = null,
    val temperature: Int? = null,
    val mode: String? = null,
    val position: Int? = null,
)

/**
 * 设备 + 状态合并视图，UI 直接消费。
 */
data class DeviceCard(
    val device: Device,
    val state: DeviceState?,
) {
    val online: Boolean get() = device.status.equals("online", ignoreCase = true)
    val power: Boolean get() = state?.power == true
}

/**
 * 登录/注册成功后的 token 信息。
 */
data class AuthToken(
    val accessToken: String,
    val tokenType: String,
    val expiresIn: Long,
)

/**
 * 业务侧统一的结果包装：成功 → [Success]；失败 → [Failure]（含人类可读消息）。
 * 不抛异常穿透到 UI，避免崩溃。
 */
sealed interface ApiResult<out T> {
    data class Success<T>(val value: T) : ApiResult<T>
    data class Failure(val message: String, val httpCode: Int = -1) : ApiResult<Nothing>
}

inline fun <T, R> ApiResult<T>.map(transform: (T) -> R): ApiResult<R> = when (this) {
    is ApiResult.Success -> ApiResult.Success(transform(value))
    is ApiResult.Failure -> this
}
