package rediskit

import (
	"context"
	"strconv"
	"time"

	"github.com/example/go-frame/pkg/library/jsonkit"
	"github.com/redis/go-redis/v9"
	"github.com/spf13/cast"
)

// 本文件补齐 Set / ZSet / Scan 三类原语，供 tokenkit 的会话索引与在线列表使用。
// 命名与调用风格对齐 redis.go：key 由调用方自行 GetKeyWithPrefix，val 非 string 自动 JSON 化。

// ZEntry ZSet 成员及其 score（score 语义由调用方约定，如在线列表用它存最后活跃时间戳）。
type ZEntry struct {
	Member string
	Score  float64
}

// SetNX 仅当 key 不存在时写入，成功返回 true。可用于分布式互斥（如登录顶号判定）。
func SetNX(ctx context.Context, key string, val any, expire time.Duration) bool {
	c := Instance()
	if _, ok := val.(string); !ok {
		val = jsonkit.ToString(val)
	}
	ok, err := c.SetNX(ctx, key, val, expire).Result()
	if err != nil {
		return false
	}
	return ok
}

// SAdd 向集合追加成员，返回新增成员数（已存在的成员不重复计数）。
func SAdd(ctx context.Context, key string, members ...string) int64 {
	c := Instance()
	n, err := c.SAdd(ctx, key, toAny(members)...).Result()
	if err != nil {
		return 0
	}
	return n
}

// SRem 从集合移除成员，返回实际移除数。
func SRem(ctx context.Context, key string, members ...string) int64 {
	c := Instance()
	n, err := c.SRem(ctx, key, toAny(members)...).Result()
	if err != nil {
		return 0
	}
	return n
}

// SMembers 返回集合全部成员；key 不存在返回空切片。
func SMembers(ctx context.Context, key string) []string {
	c := Instance()
	members, err := c.SMembers(ctx, key).Result()
	if err != nil || len(members) == 0 {
		return nil
	}
	return members
}

// SIsMember 判断成员是否在集合中。
func SIsMember(ctx context.Context, key string, member string) bool {
	c := Instance()
	ok, err := c.SIsMember(ctx, key, member).Result()
	if err != nil {
		return false
	}
	return ok
}

// SCard 返回集合基数，key 不存在为 0。
func SCard(ctx context.Context, key string) int64 {
	c := Instance()
	n, err := c.SCard(ctx, key).Result()
	if err != nil {
		return 0
	}
	return n
}

// ZAdd 以 score 更新成员（成员已存在则覆盖 score）。
func ZAdd(ctx context.Context, key string, score float64, member string) {
	c := Instance()
	c.ZAdd(ctx, key, redis.Z{Score: score, Member: member})
}

// ZRem 从有序集合移除成员，返回实际移除数。
func ZRem(ctx context.Context, key string, members ...string) int64 {
	c := Instance()
	n, err := c.ZRem(ctx, key, toAny(members)...).Result()
	if err != nil {
		return 0
	}
	return n
}

// ZCard 返回有序集合基数，key 不存在为 0。
func ZCard(ctx context.Context, key string) int64 {
	c := Instance()
	n, err := c.ZCard(ctx, key).Result()
	if err != nil {
		return 0
	}
	return n
}

// ZRevRange 按 score 从大到小取区间（start/stop 为 0 基下标，-1 表示末尾），返回成员与 score。
// 在线列表按「最近活跃时间倒序」即用此实现。
func ZRevRange(ctx context.Context, key string, start, stop int64) []ZEntry {
	c := Instance()
	items, err := c.ZRevRangeWithScores(ctx, key, start, stop).Result()
	if err != nil || len(items) == 0 {
		return nil
	}
	entries := make([]ZEntry, 0, len(items))
	for _, it := range items {
		entries = append(entries, ZEntry{Member: cast.ToString(it.Member), Score: it.Score})
	}
	return entries
}

// ZScore 返回成员的 score，成员不存在时 ok=false。
// 在线会话的「最后活跃时间」以在线索引 score 为权威，读取即用此实现。
func ZScore(ctx context.Context, key string, member string) (score float64, ok bool) {
	c := Instance()
	v, err := c.ZScore(ctx, key, member).Result()
	if err != nil {
		return 0, false
	}
	return v, true
}

// ZRemRangeByScore 移除 score 落在 [min, max] 的成员，返回移除数。
// 用于清理在线列表里「最后活跃时间早于过期窗口」的僵尸成员。
func ZRemRangeByScore(ctx context.Context, key string, min, max float64) int64 {
	c := Instance()
	n, err := c.ZRemRangeByScore(ctx, key, formatScore(min), formatScore(max)).Result()
	if err != nil {
		return 0
	}
	return n
}

// toAny go-redis 的集合命令签名是 ...interface{}，此处集中做一次转换。
func toAny(members []string) []interface{} {
	out := make([]interface{}, 0, len(members))
	for _, m := range members {
		out = append(out, m)
	}
	return out
}

// formatScore 按 redis score 区间的字面量语法序列化（闭区间用 "["，开区间用 "("）。
func formatScore(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// Scan 用 SCAN 游标遍历匹配 pattern 的全部 key。
//
// ⚠️ SCAN 是渐进式遍历，keyspace 很大时仍会扫大量 key 且不保证快照一致；
// 仅适合管理类低频调用（如按前缀清理孤儿会话），禁止放在请求热路径。
func Scan(ctx context.Context, pattern string, count int64) []string {
	c := Instance()
	if count <= 0 {
		count = 100
	}
	var (
		keys    []string
		cursor  uint64
		pageCnt = count
	)
	for {
		page, next, err := c.Scan(ctx, cursor, pattern, pageCnt).Result()
		if err != nil {
			break
		}
		keys = append(keys, page...)
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return keys
}
