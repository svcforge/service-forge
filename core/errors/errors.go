package errors

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Code string

const (
	CodeOK                 Code = "OK"
	CodeInvalidArgument    Code = "INVALID_ARGUMENT"
	CodeUnauthenticated    Code = "UNAUTHENTICATED"
	CodePermissionDenied   Code = "PERMISSION_DENIED"
	CodeNotFound           Code = "NOT_FOUND"
	CodeConflict           Code = "CONFLICT"
	CodeRateLimited        Code = "RATE_LIMITED"
	CodeUnavailable        Code = "UNAVAILABLE"
	CodeDeadlineExceeded   Code = "DEADLINE_EXCEEDED"
	CodeFailedPrecondition Code = "FAILED_PRECONDITION"
	CodeInternal           Code = "INTERNAL"
)

type AppError struct {
	Code       Code           `json:"code"`
	Message    string         `json:"message"`
	Details    map[string]any `json:"details,omitempty"`
	Cause      error          `json:"-"`
	HTTPStatus int            `json:"-"`
	GRPCCode   codes.Code     `json:"-"`
}

func (e *AppError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error {
	return e.Cause
}

func (e *AppError) WithCause(err error) *AppError {
	e.Cause = err
	return e
}

func (e *AppError) WithDetails(key string, value any) *AppError {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	e.Details[key] = value
	return e
}

var (
	mu      sync.RWMutex
	httpMap = map[Code]int{
		CodeOK:                 http.StatusOK,
		CodeInvalidArgument:    http.StatusBadRequest,
		CodeUnauthenticated:    http.StatusUnauthorized,
		CodePermissionDenied:   http.StatusForbidden,
		CodeNotFound:           http.StatusNotFound,
		CodeConflict:           http.StatusConflict,
		CodeRateLimited:        http.StatusTooManyRequests,
		CodeUnavailable:        http.StatusServiceUnavailable,
		CodeDeadlineExceeded:   http.StatusGatewayTimeout,
		CodeFailedPrecondition: http.StatusPreconditionFailed,
		CodeInternal:           http.StatusInternalServerError,
	}
	grpcMap = map[Code]codes.Code{
		CodeOK:                 codes.OK,
		CodeInvalidArgument:    codes.InvalidArgument,
		CodeUnauthenticated:    codes.Unauthenticated,
		CodePermissionDenied:   codes.PermissionDenied,
		CodeNotFound:           codes.NotFound,
		CodeConflict:           codes.AlreadyExists,
		CodeRateLimited:        codes.ResourceExhausted,
		CodeUnavailable:        codes.Unavailable,
		CodeDeadlineExceeded:   codes.DeadlineExceeded,
		CodeFailedPrecondition: codes.FailedPrecondition,
		CodeInternal:           codes.Internal,
	}
)

func RegisterHTTPStatus(code Code, status int) {
	mu.Lock()
	defer mu.Unlock()
	httpMap[code] = status
}

func RegisterGRPCCode(code Code, grpcCode codes.Code) {
	mu.Lock()
	defer mu.Unlock()
	grpcMap[code] = grpcCode
}

func New(code Code, message string) *AppError {
	return &AppError{
		Code:       code,
		Message:    message,
		HTTPStatus: HTTPStatus(code),
		GRPCCode:   GRPCCode(code),
	}
}

func Wrap(err error, code Code, message string) *AppError {
	if err == nil {
		return nil
	}
	return New(code, message).WithCause(err)
}

func HTTPStatus(code Code) int {
	mu.RLock()
	defer mu.RUnlock()
	if status, ok := httpMap[code]; ok {
		return status
	}
	return http.StatusInternalServerError
}

func GRPCCode(code Code) codes.Code {
	mu.RLock()
	defer mu.RUnlock()
	if grpcCode, ok := grpcMap[code]; ok {
		return grpcCode
	}
	return codes.Internal
}

func ToGRPCError(err error) error {
	if err == nil {
		return nil
	}
	if appErr, ok := err.(*AppError); ok {
		return status.Error(appErr.GRPCCode, appErr.Message)
	}
	return status.Error(codes.Internal, err.Error())
}

func FromGRPCError(err error) *AppError {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return New(CodeInternal, err.Error())
	}
	code := CodeInternal
	switch st.Code() {
	case codes.InvalidArgument:
		code = CodeInvalidArgument
	case codes.Unauthenticated:
		code = CodeUnauthenticated
	case codes.PermissionDenied:
		code = CodePermissionDenied
	case codes.NotFound:
		code = CodeNotFound
	case codes.AlreadyExists:
		code = CodeConflict
	case codes.ResourceExhausted:
		code = CodeRateLimited
	case codes.Unavailable:
		code = CodeUnavailable
	case codes.DeadlineExceeded:
		code = CodeDeadlineExceeded
	case codes.FailedPrecondition:
		code = CodeFailedPrecondition
	}
	return New(code, st.Message())
}

type Response struct {
	Code      Code           `json:"code"`
	Message   string         `json:"message"`
	Data      any            `json:"data,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
	Timestamp int64          `json:"timestamp"`
}

func Success(data any) Response {
	return Response{
		Code:      CodeOK,
		Message:   "ok",
		Data:      data,
		Timestamp: time.Now().Unix(),
	}
}

func Failure(err error) Response {
	appErr, ok := err.(*AppError)
	if !ok {
		appErr = New(CodeInternal, err.Error())
	}
	return Response{
		Code:      appErr.Code,
		Message:   appErr.Message,
		Details:   appErr.Details,
		Timestamp: time.Now().Unix(),
	}
}
