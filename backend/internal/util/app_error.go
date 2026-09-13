package util

import "fmt"

// AppError 统一业务错误：携带错误码、HTTP 状态码与可读 message。
type AppError struct {
	Code       int
	HTTPStatus int
	Message    string
	cause      error
}

func (e *AppError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("code=%d http=%d msg=%s: %v", e.Code, e.HTTPStatus, e.Message, e.cause)
	}
	return fmt.Sprintf("code=%d http=%d msg=%s", e.Code, e.HTTPStatus, e.Message)
}

// Unwrap 暴露被包装的底层错误，保留 errors.Is / errors.As 错误链。
func (e *AppError) Unwrap() error {
	return e.cause
}

// NewAppError 构造业务错误。
func NewAppError(code, httpStatus int, msg string) *AppError {
	return &AppError{Code: code, HTTPStatus: httpStatus, Message: msg}
}

// Wrap 包装底层错误并保留错误链。
func (e *AppError) Wrap(err error) *AppError {
	return &AppError{Code: e.Code, HTTPStatus: e.HTTPStatus, Message: e.Message + ": " + err.Error(), cause: err}
}
