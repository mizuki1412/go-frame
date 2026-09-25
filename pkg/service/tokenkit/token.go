// Package tokenkit 提供登录会话的签发、校验、续期、销毁与在线管理。
//
// 设计取舍：token 为**不透明随机串**（crypto/rand 32 字节 hex），不含任何自解释载荷，
// 一切状态以服务端会话记录为准。相比 JWT 换来三件事：
//   - 剔出即时生效，不存在「等 exp 到期」的窗口；
//   - 无需配置签名密钥，消灭「空密钥/默认密钥」这一类 P0 配置风险；
//   - 多端会话、顶号、在线列表天然是一组 Redis 原语，无需自造状态机。
//
// 存储布局（逻辑 key，redis 后端会自动加 redis.prefix）：
//
//	tk:t:<token>  STRING  会话 JSON，TTL = IdleTtl（滑动续期按空闲窗口一半节流，见 Refresh）
//	tk:u:<uid>    SET     该用户当前在线的 token 集合
//	tk:online     ZSET    全局在线索引，member=token，score=最后活跃时间（毫秒，实时权威）
//
// 两道独立过期闸门：IdleTtl 管「多久没动」（鉴权滑动续期），
// ExpireTtl 管「总共能活多久」（以 LoginTime 为起点，滑动续期无法延长）。
//
// 用户 id 一律用 **string**：tokenkit 只把 id 当不透明标识用于建索引与传参，
// 不关心它源自自增 bigint、雪花 id 还是 UUID。业务侧在自己的边界上转换
// （如 mod/user 的 int64 sys_user.id ↔ 字符串），tokenkit 不做任何数值假设。
package tokenkit

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"time"

	"github.com/example/go-frame/pkg/cli/configkey"
	"github.com/example/go-frame/pkg/library/jsonkit"
	"github.com/example/go-frame/pkg/service/configkit"
)

// 逻辑 key 前缀，集中在此避免散落字符串拼接。
const (
	keySessionPrefix = "tk:t:"
	keyUserPrefix    = "tk:u:"
	keyOnline        = "tk:online"
)

// Session 一次登录会话的完整记录。
type Session struct {
	// UserId 用户标识。**不透明字符串**，本包不对其做任何数值假设——
	// 自增 bigint 由调用方 Format 成十进制串，UUID/雪花 id 直接原样传入。
	UserId string `json:"userId"`
	// Scope token 所属域，如 "app"/"admin"；用于同一套会话存储下的域隔离。
	Scope string `json:"scope,omitempty"`
	// Device 终端标识（如 "web"/"ios"/"mini"），仅作展示与多端区分。
	Device string `json:"device,omitempty"`
	Ip     string `json:"ip,omitempty"`
	// UserAgent 浏览器/客户端 UA，仅作展示。
	UserAgent string `json:"userAgent,omitempty"`
	// LoginTime 签发时刻，ExpireTtl 的计时起点。
	LoginTime time.Time `json:"loginTime"`
	// LastActive 最近一次鉴权通过的时刻，在线列表按此倒序。
	LastActive time.Time `json:"lastActive"`
}

// Online 在线会话条目：会话记录 + 原始 token。
type Online struct {
	Session
	Token string `json:"token"`
}

// Option 会话签发选项。
type Option func(*Session)

// WithScope 标记 token 域，供 middleware.AuthLogin(scopes...) 做域隔离。
func WithScope(scope string) Option {
	return func(s *Session) { s.Scope = scope }
}

// WithDevice 标记终端类型。
func WithDevice(device string) Option {
	return func(s *Session) { s.Device = device }
}

// WithClient 记录客户端 IP 与 UA，供在线用户列表展示。
func WithClient(ip, userAgent string) Option {
	return func(s *Session) {
		s.Ip = ip
		s.UserAgent = userAgent
	}
}

// IdleTtl 会话空闲窗口（token.idle 小时）。
// <=0（未配置或显式禁用）时退化为不滑动：窗口=ExpireTtl。
func IdleTtl() time.Duration {
	idle := configkit.GetInt(configkey.TokenIdle)
	if idle <= 0 {
		idle = configkit.GetInt(configkey.TokenExpire, 168)
	}
	return time.Duration(idle) * time.Hour
}

// ExpireTtl 会话绝对上限（token.expire 小时），滑动续期无法延长。
func ExpireTtl() time.Duration {
	return time.Duration(configkit.GetInt(configkey.TokenExpire, 168)) * time.Hour
}

