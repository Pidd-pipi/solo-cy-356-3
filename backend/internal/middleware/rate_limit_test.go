package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRateLimit_MemoryFallbackWhenRedisUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// rdb 传 nil：直接走进程内固定窗口兜底，突发流量应得到明确 429
	r.Use(RateLimit(nil, nil, 3, time.Minute))
	r.GET("/x", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"code": 0}) })

	statuses := make([]int, 0, 6)
	for i := 0; i < 6; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "10.0.0.9:1234"
		r.ServeHTTP(w, req)
		statuses = append(statuses, w.Code)
	}
	allowed := 0
	rejected := 0
	for _, s := range statuses {
		switch s {
		case http.StatusOK:
			allowed++
		case http.StatusTooManyRequests:
			rejected++
		default:
			t.Fatalf("unexpected status %d", s)
		}
	}
	if allowed != 3 {
		t.Fatalf("allowed=%d want 3", allowed)
	}
	if rejected != 3 {
		t.Fatalf("rejected=%d want 3 (explicit 429, not 500)", rejected)
	}
}

func TestRateLimit_IndependentKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RateLimit(nil, nil, 1, time.Minute))
	r.GET("/y", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"code": 0}) })

	do := func(ip string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/y", nil)
		req.RemoteAddr = ip + ":1"
		r.ServeHTTP(w, req)
		return w.Code
	}
	if do("1.1.1.1") != http.StatusOK || do("2.2.2.2") != http.StatusOK {
		t.Fatalf("different IPs should each get their own quota")
	}
	if do("1.1.1.1") != http.StatusTooManyRequests {
		t.Fatalf("same IP second request must be 429")
	}
}
