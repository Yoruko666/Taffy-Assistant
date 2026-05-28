# SHVA Android 客户端

> Android 端控制/管理面板，使用 **Kotlin + Jetpack Compose (Material 3)**。

---

## 项目结构

```
client/
├── settings.gradle.kts              # Gradle 项目设置（模块声明）
├── build.gradle.kts                 # 根构建脚本（插件声明）
├── gradle.properties                # Gradle 属性（AndroidX、JVM args）
├── gradle/
│   └── wrapper/
│       └── gradle-wrapper.properties    # Gradle 8.7 发行版
│
├── README.md                        # 本文件
│
└── app/                             # 📱 主应用模块
    ├── build.gradle.kts             # 构建配置（Compose、OkHttp、依赖）
    ├── proguard-rules.pro           # 混淆规则
    │
    └── src/main/
        ├── AndroidManifest.xml      # 清单：INTERNET 权限 + Activity 注册
        ├── res/
        │   └── values/
        │       ├── strings.xml      # 字符串资源
        │       └── themes.xml       # 基础主题（Compose 全权接管 UI）
        │
        └── java/com/shva/client/
            ├── ShvaApplication.kt           # Application 入口（持有 TokenStore + ApiClient 单例）
            ├── MainActivity.kt              # 仅承载 AppNav
            ├── data/
            │   ├── ApiClient.kt             # OkHttp 封装的 REST 客户端（auth + devices）
            │   ├── Models.kt                # Device / DeviceState / DeviceCard / AuthToken / ApiResult
            │   └── TokenStore.kt            # JWT 持久化（Jetpack DataStore）
            ├── ui/
            │   ├── AppNav.kt                # Navigation Compose 顶层路由
            │   ├── login/
            │   │   ├── LoginScreen.kt       # 登录 / 注册（同页切换）
            │   │   └── LoginViewModel.kt
            │   ├── devices/
            │   │   ├── DeviceListScreen.kt  # UC-03 设备列表 + 卡片开关
            │   │   └── DeviceListViewModel.kt
            │   ├── voice/
            │   │   └── VoiceChatScreen.kt   # 语音对话（M3 阶段为 WS 消息流诊断）
            │   └── theme/
            │       └── Theme.kt             # Material 3 亮/暗主题
            └── websocket/
                └── ServerWebSocket.kt       # WebSocket 连接管理（自动重连）
```

## 前置要求

| 工具 | 版本 | 说明 |
|---|---|---|
| Android Studio | Hedgehog (2023.1+) 或更高 | 推荐最新稳定版 |
| JDK | 17 | Android Studio 内置 JDK 即满足 |
| Gradle | 8.7 | 由 wrapper 自动下载，无需预装 |
| Go Server | 运行中 | Android 客户端需连接 Go Server (:8080) |

## 编译 & 运行

### 1. 打开项目

用 Android Studio 打开 `client/` 目录（不是 `client/app/`）：

```bash
# 命令行也可
cd client
code .          # 或直接 Android Studio → Open → 选择 client/
```

首次打开会自动下载 Gradle 8.7 + 依赖（需联网，约 2~5 分钟）。

### 2. 编译 APK（命令行）

```powershell
cd client
./gradlew assembleDebug
```

APK 输出路径：`app/build/outputs/apk/debug/app-debug.apk`

### 3. 安装到设备

**模拟器**（推荐 `Pixel 6 API 33` 或更高）：

```powershell
# Android Studio 中直接点 Run ▶ 按钮
# 或命令行（需先启动模拟器）：
./gradlew installDebug
```

**真机**：开启开发者选项 + USB 调试，连接后：

```powershell
./gradlew installDebug
```

### 4. 运行 Demo

启动后会**自动连接**到 Go Server（默认 `ws://10.0.2.2:8080/v1/voice?device_id=android_client&token=t1`）。

- **模拟器**：`10.0.2.2` 自动映射到宿主机 `localhost`，Go Server 在本机运行即可
- **真机**：需将服务器 IP 改为局域网地址（通过 `BuildConfig.SERVER_HOST` 修改）

连接成功后，顶部状态栏显示 **🟢 已连接**，底部显示收到的所有服务端推送消息（`asr_final`、`llm_result` 等）。

### 服务端地址配置

编辑 `app/build.gradle.kts`：

```kotlin
buildConfigField("String", "SERVER_HOST", "\"10.0.2.2\"")   // 模拟器→宿主机
buildConfigField("int", "SERVER_PORT", "8080")
```

修改后重新编译生效。

## 当前能力（M3）

| 功能 | 状态 |
|---|---|
| 用户注册 / 登录（手机号 + 密码） | ✅ POST /api/v1/auth/{register,login} |
| JWT 持久化（DataStore） | ✅ 启动自动恢复登录态 |
| 设备列表（按用户隔离） | ✅ GET /api/v1/devices + /devices/states |
| 设备开关（卡片即点即生效） | ✅ PUT /api/v1/devices/state（乐观更新+失败回滚） |
| 离线设备保护 | ✅ status=offline 的设备禁止控制 |
| 401 自动登出 | ✅ token 过期回到登录页 |
| 语音对话页（WS 监听） | ✅ 联调期诊断窗口，展示 asr_*/llm_*/tts_audio |
| 录音按钮 / 文本对话上行 | 📅 M4 |
| 设备详情页（亮度/温度滑块） | 📅 M4 |

### 测试账号（来自 `server/migrations/002_seed.sql`）

| 手机号 | 密码 | 设备数 |
|---|---|---|
| `13800000001` | `password123` | 6 |
| `13800000002` | `password123` | 3 |

## 网络架构

```
Android App ──WSS──> Go Server :8080  (WebSocket 实时消息)
            ──HTTPS─> Go Server :8080  (REST API，M3 接入)
```

客户端**只连接 Go Server 一个端点**，所有 AI 能力由 Go Server 内部编排。客户端不录音、不上传音频。

## 依赖

| 库 | 用途 |
|---|---|
| `androidx.compose.material3` | Material 3 UI 组件 |
| `androidx.compose.material:material-icons-extended` | 设备类型图标（灯/空调/窗帘…） |
| `androidx.compose.ui` | Compose 基础 UI |
| `androidx.activity:activity-compose` | Activity + Compose 集成 |
| `androidx.navigation:navigation-compose:2.7.7` | 顶层页面路由 |
| `androidx.datastore:datastore-preferences:1.1.1` | JWT 等少量键值持久化 |
| `org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.0` | IO 调度 + Flow |
| `com.squareup.okhttp3:okhttp:4.12.0` | REST + WebSocket 客户端 |

> 与家具端的语音链路设计见：
> - 家具端协议：[`furniture/README.md`](../furniture/README.md)
> - 服务端 WS 透传：[`server/README.md`](../server/README.md)
> - ASR 模型接口：[`model/README.md`](../model/README.md)
