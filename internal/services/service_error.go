package services

import (
	"errors"
	"fmt"
)

type ServiceErrorCode string

const (
	ErrInvalidInput ServiceErrorCode = "INVALID_INPUT"
	ErrUpstream     ServiceErrorCode = "UPSTREAM_ERROR"
	ErrNoData       ServiceErrorCode = "NO_DATA"
	ErrTimeout      ServiceErrorCode = "TIMEOUT"
)

type ServiceError struct {
	Code      ServiceErrorCode
	Msg       string
	Retryable bool
	Cause     error
}

func (e *ServiceError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil && e.Msg != "" {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Msg, e.Cause)
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Cause)
	}
	if e.Msg != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Msg)
	}
	return string(e.Code)
}

func NewServiceError(code ServiceErrorCode, msg string, retryable bool) *ServiceError {
	return &ServiceError{
		Code:      code,
		Msg:       msg,
		Retryable: retryable,
	}
}

func WrapServiceError(code ServiceErrorCode, msg string, retryable bool, cause error) *ServiceError {
	return &ServiceError{
		Code:      code,
		Msg:       msg,
		Retryable: retryable,
		Cause:     cause,
	}
}

func AsServiceError(err error) (*ServiceError, bool) {
	if err == nil {
		return nil, false
	}
	var serr *ServiceError
	if errors.As(err, &serr) {
		return serr, true
	}
	return nil, false
}

func ServiceErrorCodeOf(err error) ServiceErrorCode {
	if serr, ok := AsServiceError(err); ok && serr != nil {
		return serr.Code
	}
	return ""
}
