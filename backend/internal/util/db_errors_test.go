package util

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// 用真实 PostgreSQL 错误码构造错误（非文本），验证分类函数。
func pgErr(code string) error { return &pgconn.PgError{Code: code, Message: "ignored-in-classification"} }

func TestDBErrorClassification(t *testing.T) {
	type tc struct {
		name      string
		err       error
		unique    bool
		lock      bool
		retryable bool
		pool      bool
	}
	cases := []tc{
		{"pg unique 23505", pgErr("23505"), true, false, false, false},
		{"pg deadlock 40P01", pgErr("40P01"), false, true, true, false},
		{"pg lock unavailable 55P03", pgErr("55P03"), false, true, true, false},
		{"pg too many clients 53300", pgErr("53300"), false, false, true, true},
		{"pg query canceled 57014 by lock timeout", &pgconn.PgError{Code: "57014", Message: "canceling statement due to lock timeout"}, false, true, true, false},
		{"pg query canceled 57014 generic", &pgconn.PgError{Code: "57014", Message: "user request"}, false, false, false, false},
		{"sqlite locked", errors.New("database is locked"), false, true, true, false},
		{"sqlite table locked", errors.New("database table is locked: plot_members"), false, true, true, false},
		{"sqlite unique", errors.New("UNIQUE constraint failed: plot_members.plot_id"), true, false, false, false},
		{"context deadline exceeded", context.DeadlineExceeded, false, false, true, true},
		{"too many clients text", errors.New("FATAL: sorry, too many clients already"), false, false, true, true},
		{"connection pool timeout text", errors.New("connection pool timeout after 1s"), false, false, true, true},
		{"plain business error", errors.New("validation failed"), false, false, false, false},
		{"nil", nil, false, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsUniqueViolation(c.err); got != c.unique {
				t.Errorf("IsUniqueViolation=%v want %v", got, c.unique)
			}
			if got := IsLockContention(c.err); got != c.lock {
				t.Errorf("IsLockContention=%v want %v", got, c.lock)
			}
			if got := IsRetryableResourceError(c.err); got != c.retryable {
				t.Errorf("IsRetryableResourceError=%v want %v", got, c.retryable)
			}
			if got := IsPoolOrConnExhausted(c.err); got != c.pool {
				t.Errorf("IsPoolOrConnExhausted=%v want %v", got, c.pool)
			}
		})
	}
}

// 包装后的错误必须可被 errors.As 穿透识别（AppError.Unwrap 场景）。
type wrappedErr struct{ inner error }

func (w *wrappedErr) Error() string { return fmt.Sprintf("wrapped: %v", w.inner) }
func (w *wrappedErr) Unwrap() error { return w.inner }

func TestDBErrorClassificationThroughWrappers(t *testing.T) {
	wrapped := &wrappedErr{inner: pgErr("53300")}
	if !IsPoolOrConnExhausted(wrapped) {
		t.Fatal("wrapped 53300 must still be classified as pool exhausted")
	}
	if !IsRetryableResourceError(&wrappedErr{inner: pgErr("40P01")}) {
		t.Fatal("wrapped 40P01 must be retryable")
	}
	if !IsUniqueViolation(&wrappedErr{inner: pgErr("23505")}) {
		t.Fatal("wrapped 23505 must be unique violation")
	}
}

// AppError 包装锁错误后仍应被判定为可重试（service 层依赖此判定）。
func TestAppErrorUnwrapClassified(t *testing.T) {
	app := NewAppError(Code5000Test, 500, "内部错误").Wrap(pgErr("55P03"))
	if !IsRetryableResourceError(app) {
		t.Fatal("AppError wrapping 55P03 must be retryable via Unwrap chain")
	}
}

const Code5000Test = 5000
