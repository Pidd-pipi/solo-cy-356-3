package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	"github.com/communitygarden/server/internal/constants"
	"github.com/communitygarden/server/internal/util"
)

// RateLimit 限流中间件：优先使用 Redis 固定窗口；Redis 不可用/超时时使用进程内
// 固定窗口兜底，保证突发流量得到明确的 429，而不是把资源耗尽暴露为 500。
func RateLimit(rdb *redis.Client, logger *slog.Logger, limit int, window time.Duration) gin.HandlerFunc {
	// 每个中间件实例独立的进程内限流器（测试隔离，多实例部署时为单实例配额）
	memLimiter := newMemoryLimiter()
	return func(c *gin.Context) {
		key := fmt.Sprintf("ratelimit:%s:%s", c.ClientIP(), c.FullPath())
		allowed := false

		if rdb != nil {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 200*time.Millisecond)
			pipe := rdb.Pipeline()
			incr := pipe.Incr(ctx, key)
			pipe.Expire(ctx, key, window)
			_, execErr := pipe.Exec(ctx)
			if execErr == nil {
				allowed = incr.Val() <= int64(limit)
				cancel()
				if !allowed {
					rejectRateLimited(c, logger, key)
					return
				}
				c.Next()
				return
			}
			// Redis 故障/超时：记录后走内存兜底（不放任流量无界涌入）
			if logger != nil {
				logger.Warn(constants.LogRedisUnavailable, "err", execErr, "fallback", "in-memory rate limiter")
			}
			cancel()
		}

		if !memLimiter.allow(key, limit, time.Now(), window) {
			rejectRateLimited(c, logger, key)
			return
		}
		c.Next()
	}
}

func rejectRateLimited(c *gin.Context, logger *slog.Logger, key string) {
	if logger != nil {
		logger.Warn(constants.LogRateLimitReached, "ip", c.ClientIP(), "path", c.FullPath(), "key", key)
	}
	util.Fail(c, http.StatusTooManyRequests, constants.CodeRateLimited, constants.ErrorText[constants.CodeRateLimited])
	c.Abort()
}
