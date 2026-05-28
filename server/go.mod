module taffy-server

go 1.22

require (
	github.com/go-sql-driver/mysql v1.8.1
	github.com/golang-jwt/jwt/v5 v5.2.2
	github.com/gorilla/websocket v1.5.3
	github.com/redis/go-redis/v9 v9.7.0
	golang.org/x/crypto v0.31.0
	gopkg.in/yaml.v3 v3.0.1
	taffy.local/pkg/protocol v0.0.0-00010101000000-000000000000
)

require (
	filippo.io/edwards25519 v1.1.0 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
)

// 仓库内本地 module 解析：避免 server / worker 重复定义协议结构体。
// 详见仓库根 go.work 与 pkg/protocol/。
replace taffy.local/pkg/protocol => ../pkg/protocol
