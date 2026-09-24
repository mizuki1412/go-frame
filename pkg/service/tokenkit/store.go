package tokenkit

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/example/go-frame/pkg/service/rediskit"
)

// 存储后端抽象：配置了 redis.host 时走 Redis（多实例共享登录态），
// 未配置则退回进程内内存（单机开发与单元测试）。两种实现的 key 语义完全一致，
// 上层无需感知差异。
//
// ⚠️ 内存后端的登录态只在本进程有效，多实例部署必须配置 redis，
// 否则 A 实例登录的 token 在 B 实例上校验必然失败（与 cachekit 的限制同源）。

// scored 有序集合成员及其 score。
type scored struct {
	Member string
	Score  float64
}

// store 会话数据所需的最小存储能力集合。
type store interface {
	get(key string) []byte
	set(key string, val []byte, ttl time.Duration)
	del(keys ...string)
	sadd(key string, members ...string)
	srem(key string, members ...string)
	smembers(key string) []string
	zadd(key string, score float64, member string)
	zrem(key string, members ...string)
	// zrevrange 按 score 倒序返回 [start, stop] 区间，-1 表示到末尾。
	zrevrange(key string, start, stop int64) []scored
	zcard(key string) int64
	// zremrangebyscore 清理 [min, max] 区间的成员，返回移除数。
	zremrangebyscore(key string, min, max float64) int64
}

var (
	backend     store
	backendOnce sync.Once
)

// getStore 惰性选定后端：redis 优先，内存兜底。
func getStore() store {
	backendOnce.Do(func() {
		if rediskit.HasConfig() {
			backend = &redisStore{}
		} else {
			backend = newMemoryStore()
		}
	})
	return backend
}

// UseMemoryBackend 强制使用内存后端，仅供单元测试在无 Redis 环境下隔离状态。
func UseMemoryBackend() {
	backendOnce.Do(func() {})
	backend = newMemoryStore()
}

// ---------------------------------------------------------------- Redis 后端

type redisStore struct{}

var redisCtx = context.Background()

// key 统一在此加 redis.prefix，业务侧只见逻辑 key。
func (s *redisStore) key(k string) string { return rediskit.GetKeyWithPrefix(k) }

func (s *redisStore) get(key string) []byte {
	v := rediskit.Get(redisCtx, s.key(key), "")
	if v == "" {
		return nil
	}
	return []byte(v)
}

func (s *redisStore) set(key string, val []byte, ttl time.Duration) {
	rediskit.Set(redisCtx, s.key(key), string(val), ttl)
}

func (s *redisStore) del(keys ...string) {
	full := make([]string, 0, len(keys))
	for _, k := range keys {
		full = append(full, s.key(k))
	}
	rediskit.Del(redisCtx, full...)
}

func (s *redisStore) sadd(key string, members ...string) {
	rediskit.SAdd(redisCtx, s.key(key), members...)
}

func (s *redisStore) srem(key string, members ...string) {
	rediskit.SRem(redisCtx, s.key(key), members...)
}

func (s *redisStore) smembers(key string) []string {
	return rediskit.SMembers(redisCtx, s.key(key))
}

func (s *redisStore) zadd(key string, score float64, member string) {
	rediskit.ZAdd(redisCtx, s.key(key), score, member)
}

func (s *redisStore) zrem(key string, members ...string) {
	rediskit.ZRem(redisCtx, s.key(key), members...)
}

func (s *redisStore) zrevrange(key string, start, stop int64) []scored {
	entries := rediskit.ZRevRange(redisCtx, s.key(key), start, stop)
	out := make([]scored, 0, len(entries))
	for _, e := range entries {
		out = append(out, scored{Member: e.Member, Score: e.Score})
	}
	return out
}

func (s *redisStore) zcard(key string) int64 {
	return rediskit.ZCard(redisCtx, s.key(key))
}

func (s *redisStore) zremrangebyscore(key string, min, max float64) int64 {
	return rediskit.ZRemRangeByScore(redisCtx, s.key(key), min, max)
}

