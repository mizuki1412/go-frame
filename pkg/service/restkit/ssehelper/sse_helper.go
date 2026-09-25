package ssehelper

import (
	"sync"
	"time"

	"github.com/example/go-frame/pkg/service/logkit"
	"github.com/example/go-frame/pkg/service/restkit/context"
)

// pingInterval 心跳间隔。SSE 空闲时反代/浏览器可能掐断长连接，
// ServiceClient 定期写一行 SSE 注释帧保活（注释帧客户端不感知）。
const pingInterval = 30 * time.Second

// 在线客户端注册表。
// P0 修复：原实现仅用 sync.Once 保证初始化，AddClient/RemoveClient/ToSend
// 对 map 的增删读全部无锁——HTTP 每请求一个 goroutine，并发即触发
// fatal error: concurrent map writes（不可 recover，直接打崩进程）。
// 现统一以 RWMutex 保护；channel 在移除时 close，使消费方的 for/select 得以退出。
var (
	clientChannels map[string]chan string
	mux            sync.RWMutex
)

func init() {
	clientChannels = make(map[string]chan string)
}

// AddClient 注册一个 SSE 客户端，返回本连接专属的接收 channel；
// channel 被 close 即代表连接已失效（被替换或移除），消费方应立即退出。
// 小缓冲 + ToSend 非阻塞发送：消费方短暂阻塞时不至于卡住发送方。
//
// 同 clientId 重连（如页面刷新）时替换旧 channel 并关闭之：
// 旧连接的消费循环退出，新连接的 channel 独享消息，不会两条连接分食。
func AddClient(clientId string, ctx *context.Context) chan string {
	ch := make(chan string, 16)
	mux.Lock()
	if old, ok := clientChannels[clientId]; ok {
		clientChannels[clientId] = ch
		// 锁内替换并 close 旧 channel，替换与关闭原子，杜绝 send on closed channel
		close(old)
	} else {
		clientChannels[clientId] = ch
	}
	mux.Unlock()
	logkit.Info("SSE Client add: " + clientId)
	// 本连接断开后自清理；removeIfCurrent 保证晚到的旧连接断开通知不会误删新连接
	go func() {
		<-ctx.Proxy.Request.Context().Done()
		removeIfCurrent(clientId, ch)
	}()
	return ch
}

// removeIfCurrent 仅当 clientId 仍指向 ch 时移除并关闭。
// 重连后旧连接的断开通知可能晚到，直接按 clientId 删会误关新连接的 channel。
func removeIfCurrent(clientId string, ch chan string) {
	mux.Lock()
	if cur, ok := clientChannels[clientId]; ok && cur == ch {
		delete(clientChannels, clientId)
		close(ch)
		logkit.Info("SSE Client close: " + clientId)
	}
	mux.Unlock()
}

// RemoveClient 移除指定客户端（无论 channel 是否还是当初注册的那个）。
func RemoveClient(clientId string) {
	mux.Lock()
	defer mux.Unlock()
	if c, ok := clientChannels[clientId]; ok {
		delete(clientChannels, clientId)
		close(c) // 通知消费循环退出，回收 goroutine
		logkit.Info("SSE Client close: " + clientId)
	}
}

// ServiceClient 注册并消费 clientId 的推送消息，直到连接失效。
// 心跳注释帧与消息在同一个 goroutine 串行写出，不存在并发写响应。
func ServiceClient(clientId string, ctx *context.Context) {
	ch := AddClient(clientId, ctx)
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	done := ctx.Proxy.Request.Context().Done()
	for {
		select {
		case msg, ok := <-ch:
			if !ok { // 被替换或移除
				return
			}
			ctx.SendSSE(msg)
		case <-ticker.C:
			if _, err := ctx.Proxy.Writer.WriteString(": ping\n\n"); err != nil {
				logkit.Error("SSE ping write failed: " + err.Error())
				removeIfCurrent(clientId, ch)
				return
			}
			ctx.Proxy.Writer.Flush()
		case <-done:
			removeIfCurrent(clientId, ch)
			return
		}
	}
}

// ToSend 向指定客户端推送消息（非阻塞：缓冲满则丢弃并记日志）。
// 发送全程持读锁，与移除路径的写锁互斥，杜绝 send on closed channel。
func ToSend(clientId string, msg string) {
	mux.RLock()
	defer mux.RUnlock()
	if c, ok := clientChannels[clientId]; ok {
		select {
		case c <- msg:
		default:
			logkit.Error("SSE client buffer full, drop message: " + clientId)
		}
	}
}
