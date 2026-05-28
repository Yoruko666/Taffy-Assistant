package com.taffy.client

import android.app.Application
import com.taffy.client.data.ApiClient
import com.taffy.client.data.TokenStore

/** 应用入口。集中持有跨页面共享的 TokenStore / ApiClient 单例。*/
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
