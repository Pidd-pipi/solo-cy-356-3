package middleware

import (
	"sync"
	"time"
)

// memoryLimiter 进程内固定窗口限流器：按 key 计数，窗口结束惰性重置。
// 仅作为 Redis 不可用时的兜底（单实例足够给出明确的 429，而非让流量打爆下游）。
type memoryLimiter struct {
	mu      sync.Mutex
	entries map[string]*windowCounter
}

type windowCounter struct {
	count   int64
	resetAt time.Time
}

func newMemoryLimiter() *memoryLimiter {
	ml := &memoryLimiter{entries: make(map[string]*windowCounter)}
	go ml.janitor()
	return ml
}

// allow 返回当前窗口内是否放行（占用一次配额）。
func (m *memoryLimiter) allow(key string, limit int, now time.Time, window time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	wc, ok := m.entries[key]
	if !ok || !now.Before(wc.resetAt) { // now >= resetAt 即新窗口
		m.entries[key] = &windowCounter{count: 1, resetAt: now.Add(window)}
		return int64(limit) >= 1
	}
	wc.count++
	return wc.count <= int64(limit)
}

// janitor 定期清理过期窗口，避免 key 无限增长。
func (m *memoryLimiter) janitor() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for now := range ticker.C {
		m.mu.Lock()
		for k, v := range m.entries {
			if !now.Before(v.resetAt) {
				delete(m.entries, k)
			}
		}
		m.mu.Unlock()
	}
}