// ---------------------------------------------------------------- 内存后端

type memEntry struct {
	val      []byte
	expireAt time.Time // 零值表示永不过期
	zset     map[string]float64
	set      map[string]struct{}
}

func (e *memEntry) alive(now time.Time) bool {
	return e.expireAt.IsZero() || e.expireAt.After(now)
}

type memoryStore struct {
	mu      sync.RWMutex
	entries map[string]*memEntry
}

func newMemoryStore() *memoryStore {
	return &memoryStore{entries: make(map[string]*memEntry)}
}

func (m *memoryStore) entry(key string) *memEntry {
	e, ok := m.entries[key]
	if !ok {
		e = &memEntry{}
		m.entries[key] = e
	}
	return e
}

// dropLocked 清理已过期条目，调用方须持写锁。
func (m *memoryStore) dropLocked(key string) {
	e, ok := m.entries[key]
	if !ok {
		return
	}
	if e.alive(time.Now()) {
		return
	}
	delete(m.entries, key)
}

func (m *memoryStore) get(key string) []byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[key]
	if !ok || !e.alive(time.Now()) {
		return nil
	}
	return e.val
}

func (m *memoryStore) set(key string, val []byte, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entry(key)
	e.val = val
	e.expireAt = time.Time{}
	if ttl > 0 {
		e.expireAt = time.Now().Add(ttl)
	}
}

func (m *memoryStore) del(keys ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.entries, k)
	}
}

func (m *memoryStore) sadd(key string, members ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entry(key)
	if e.set == nil {
		e.set = make(map[string]struct{}, len(members))
	}
	for _, v := range members {
		e.set[v] = struct{}{}
	}
}

func (m *memoryStore) srem(key string, members ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		return
	}
	for _, v := range members {
		delete(e.set, v)
	}
}

func (m *memoryStore) smembers(key string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[key]
	if !ok || !e.alive(time.Now()) {
		return nil
	}
	out := make([]string, 0, len(e.set))
	for v := range e.set {
		out = append(out, v)
	}
	return out
}

func (m *memoryStore) zadd(key string, score float64, member string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.entry(key)
	if e.zset == nil {
		e.zset = make(map[string]float64)
	}
	e.zset[member] = score
}

func (m *memoryStore) zrem(key string, members ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		return
	}
	for _, v := range members {
		delete(e.zset, v)
	}
}

func (m *memoryStore) zrevrange(key string, start, stop int64) []scored {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[key]
	if !ok || !e.alive(time.Now()) {
		return nil
	}
	all := make([]scored, 0, len(e.zset))
	for member, score := range e.zset {
		all = append(all, scored{Member: member, Score: score})
	}
	// score 倒序；同分按 member 升序，保证分页稳定
	sort.Slice(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].Member < all[j].Member
	})
	from, to := sliceRange(start, stop, len(all))
	out := make([]scored, 0, to-from)
	out = append(out, all[from:to]...)
	return out
}

func (m *memoryStore) zcard(key string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[key]
	if !ok || !e.alive(time.Now()) {
		return 0
	}
	return int64(len(e.zset))
}

func (m *memoryStore) zremrangebyscore(key string, min, max float64) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok {
		return 0
	}
	var n int64
	for member, score := range e.zset {
		if score >= min && score <= max {
			delete(e.zset, member)
			n++
		}
	}
	return n
}

// sliceRange 把可能为负的 redis 风格下标（-1=末尾）归一化为 [from, to) 闭开区间。
func sliceRange(start, stop int64, length int) (from, to int) {
	if start < 0 {
		start += int64(length)
	}
	if stop < 0 {
		stop += int64(length)
	}
	if start < 0 {
		start = 0
	}
	if stop >= int64(length) {
		stop = int64(length) - 1
	}
	if start > stop || length == 0 {
		return 0, 0
	}
	return int(start), int(stop) + 1
}
