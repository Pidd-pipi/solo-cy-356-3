package util

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL 错误码（https://www.postgresql.org/docs/current/errcodes-appendix.html）。
const (
	pgCodeUniqueViolation = "23505" // unique_violation：唯一索引冲突
	pgCodeDeadlock        = "40P01" // deadlock_detected：检测到死锁，事务被中止
	pgCodeLockNotAvail    = "55P03" // lock_not_available：lock_timeout 达到
	pgCodeTooManyClients  = "53300" // too_many_connections：服务器连接数已满
	pgCodeQueryCanceled   = "57014" // query_canceled：statement_timeout / 用户取消
)

// IsUniqueViolation 判断是否唯一索引冲突（兼容 PostgreSQL 与 SQLite）。
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgCodeUniqueViolation {
		return true
	}
	// SQLite（glebarez 纯 Go 驱动）唯一冲突文本
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") || strings.Contains(msg, "duplicate key")
}

// IsLockContention 判断是否为锁竞争错误：死锁、锁等待超时、语句超时被锁等待触发。
func IsLockContention(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgCodeDeadlock, pgCodeLockNotAvail:
			return true
		case pgCodeQueryCanceled:
			// statement_timeout 命中（等待行锁时取消也返回 57014，消息含 lock/timeout）
			msg := strings.ToLower(pgErr.Message)
			return strings.Contains(msg, "lock") || strings.Contains(msg, "timeout")
		}
	}
	msg := strings.ToLower(err.Error())
	// SQLite 写锁竞争
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked")
}

// IsPoolOrConnExhausted 判断是否为数据库连接资源耗尽：
// 连接池等待超时（pgxpool）、连接建立超时/取消、PostgreSQL 连接数已满。
// 这些错误说明请求过多，应排队重试，重试耗尽后给调用方明确的“繁忙”结果，而非 500。
func IsPoolOrConnExhausted(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgCodeTooManyClients {
		return true
	}
	// 连接池获取连接等待超时（pgx v5 返回 context deadline exceeded）
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "too many clients"),
		strings.Contains(msg, "too many connections"),
		strings.Contains(msg, "connection pool timeout"),
		strings.Contains(msg, "acquire timeout"),
		strings.Contains(msg, "timed out getting a connection"),
		strings.Contains(msg, "context deadline exceeded"),
		strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "server closed the connection"):
		return true
	}
	return false
}

// IsRetryableLockError 兼容旧调用：锁竞争错误。
func IsRetryableLockError(err error) bool {
	return IsLockContention(err)
}

// IsRetryableResourceError 资源/锁类可重试错误（死锁、锁超时、连接池耗尽等瞬态错误）。
func IsRetryableResourceError(err error) bool {
	return IsLockContention(err) || IsPoolOrConnExhausted(err)
}
