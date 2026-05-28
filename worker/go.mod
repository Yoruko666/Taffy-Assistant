module taffy-worker

go 1.22

require (
	github.com/gorilla/websocket v1.5.3
	gopkg.in/yaml.v3 v3.0.1
	taffy.local/pkg/httpx v0.0.0-00010101000000-000000000000
	taffy.local/pkg/protocol v0.0.0-00010101000000-000000000000
	taffy.local/pkg/wsutil v0.0.0-00010101000000-000000000000
)

// 仓库内本地 module 解析：避免 server / worker 重复定义协议结构体与 WS 工具。
// 详见仓库根 go.work 与 pkg/。
replace (
	taffy.local/pkg/httpx => ../pkg/httpx
	taffy.local/pkg/protocol => ../pkg/protocol
	taffy.local/pkg/wsutil => ../pkg/wsutil
)
