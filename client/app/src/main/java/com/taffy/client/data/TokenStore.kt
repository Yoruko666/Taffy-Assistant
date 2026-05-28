package com.taffy.client.data

import android.content.Context
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map

/**
 * JWT 持久化存储，基于 Jetpack DataStore（Preferences）。
 * 单一来源管理 access_token 与登录用户的手机号。
 */
private val Context.tokenDataStore by preferencesDataStore(name = "taffy_auth")

class TokenStore(private val context: Context) {

    private object Keys {
        val ACCESS_TOKEN: Preferences.Key<String> = stringPreferencesKey("access_token")
        val USER_PHONE: Preferences.Key<String> = stringPreferencesKey("user_phone")
    }

    val tokenFlow: Flow<String?> = context.tokenDataStore.data
        .map { prefs -> prefs[Keys.ACCESS_TOKEN] }

    val phoneFlow: Flow<String?> = context.tokenDataStore.data
        .map { prefs -> prefs[Keys.USER_PHONE] }

    suspend fun save(token: String, phone: String) {
        context.tokenDataStore.edit { prefs ->
            prefs[Keys.ACCESS_TOKEN] = token
            prefs[Keys.USER_PHONE] = phone
        }
    }

    suspend fun clear() {
        context.tokenDataStore.edit { it.clear() }
    }

    /** 读取一次 token。Compose / ViewModel 应使用 [tokenFlow]。*/
    suspend fun tokenOnce(): String? = tokenFlow.first()
}
