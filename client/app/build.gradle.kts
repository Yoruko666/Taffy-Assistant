plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "com.taffy.client"
    compileSdk = 34

    defaultConfig {
        applicationId = "com.taffy.client"
        minSdk = 26
        targetSdk = 34
        versionCode = 1
        versionName = "1.0.0"

        // 服务端地址（默认 Android 模拟器映射 10.0.2.2 → 宿主机 localhost）
        buildConfigField("String", "SERVER_HOST", "\"10.0.2.2\"")
        buildConfigField("int", "SERVER_PORT", "8080")
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    composeOptions {
        kotlinCompilerExtensionVersion = "1.5.10"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }
}

dependencies {
    // Compose BOM
    val composeBom = platform("androidx.compose:compose-bom:2024.04.00")
    implementation(composeBom)
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-extended")
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.foundation:foundation")
    debugImplementation("androidx.compose.ui:ui-tooling")

    // Activity + Lifecycle
    implementation("androidx.activity:activity-compose:1.9.0")
    implementation("androidx.lifecycle:lifecycle-runtime-ktx:2.7.0")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.7.0")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.8.6")

    // Navigation Compose（页面切换：登录页 → 设备列表）
    implementation("androidx.navigation:navigation-compose:2.7.7")

    // DataStore Preferences（持久化 JWT）
    implementation("androidx.datastore:datastore-preferences:1.1.1")

    // Coroutines（IO 调度 + Flow）
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.0")

    // HTTP / WebSocket（OkHttp 同时承担 REST 和 WS，无需 Retrofit，保持轻量）
    implementation("com.squareup.okhttp3:okhttp:4.12.0")

    // Core
    implementation("androidx.core:core-ktx:1.13.1")
}
