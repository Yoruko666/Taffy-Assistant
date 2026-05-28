package com.taffy.client.data

import com.taffy.client.BuildConfig
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONArray
import org.json.JSONException
import org.json.JSONObject
import java.io.IOException
import java.util.concurrent.TimeUnit

/**
 * REST 客户端（仅当前模块使用，刻意不抽象成 Retrofit 以减少依赖）。
 *
 * - 所有调用都在 [Dispatchers.IO] 上挂起；
 * - 业务/网络异常一律收敛到 [ApiResult.Failure]，不向 UI 抛异常；
 * - 已登录请求由 ViewModel 显式传 token，不在本类内部读取，方便测试。
 *
 * 与 server 端的契约见 server/internal/handler/{auth,device}.go。
 */
class ApiClient(private val tokenStore: TokenStore) {

    private val baseUrl: String = "http://${BuildConfig.SERVER_HOST}:${BuildConfig.SERVER_PORT}"

    private val client: OkHttpClient = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(15, TimeUnit.SECONDS)
        .build()

    private val jsonMediaType = "application/json; charset=utf-8".toMediaType()

    // ───────────────────────── Auth ─────────────────────────

    suspend fun login(phone: String, password: String): ApiResult<AuthToken> {
        val body = JSONObject().apply {
            put("phone", phone)
            put("password", password)
        }
        val res = post("/api/v1/auth/login", body, withAuth = false)
        return res.map { json -> json.toAuthToken() }
    }

    suspend fun register(
        phone: String,
        password: String,
        nickname: String?,
        email: String?,
    ): ApiResult<AuthToken> {
        val body = JSONObject().apply {
            put("phone", phone)
            put("password", password)
            if (!nickname.isNullOrBlank()) put("nickname", nickname)
            if (!email.isNullOrBlank()) put("email", email)
        }
        val res = post("/api/v1/auth/register", body, withAuth = false)
        return res.map { json -> json.toAuthToken() }
    }

    // ───────────────────────── Devices ─────────────────────────

    suspend fun listDevices(): ApiResult<List<Device>> {
        val res = get("/api/v1/devices")
        return res.map { json ->
            val arr = json.optJSONArray("devices") ?: JSONArray()
            (0 until arr.length()).map { i -> arr.getJSONObject(i).toDevice() }
        }
    }

    suspend fun listDeviceStates(): ApiResult<List<DeviceState>> {
        val res = get("/api/v1/devices/states")
        return res.map { json ->
            val arr = json.optJSONArray("states") ?: JSONArray()
            (0 until arr.length()).map { i -> arr.getJSONObject(i).toDeviceState() }
        }
    }

    /**
     * 更新设备状态。仅传入需要修改的字段，其他保持后端当前值。
     */
    suspend fun updateDeviceState(
        deviceId: String,
        power: Boolean? = null,
        brightness: Int? = null,
        temperature: Int? = null,
        mode: String? = null,
        position: Int? = null,
    ): ApiResult<Unit> {
        val body = JSONObject().apply {
            put("device_id", deviceId)
            if (power != null) put("power", power)
            if (brightness != null) put("brightness", brightness)
            if (temperature != null) put("temperature", temperature)
            if (mode != null) put("mode", mode)
            if (position != null) put("position", position)
        }
        val res = put("/api/v1/devices/state", body)
        return res.map { }
    }

    // ───────────────────────── HTTP 内部封装 ─────────────────────────

    private suspend fun get(path: String): ApiResult<JSONObject> = withContext(Dispatchers.IO) {
        execute(Request.Builder().url(baseUrl + path).get().withAuth())
    }

    private suspend fun post(
        path: String,
        body: JSONObject,
        withAuth: Boolean = true,
    ): ApiResult<JSONObject> = withContext(Dispatchers.IO) {
        val builder = Request.Builder()
            .url(baseUrl + path)
            .post(body.toString().toRequestBody(jsonMediaType))
        execute(if (withAuth) builder.withAuth() else builder)
    }

    private suspend fun put(
        path: String,
        body: JSONObject,
    ): ApiResult<JSONObject> = withContext(Dispatchers.IO) {
        val builder = Request.Builder()
            .url(baseUrl + path)
            .put(body.toString().toRequestBody(jsonMediaType))
        execute(builder.withAuth())
    }

    private suspend fun Request.Builder.withAuth(): Request.Builder {
        val token = tokenStore.tokenOnce()
        if (!token.isNullOrBlank()) header("Authorization", "Bearer $token")
        return this
    }

    /**
     * 实际执行请求。失败统一返回 [ApiResult.Failure]。
     * - 2xx 期望 body 是 JSON object；空 body 返回 `{}`。
     * - 非 2xx 尝试解析 `{"error":"..."}`，否则用 statusLine。
     */
    private fun execute(builder: Request.Builder): ApiResult<JSONObject> {
        return try {
            client.newCall(builder.build()).execute().use { resp ->
                val raw = resp.body?.string().orEmpty()
                if (resp.isSuccessful) {
                    val obj = if (raw.isBlank()) JSONObject() else runCatching { JSONObject(raw) }
                        .getOrElse { return@use ApiResult.Failure("响应不是合法 JSON: $raw", resp.code) }
                    ApiResult.Success(obj)
                } else {
                    val msg = parseErrorMessage(raw) ?: "HTTP ${resp.code}"
                    ApiResult.Failure(msg, resp.code)
                }
            }
        } catch (e: IOException) {
            ApiResult.Failure("网络异常：${e.message ?: e.javaClass.simpleName}")
        } catch (e: Exception) {
            ApiResult.Failure("未知错误：${e.message ?: e.javaClass.simpleName}")
        }
    }

    private fun parseErrorMessage(raw: String): String? = try {
        if (raw.isBlank()) null else JSONObject(raw).optString("error").takeIf { it.isNotBlank() }
    } catch (_: JSONException) {
        null
    }
}

// ───────────────────────── DTO 解码 ─────────────────────────

private fun JSONObject.toAuthToken(): AuthToken = AuthToken(
    accessToken = optString("access_token"),
    tokenType = optString("token_type", "Bearer"),
    expiresIn = optLong("expires_in", 0L),
)

private fun JSONObject.toDevice(): Device = Device(
    deviceId = optString("device_id"),
    ownerId = optLong("owner_id"),
    type = optString("type"),
    name = optString("name"),
    room = optString("room"),
    status = optString("status"),
)

private fun JSONObject.toDeviceState(): DeviceState = DeviceState(
    deviceId = optString("device_id"),
    power = optBoolean("power"),
    brightness = if (isNull("brightness")) null else optInt("brightness"),
    temperature = if (isNull("temperature")) null else optInt("temperature"),
    mode = if (isNull("mode")) null else optString("mode").takeIf { it.isNotBlank() },
    position = if (isNull("position")) null else optInt("position"),
)
