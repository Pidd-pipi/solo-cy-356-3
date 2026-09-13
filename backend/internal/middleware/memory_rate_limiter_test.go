package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// 通过注入时钟，确定性验证固定窗口：窗口内拒绝、窗口到达后配额重置、不同 key 独立。
func TestMemoryLimiterWindowReset(t *testing.T) {
	ml := newMemoryLimiter()
	start := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	const win = time.Minute
	const limit = 3

	// 第一个窗口：恰好 limit 次放行，之后拒绝
	for i := 1; i <= limit; i++ {
		if !ml.allow("k", limit, start, win) {
			t.Fatalf("request %d in window should be allowed", i)
		}
	}
	if ml.allow("k", limit, start.Add(10*time.Second), win) {
		t.Fatal("4th request within same window must be rejected")
	}
	if ml.allow("k", limit, start.Add(59*time.Second), win) {
		t.Fatal("request at 59s still belongs to same window, must reject")
	}

	// 窗口边界到达（>= resetAt）：配额必须重置
	if !ml.allow("k", limit, start.Add(time.Minute), win) { // 新窗口第 1 次
		t.Fatal("request exactly at window boundary should reset and allow")
	}
	if !ml.allow("k", limit, start.Add(time.Minute+30*time.Second), win) { // 第 2 次
		t.Fatal("request in new window should be allowed")
	}
	if !ml.allow("k", limit, start.Add(time.Minute+40*time.Second), win) { // 第 3 次
		t.Fatal("3rd request in new window should still be allowed")
	}
	if ml.allow("k", limit, start.Add(time.Minute+50*time.Second), win) { // 第 4 次拒绝
		t.Fatal("over-limit request in new window must reject")
	}

	// 多个后续窗口都能稳定重置（可重复性）
	for w := 2; w <= 5; w++ {
		at := start.Add(time.Duration(w) * win)
		for i := 0; i < limit; i++ {
			if !ml.allow("k", limit, at.Add(time.Duration(i)*time.Second), win) {
				t.Fatalf("window %d request %d should be allowed", w, i)
			}
		}
		if ml.allow("k", limit, at.Add(time.Duration(limit)*time.Second), win) {
			t.Fatalf("window %d over-limit request must reject", w)
		}
	}
}

func TestMemoryLimiterIndependentKeys(t *testing.T) {
	ml := newMemoryLimiter()
	now := time.Now()
	// 两个不同 key 各自享有完整配额，互不影响
	for i := 0; i < 3; i++ {
		if !ml.allow("a", 3, now, time.Minute) {
			t.Fatalf("a request %d allowed", i)
		}
		if !ml.allow("b", 3, now, time.Minute) {
			t.Fatalf("b request %d allowed", i)
		}
	}
	if ml.allow("a", 3, now, time.Minute) {
		t.Fatal("a must be at limit")
	}
	if ml.allow("b", 3, now, time.Minute) {
		t.Fatal("b is also at limit")
	}
	// 但新窗口后 a/b 各自独立重置
	if !ml.allow("a", 3, now.Add(time.Minute+time.Second), time.Minute) {
		t.Fatal("a quota should reset independently")
	}
}

// 过期窗口条目被惰性覆盖，entries 不无限增长。
func TestMemoryLimiterExpiredEntryRecycled(t *testing.T) {
	ml := newMemoryLimiter()
	start := time.Now().Add(-time.Hour)
	ml.allow("old", 1, start, time.Minute)
	if len(ml.entries) != 1 {
		t.Fatalf("entries=%d want 1", len(ml.entries))
	}
	// 同一 key 在很久之后访问，应开新窗口而不是被旧计数拒绝
	if !ml.allow("old", 1, time.Now(), time.Minute) {
		t.Fatal("expired window must be recycled")
	}
}

// RateLimit 中间件：Redis 为 nil 时内存兜底，突发返回明确 429，不出现 500。
func TestRateLimit_MemoryFallbackBurst(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for round := 0; round < 3; round++ { // 重复运行确保稳定
		r := gin.New()
		r.Use(RateLimit(nil, nil, 5, time.Minute))
		r.GET("/burst", func(c *gin.Context) { c.Status(http.StatusOK) })
		codes := make([]int, 0, 12)
		for i := 0; i < 12; i++ {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/burst", nil)
			req.RemoteAddr = "203.0.113.7:5000"
			r.ServeHTTP(w, req)
			codes = append(codes, w.Code)
		}
		allowed, limited := 0, 0
		for _, c := range codes {
			switch c {
			case http.StatusOK:
				allowed++
			case http.StatusTooManyRequests:
				limited++
			default:
				t.Fatalf("round %d unexpected status %d (only 200/429 expected)", round, c)
			}
		}
		if allowed != 5 || limited != 7 {
			t.Fatalf("round %d allowed=%d limited=%d want 5/7", round, allowed, limited)
		}
	}
}
