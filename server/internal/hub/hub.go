// Package hub 实现客户端实时推送 Hub（UC-10）。
//
// 为什么独立成包：
//   - voice / device 等业务侧只想"写消息"，不关心连接生命周期；
//   - hub 内部维护 user_id → []*Client 映射，注销 / 心跳 / 并发写都封在这里。
//
// 设计目标：
//   - 不阻塞业务路径：Broadcast 内部 select+default 丢弃满队列的客户端连接，避免某个慢客户端拖垮全链路；
//   - 可降级：MQTT bridge 缺失 / Redis 缺失时 hub 仍正常工作（推送基于内存）；
//   - 弱依赖业务：仅暴露 [Publisher] 接口给 service / handler 调用，反向依赖被切断。
package hub

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"taffy.local/pkg/wsutil"
)

// 单个 client 队列的最大缓冲。超过即丢弃后续帧（业务侧应能接受偶发丢帧——客户端
// 重新加载列表即可恢复一致性）。
const sendBufferSize = 32

// Publisher 给业务侧用的最小接口：把一条事件发给某个用户的所有在线连接。
type Publisher interface {
	BroadcastToUser(userID int64, event any)
}

// Hub 客户端推送中心。
type Hub struct {
	mu      sync.RWMutex
	clients map[int64]map[*Client]struct{} // user_id → 连接集合
	logger  *slog.Logger
}

// New 创建一个空 Hub。
func New(logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{
		clients: make(map[int64]map[*Client]struct{}),
		logger:  logger.With("component", "hub"),
	}
}

// Client 一个用户连接（通常一个 user 可能有多个并发会话：多设备 / 多 Tab）。
type Client struct {
	hub    *Hub
	userID int64
	conn   *websocket.Conn
	send   chan []byte
	once   sync.Once
}

// Register 接管一个已 upgrade 的 ws 连接。阻塞到连接关闭。
//
// 用法：
//
//	c := hub.Register(userID, conn)
//	defer c.Close()
//	c.Run()  // 阻塞读循环，直到客户端断开
func (h *Hub) Register(userID int64, conn *websocket.Conn) *Client {
	c := &Client{
		hub:    h,
		userID: userID,
		conn:   conn,
		send:   make(chan []byte, sendBufferSize),
	}
	h.mu.Lock()
	if _, ok := h.clients[userID]; !ok {
		h.clients[userID] = make(map[*Client]struct{})
	}
	h.clients[userID][c] = struct{}{}
	h.mu.Unlock()
	h.logger.Info("client connected", "user_id", userID, "total", h.countLocked(userID))
	return c
}

// Run 启动读 / 写两个 goroutine 并阻塞至连接关闭。
func (c *Client) Run() {
	stop := make(chan struct{})
	go c.writePump(stop)
	c.readPump()
	close(stop)
	c.Close()
}

// readPump 阻塞读取，仅用于检测断连——客户端不需要发任何业务帧。
func (c *Client) readPump() {
	defer c.conn.Close()
	c.conn.SetReadLimit(1024)
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			if !wsutil.IsNormalClose(err) {
				c.hub.logger.Debug("client read err", "user_id", c.userID, "err", err)
			}
			return
		}
	}
}

// writePump 阻塞向客户端发送队列中的帧；定时 ping 保活。
func (c *Client) writePump(stop <-chan struct{}) {
	pingTicker := time.NewTicker(20 * time.Second)
	defer pingTicker.Stop()
	for {
		select {
		case data, ok := <-c.send:
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := c.conn.WriteMessage(websocket.TextMessage, data); err != nil {
				c.hub.logger.Debug("client write err", "user_id", c.userID, "err", err)
				return
			}
		case <-pingTicker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-stop:
			return
		}
	}
}

// Close 从 Hub 摘除并关闭底层连接。重复调用安全。
func (c *Client) Close() {
	c.once.Do(func() {
		c.hub.mu.Lock()
		if set, ok := c.hub.clients[c.userID]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(c.hub.clients, c.userID)
			}
		}
		c.hub.mu.Unlock()
		close(c.send)
		_ = c.conn.Close()
		c.hub.logger.Info("client disconnected", "user_id", c.userID)
	})
}

// BroadcastToUser 向指定 user 的所有连接广播事件。
// 业务路径同步调用此方法不会阻塞 —— 单个 client 队列满即丢弃。
func (h *Hub) BroadcastToUser(userID int64, event any) {
	data, err := json.Marshal(event)
	if err != nil {
		h.logger.Warn("broadcast marshal failed", "err", err)
		return
	}

	h.mu.RLock()
	set := h.clients[userID]
	targets := make([]*Client, 0, len(set))
	for c := range set {
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	if len(targets) == 0 {
		return
	}

	for _, c := range targets {
		select {
		case c.send <- data:
		default:
			h.logger.Warn("client send buffer full, dropping event",
				"user_id", userID, "buffer", sendBufferSize)
		}
	}
}

func (h *Hub) countLocked(userID int64) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients[userID])
}
