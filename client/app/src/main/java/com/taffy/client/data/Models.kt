package com.taffy.client.data

/** 设备元信息（来自 GET /api/v1/devices）。*/
data class Device(
    val deviceId: String,
    val ownerId: Long?,    // 池中设备 owner_id 为 null
    val type: String,     // SPEAKER / LIGHT / AIRCON / CURTAIN / SOCKET
    val name: String,
    val room: String,
    val status: String,   // online / offline / waiting_bind
)

/** 待绑定池中的一台设备（GET /api/v1/devices/bindable）。*/
data class BindableDevice(
    val deviceId: String,
    val type: String,
    val name: String,
    /** 仅当请求带 ?reveal=1 时有值，演示期方便老师评审。*/
    val bindCode: String? = null,
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

/** 设备 + 状态合并视图，UI 直接消费。*/
data class DeviceCard(
    val device: Device,
    val state: DeviceState?,
) {
    val online: Boolean get() = device.status.equals("online", ignoreCase = true)
    val power: Boolean get() = state?.power == true
}

/** 登录 / 注册成功后的 token 信息。*/
data class AuthToken(
    val accessToken: String,
    val tokenType: String,
    val expiresIn: Long,
)

/**
 * 业务结果包装，不向 UI 抛异常。
 *
 * - [Failure.code]     服务端 `error` 字段的业务错误码，供 UI 精确翻译；
 * - [Failure.message]  服务端 `message` 字段或本地兜底文案；
 * - [Failure.httpCode] HTTP 状态码，-1 表示网络 / 解码异常未拿到。
 */
sealed interface ApiResult<out T> {
    data class Success<T>(val value: T) : ApiResult<T>
    data class Failure(
        val message: String,
        val httpCode: Int = -1,
        val code: String? = null,
    ) : ApiResult<Nothing>
}

inline fun <T, R> ApiResult<T>.map(transform: (T) -> R): ApiResult<R> = when (this) {
    is ApiResult.Success -> ApiResult.Success(transform(value))
    is ApiResult.Failure -> this
}