// AllowMultiLogin 是否允许同一账号多端同时在线（token.multiLogin，默认 true）。
// 关闭时 Create 会先剔出该用户此前所有会话，仅保留最新一个。
func AllowMultiLogin() bool {
	return configkit.GetBool(configkey.TokenMultiLogin, true)
}

// Create 为 userId 签发新会话并返回 token 字符串。
// userId 为空说明调用方没拿到登录身份，属于不可恢复的调用错误，直接 panic。
// token.multiLogin=false 时，本次登录会剔出该用户全部旧会话（顶号）。
func Create(userId string, opts ...Option) string {
	if userId == "" {
		panic("tokenkit: Create 收到空 userId")
	}
	token := newToken()
	now := time.Now()
	s := &Session{UserId: userId, LoginTime: now, LastActive: now}
	for _, opt := range opts {
		opt(s)
	}
	if !AllowMultiLogin() {
		DestroyByUser(userId)
	}
	store := getStore()
	store.set(keySessionPrefix+token, []byte(jsonkit.ToString(s)), IdleTtl())
	store.sadd(keyUserPrefix+userId, token)
	store.zadd(keyOnline, float64(now.UnixMilli()), token)
	return token
}

// Parse 查询会话。不存在、已空闲超时或已超绝对上限时返回 nil（视为未登录）。
// 本函数**不做续期**，仅读取；请求链路中应使用 Refresh。
func Parse(token string) *Session {
	if token == "" {
		return nil
	}
	raw := getStore().get(keySessionPrefix + token)
	if len(raw) == 0 {
		return nil
	}
	s := &Session{}
	// LoginTime 是 Create 必填的不变量：为零值说明记录损坏（手工改库/旧版本残留），
	// 按未登录处理并顺手清掉，避免每次请求都重复解析失败。
	// 之所以能拿它当哨兵，是因为 UserId 是 string——空串也可能来自 "" 以外的
	// 业务语义（0 号用户是合法输入），没有可靠的数值零值可用。
	if err := jsonkit.ParseObj(string(raw), s); err != nil || s.LoginTime.IsZero() {
		Destroy(token)
		return nil
	}
	if s.expired() {
		Destroy(token)
		return nil
	}
	return s
}

// Refresh 滑动续期：推进在线索引的最后活跃时间；返回解析出的会话与是否成功。
// 会话不存在/已超绝对上限时返回 (nil, false)。
//
// 写节流：会话记录（tk:t:）距上次落库不足空闲窗口一半时**不重写**（连 TTL 也不动——
// 活跃会话的 key 始终保有 ≥ 窗口一半的余量，不会中途过期），超过一半才整体重写并拉满 TTL。
// 由此把每请求的存储开销从「1 读 + 3 写」压到常规情况下的「1 读 + 1 写」；
// 代价是记录里的 LastActive 允许 idle/2 的滞后，**实时权威在在线索引的 score**，
// 展示类读取（LastActive/ListOnline/ListOnlineOf）一律走 score。
// 原 token 已在 Create 时写入用户索引，此处不再重复 sadd。
func Refresh(token string) (*Session, bool) {
	s := Parse(token)
	if s == nil {
		return nil, false
	}
	now := time.Now()
	if now.Sub(s.LastActive) >= IdleTtl()/2 {
		s.LastActive = now
		getStore().set(keySessionPrefix+token, []byte(jsonkit.ToString(s)), IdleTtl())
	}
	getStore().zadd(keyOnline, float64(now.UnixMilli()), token)
	return s, true
}

// LastActive 返回会话最近活跃时刻，会话不存在返回零值。
// 以在线索引 score 为准（实时）；索引缺失时回读会话记录兜底（可能有 idle/2 滞后）。
func LastActive(token string) time.Time {
	if score, ok := getStore().zscore(keyOnline, token); ok {
		return time.UnixMilli(int64(score))
	}
	s := Parse(token)
	if s == nil {
		return time.Time{}
	}
	return s.LastActive
}

// Destroy 销毁单个会话（登出）。token 为空时静默返回。
func Destroy(token string) {
	if token == "" {
		return
	}
	// 先读会话拿到 uid，才能同步清掉用户索引里的成员
	store := getStore()
	raw := store.get(keySessionPrefix + token)
	if len(raw) > 0 {
		s := &Session{}
		if err := jsonkit.ParseObj(string(raw), s); err == nil && !s.LoginTime.IsZero() {
			store.srem(keyUserPrefix+s.UserId, token)
		}
	}
	store.del(keySessionPrefix + token)
	store.zrem(keyOnline, token)
}

