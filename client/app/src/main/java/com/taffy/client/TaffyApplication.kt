package com.taffy.client

import android.app.Application
import com.taffy.client.data.ApiClient
import com.taffy.client.data.TokenStore

/**
 * 应用入口。集中持有跨页面共享的单例（TokenStore / ApiClient），
 * 避免 ViewModel 各自构造、状态不一致。
 */
class TaffyApplication : Application() {

    lateinit var tokenStore: TokenStore
        private set

    lateinit var apiClient: ApiClient
        private set

    override fun onCreate() {
        super.onCreate()
        tokenStore = TokenStore(applicationContext)
        apiClient = ApiClient(tokenStore)
    }
}
