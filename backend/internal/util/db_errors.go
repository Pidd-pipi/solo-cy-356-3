package util

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL 错误码（https://www.postgresql.org/docs/current/errcodes-appendix.html）。
const (
	pgCodeUniqueViolation = "23505" // unique_violation：唯一索引冲突
	pgCodeDeadlock        = "40P01" // deadlock_detected：检测到死锁，事务被中止
	pgCodeLockNotAvail    = "55P03" // lock_not_available：锁等待超时
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

// IsRetryableLockError 判断是否为锁竞争类可重试错误（死锁/锁等待超时）。
func IsRetryableLockError(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == pgCodeDeadlock || pgErr.Code == pgCodeLockNotAvail) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") // SQLite 写锁竞争
}
