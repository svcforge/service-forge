package gateway

import (
	"strings"
	"time"

	"github.com/svcforge/service-forge/core/config"
	sferrors "github.com/svcforge/service-forge/core/errors"
)

// defaultRetryOn lists the codes retried when a route enables retries without
// specifying RetryOn. Both are transient by nature and only surface from the
// gRPC call itself, never from request binding, so retrying them is safe.
var defaultRetryOn = []sferrors.Code{
	sferrors.CodeUnavailable,
	sferrors.CodeDeadlineExceeded,
}

// retryPolicy is the compiled, immutable form of config.RetryConfig used on the
// hot path. A nil policy means "no retries".
type retryPolicy struct {
	maxAttempts   int
	perTryTimeout time.Duration
	backoff       time.Duration
	retryOn       map[sferrors.Code]struct{}
}

// buildRetryPolicy compiles a route's retry configuration. It returns nil when
// retries are disabled (no config, or MaxAttempts <= 1) so callers can take the
// zero-overhead path.
func buildRetryPolicy(cfg *config.RetryConfig) *retryPolicy {
	if cfg == nil || cfg.MaxAttempts <= 1 {
		return nil
	}

	codesList := defaultRetryOn
	if len(cfg.RetryOn) > 0 {
		codesList = make([]sferrors.Code, 0, len(cfg.RetryOn))
		for _, raw := range cfg.RetryOn {
			if trimmed := strings.TrimSpace(raw); trimmed != "" {
				codesList = append(codesList, sferrors.Code(strings.ToUpper(trimmed)))
			}
		}
	}

	retryOn := make(map[sferrors.Code]struct{}, len(codesList))
	for _, code := range codesList {
		retryOn[code] = struct{}{}
	}

	return &retryPolicy{
		maxAttempts:   cfg.MaxAttempts,
		perTryTimeout: cfg.PerTryTimeout,
		backoff:       cfg.Backoff,
		retryOn:       retryOn,
	}
}

// retryable reports whether an attempt error is worth retrying under this
// policy. Classification uses the framework code so a transient transport
// failure (UNAVAILABLE) is retried while a client error (INVALID_ARGUMENT) is
// surfaced immediately.
func (p *retryPolicy) retryable(err error) bool {
	if err == nil {
		return false
	}
	_, ok := p.retryOn[errorCode(err)]
	return ok
}

// errorCode extracts the framework code from an error, mapping raw gRPC status
// errors when the error is not already an *AppError.
func errorCode(err error) sferrors.Code {
	if appErr, ok := err.(*sferrors.AppError); ok {
		return appErr.Code
	}
	return sferrors.FromGRPCError(err).Code
}
