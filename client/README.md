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
            ├── ShvaApplication.kt           # Application 入口
            ├── MainActivity.kt              # 主界面（消息列表 + 连接状态）
            ├── ui/
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

## 当前能力（M2）

| 功能 | 状态 |
|---|---|
| WebSocket 连接 Go Server | ✅ 被动接收消息 |
| 消息列表展示 | ✅ 按类型区分颜色 |
| 自动重连 | ✅ 断线 3s 后重试 |
| 连接状态指示 | ✅ 顶部状态栏 |
| REST API 调用 | 📅 M3 |
| 设备管理 UI | 📅 M3 |
| 用户登录 | 📅 M3 |

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
| `androidx.compose.ui` | Compose 基础 UI |
| `androidx.activity:activity-compose` | Activity + Compose 集成 |
| `com.squareup.okhttp3:okhttp:4.12.0` | WebSocket 客户端（含自动心跳） |

> 与家具端的语音链路设计见：
> - 家具端协议：[`furniture/README.md`](../furniture/README.md)
> - 服务端 WS 透传：[`server/README.md`](../server/README.md)
> - ASR 模型接口：[`model/README.md`](../model/README.md)
