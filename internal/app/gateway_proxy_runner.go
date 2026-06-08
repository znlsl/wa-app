package app

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
)

type gatewayProxyEngineRequest struct {
	Username      string
	Purpose       string
	CorrelationID string
	TTL           time.Duration
	Mode          DynamicProxySessionMode
}

const optionalGatewayProxyFallbackLogInterval = 10 * time.Minute

var optionalGatewayProxyFallbackLogs = proxyFallbackLogLimiter{last: map[string]time.Time{}}

type proxyFallbackLogLimiter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (s *Server) optionalGatewayProxyEngine(ctx context.Context, native *NativeEngine, req gatewayProxyEngineRequest) (*NativeEngine, func(), bool) {
	if native == nil || strings.TrimSpace(native.activeProxyURL) != "" || s == nil {
		return native, func() {}, false
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		return native, func() {}, false
	}
	if s.proxyRuntime == nil {
		logOptionalGatewayProxyFallback(req, "runtime_not_configured")
		return native, func() {}, false
	}
	route, err := s.proxyRuntime.GatewayProxyRoute(ctx, username, DynamicProxyRouteRequest{
		Purpose:       req.Purpose,
		CorrelationID: req.CorrelationID,
		TTL:           req.TTL,
		Mode:          req.Mode,
	})
	if err != nil {
		logOptionalGatewayProxyFallback(req, optionalGatewayProxyFallbackReason(err))
		return native, func() {}, false
	}
	proxied, err := native.WithProxyURL(route.ProxyURL)
	if err != nil {
		logOptionalGatewayProxyFallback(req, "invalid_proxy_route")
		_ = s.proxyRuntime.ReleaseProxyRoute(context.Background(), route)
		return native, func() {}, false
	}
	return proxied, func() { _ = s.proxyRuntime.ReleaseProxyRoute(context.Background(), route) }, true
}

func logOptionalGatewayProxyFallback(req gatewayProxyEngineRequest, reason string) {
	purpose := safeProxyLogToken(req.Purpose, "unknown")
	reason = safeProxyLogToken(reason, "fallback")
	if !optionalGatewayProxyFallbackLogs.allow(purpose, reason, time.Now().UTC()) {
		return
	}
	log.Printf("WA optional gateway proxy fallback purpose=%s mode=%s reason=%s", purpose, safeProxyLogToken(string(req.Mode), "default"), reason)
}

func (l *proxyFallbackLogLimiter) allow(purpose string, reason string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := purpose + ":" + reason
	if last, ok := l.last[key]; ok && now.Sub(last) < optionalGatewayProxyFallbackLogInterval {
		return false
	}
	l.last[key] = now
	return true
}

func optionalGatewayProxyFallbackReason(err error) string {
	if err == nil {
		return "unknown"
	}
	if errors.Is(err, context.Canceled) {
		return "context_canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		switch appErr.Code {
		case waappv1.WaErrorCode_WA_ERROR_CODE_ROUTE_UNAVAILABLE:
			return "route_unavailable"
		case waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED:
			return "validation_failed"
		default:
			if appErr.Retryable {
				return "retryable_runtime_error"
			}
			return "runtime_error"
		}
	}
	return "runtime_error"
}

func safeProxyLogToken(value string, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
			out.WriteRune(char)
		case char >= '0' && char <= '9':
			out.WriteRune(char)
		case char == '_' || char == '-':
			out.WriteRune(char)
		}
	}
	token := strings.Trim(out.String(), "_-")
	if token == "" {
		return fallback
	}
	if len(token) > 64 {
		return token[:64]
	}
	return token
}