// DestroyByUser 剔出该用户全部在线会话，返回被销毁的会话数。
func DestroyByUser(userId string) int {
	if userId == "" {
		return 0
	}
	store := getStore()
	userKey := keyUserPrefix + userId
	tokens := store.smembers(userKey)
	if len(tokens) == 0 {
		// 用户索引可能因历史数据不一致为空，回退扫在线索引兜底
		tokens = scanUserTokens(store, userId)
	}
	for _, t := range tokens {
		store.del(keySessionPrefix + t)
		store.zrem(keyOnline, t)
	}
	store.del(userKey)
	return len(tokens)
}

// ListByUser 返回该用户当前在线的 token 列表。
func ListByUser(userId string) []string {
	if userId == "" {
		return nil
	}
	store := getStore()
	tokens := store.smembers(keyUserPrefix + userId)
	if len(tokens) == 0 {
		return nil
	}
	// 过滤已失效会话（用户索引可能残留过期 token）
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		if Parse(t) != nil {
			out = append(out, t)
		}
	}
	return out
}

// OnlineCount 该用户当前在线会话数。
func OnlineCount(userId string) int { return len(ListByUser(userId)) }

// ListOnline 返回全局在线会话，按最近活跃时间倒序分页。
// 返回条目与清理后的在线总数（先剔除超过空闲窗口的僵尸索引，再分页）。
//
// ⚠️ 页内会逐个回读会话记录以保证只返回真实在线的条目，因此单页 limit 越大越慢；
// limit 建议不超过 200。
func ListOnline(offset, limit int) ([]Online, int64) {
	store := getStore()
	// 空闲窗口之外没有任何刷新，索引成员必为僵尸，一次区间删除比逐条判断便宜得多
	staleBefore := float64(time.Now().Add(-IdleTtl()).UnixMilli())
	store.zremrangebyscore(keyOnline, 0, staleBefore)
	total := store.zcard(keyOnline)
	if limit <= 0 {
		return nil, total
	}
	entries := store.zrevrange(keyOnline, int64(offset), int64(offset+limit-1))
	out := make([]Online, 0, len(entries))
	for _, e := range entries {
		s := Parse(e.Member)
		if s == nil {
			// 会话记录已失效但索引尚在（进程崩溃/手工删 key），顺手清掉
			store.zrem(keyOnline, e.Member)
			continue
		}
		o := Online{Session: *s, Token: e.Member}
		// LastActive 以在线索引 score 为权威（会话记录因写节流允许 idle/2 滞后）
		o.LastActive = time.UnixMilli(int64(e.Score))
		out = append(out, o)
	}
	return out, total
}

// ListOnlineOf 返回某用户当前在线的会话详情，按最近活跃时间倒序。
func ListOnlineOf(userId string) []Online {
	tokens := ListByUser(userId)
	out := make([]Online, 0, len(tokens))
	for _, t := range tokens {
		if s := Parse(t); s != nil {
			out = append(out, Online{Session: *s, Token: t})
		}
	}
	// 同 ListOnline：LastActive 以在线索引 score 为权威
	for i := range out {
		if score, ok := getStore().zscore(keyOnline, out[i].Token); ok {
			out[i].LastActive = time.UnixMilli(int64(score))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActive.After(out[j].LastActive) })
	return out
}

// expired 判断会话是否超出绝对上限。以 LoginTime 为基准，滑动续期无法延长。
func (s *Session) expired() bool {
	return time.Since(s.LoginTime) > ExpireTtl()
}

// newToken 生成 32 字节加密随机 token（64 位 hex 字符）。
// crypto/rand 读取失败属系统级异常，此时直接 panic——退化为可预测 token 比崩溃危险得多。
func newToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

// scanUserTokens 用户索引缺失时，从在线索引回捞该用户的会话。
// 正常链路不会走到这里（Create/Destroy 都维护用户索引），仅兜底历史脏数据。
func scanUserTokens(s store, userId string) []string {
	var out []string
	for _, e := range s.zrevrange(keyOnline, 0, -1) {
		if sess := Parse(e.Member); sess != nil && sess.UserId == userId {
			out = append(out, e.Member)
		}
	}
	return out
}
