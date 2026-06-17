package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/core/config"
	sferrors "github.com/svcforge/service-forge/core/errors"
	"github.com/svcforge/service-forge/transport/grpcclient"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type proxyRoute struct {
	cfg        config.GatewayRouteConfig
	fullRPC    string
	rpcService string
	invoker     ProxyInvoker
	streamCodec *streamCodec
	retry       *retryPolicy
	breaker     *circuitBreaker
	balancer    balancer
	claims      *claimForwarder
	conns       []*grpc.ClientConn
}

type grpcProxy struct {
	dialer *grpcclient.Dialer
}

type ProxyInvoker func(ctx context.Context, c *fiber.Ctx, conn *grpc.ClientConn) (any, error)

type handledResponse struct{}

var proxyInvokers sync.Map

func RegisterProxyInvoker(rpc string, invoker ProxyInvoker) error {
	if invoker == nil {
		return fmt.Errorf("proxy invoker is required")
	}
	fullRPC, _, err := normalizeRPC(rpc)
	if err != nil {
		return err
	}
	proxyInvokers.Store(fullRPC, invoker)
	return nil
}

func MustRegisterProxyInvoker(rpc string, invoker ProxyInvoker) {
	if err := RegisterProxyInvoker(rpc, invoker); err != nil {
		panic(err)
	}
}

func NewUnaryProxy[Req proto.Message, Resp proto.Message](
	newRequest func() Req,
	call func(context.Context, *grpc.ClientConn, Req) (Resp, error),
) ProxyInvoker {
	return func(ctx context.Context, c *fiber.Ctx, conn *grpc.ClientConn) (any, error) {
		req := newRequest()
		if err := bindProtoRequest(c, req); err != nil {
			return nil, err
		}
		resp, err := call(ctx, conn, req)
		if err != nil {
			return nil, err
		}
		if err := writeProtoSuccess(c, resp); err != nil {
			return nil, err
		}
		return handledResponse{}, nil
	}
}

func NewUnaryInvokeProxy[Req proto.Message, Resp proto.Message](
	rpc string,
	newRequest func() Req,
	newResponse func() Resp,
) ProxyInvoker {
	fullRPC, _, err := normalizeRPC(rpc)
	if err != nil {
		return func(context.Context, *fiber.Ctx, *grpc.ClientConn) (any, error) {
			return nil, err
		}
	}
	reqPool := sync.Pool{New: func() any { return newRequest() }}
	respPool := sync.Pool{New: func() any { return newResponse() }}
	return func(ctx context.Context, c *fiber.Ctx, conn *grpc.ClientConn) (any, error) {
		req := reqPool.Get().(Req)
		defer func() {
			proto.Reset(req)
			reqPool.Put(req)
		}()
		if err := bindProtoRequest(c, req); err != nil {
			return nil, err
		}
		resp := respPool.Get().(Resp)
		defer func() {
			proto.Reset(resp)
			respPool.Put(resp)
		}()
		if err := conn.Invoke(ctx, fullRPC, req, resp); err != nil {
			return nil, err
		}
		if err := writeProtoSuccess(c, resp); err != nil {
			return nil, err
		}
		return handledResponse{}, nil
	}
}

func NewUnaryCodecProxy[Req proto.Message, Resp proto.Message](
	newRequest func() Req,
	newResponse func() Resp,
	bind func(*fiber.Ctx, Req) error,
	invoke func(context.Context, *grpc.ClientConn, Req, Resp) error,
	write func(*fiber.Ctx, Resp) error,
) ProxyInvoker {
	reqPool := sync.Pool{New: func() any { return newRequest() }}
	respPool := sync.Pool{New: func() any { return newResponse() }}
	return func(ctx context.Context, c *fiber.Ctx, conn *grpc.ClientConn) (any, error) {
		req := reqPool.Get().(Req)
		defer func() {
			proto.Reset(req)
			reqPool.Put(req)
		}()
		if bind != nil {
			if err := bind(c, req); err != nil {
				return nil, err
			}
		}

		resp := respPool.Get().(Resp)
		defer func() {
			proto.Reset(resp)
			respPool.Put(resp)
		}()
		if err := invoke(ctx, conn, req, resp); err != nil {
			return nil, err
		}
		if err := write(c, resp); err != nil {
			return nil, err
		}
		return handledResponse{}, nil
	}
}

func newGRPCProxy(dialer *grpcclient.Dialer) *grpcProxy {
	return &grpcProxy{dialer: dialer}
}

func newProxyRoute(cfg config.GatewayRouteConfig) (*proxyRoute, error) {
	if strings.TrimSpace(cfg.Path) == "" {
		return nil, fmt.Errorf("gateway route path is required")
	}
	if strings.TrimSpace(cfg.RPC) == "" {
		return nil, fmt.Errorf("gateway route rpc is required")
	}
	fullRPC, service, err := normalizeRPC(cfg.RPC)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Service) == "" && strings.TrimSpace(cfg.Target) == "" {
		return nil, fmt.Errorf("gateway route %s must set service or target", routeLabel(cfg))
	}
	if strings.TrimSpace(cfg.Method) == "" {
		if strings.TrimSpace(cfg.Stream) != "" {
			cfg.Method = fiber.MethodGet
		} else {
			cfg.Method = fiber.MethodPost
		}
	}
	return &proxyRoute{
		cfg:        cfg,
		fullRPC:    fullRPC,
		rpcService: service,
		retry:      buildRetryPolicy(cfg.Retry),
		breaker:    newCircuitBreaker(cfg.CircuitBreaker),
		balancer:   buildBalancer(cfg.LoadBalance),
		claims:     buildClaimForwarder(cfg.ForwardClaims),
	}, nil
}

func (p *grpcProxy) Invoke(ctx context.Context, c *fiber.Ctx, route *proxyRoute) (any, error) {
	if route.breaker == nil {
		return p.invokeWithRetry(ctx, c, route)
	}

	gen, allowed := route.breaker.beforeRequest()
	if !allowed {
		return nil, sferrors.New(sferrors.CodeUnavailable, "circuit breaker open").
			WithDetails("rpc", route.fullRPC)
	}
	result, err := p.invokeWithRetry(ctx, c, route)
	route.breaker.afterRequest(gen, !isBreakerFailure(err))
	return result, err
}

// invokeWithRetry runs a single proxy call, applying the route's retry policy
// when configured. The circuit breaker (if any) wraps this in Invoke, so a
// fully retried-then-failed call counts as one breaker failure.
func (p *grpcProxy) invokeWithRetry(ctx context.Context, c *fiber.Ctx, route *proxyRoute) (any, error) {
	if route.retry == nil {
		return p.invokeOnce(ctx, c, route)
	}

	var lastErr error
	for attempt := 0; attempt < route.retry.maxAttempts; attempt++ {
		if attempt > 0 && route.retry.backoff > 0 {
			select {
			case <-time.After(route.retry.backoff):
			case <-ctx.Done():
				return nil, sferrors.New(sferrors.CodeDeadlineExceeded, "gateway retry aborted").WithCause(ctx.Err())
			}
		}
		result, err := p.invokeOnce(ctx, c, route)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !route.retry.retryable(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// invokeOnce performs a single proxy attempt: it applies the per-attempt
// timeout, selects (or dials) a connection, and runs the registered invoker.
// Each attempt picks a fresh connection from the pool so a retry naturally
// routes around a bad endpoint.
func (p *grpcProxy) invokeOnce(ctx context.Context, c *fiber.Ctx, route *proxyRoute) (any, error) {
	if timeout := route.attemptTimeout(); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	conn, release := route.pickConn()
	defer release()
	if conn == nil {
		var err error
		conn, err = p.dial(ctx, route.cfg)
		if err != nil {
			return nil, sferrors.New(sferrors.CodeUnavailable, "grpc target unavailable").WithCause(err)
		}
	}
	ctx, err := route.claims.outgoing(ctx, fiberLocals(c))
	if err != nil {
		return nil, err
	}
	return route.invoker(ctx, c, conn)
}

// fiberLocals adapts a fiber request context to the locals lookup the claim
// forwarder expects.
func fiberLocals(c *fiber.Ctx) func(string) any {
	return func(key string) any { return c.Locals(key) }
}

// attemptTimeout returns the deadline applied to a single attempt: the retry
// per-try timeout when set, otherwise the route timeout.
func (r *proxyRoute) attemptTimeout() time.Duration {
	if r.retry != nil && r.retry.perTryTimeout > 0 {
		return r.retry.perTryTimeout
	}
	return r.cfg.Timeout
}

// pickConn selects a pooled connection via the route balancer and returns a
// release callback (always non-nil) to be invoked when the attempt completes.
// A nil connection means there is no pool and the caller should dial on demand.
func (r *proxyRoute) pickConn() (*grpc.ClientConn, func()) {
	switch n := len(r.conns); n {
	case 0:
		return nil, noopRelease
	case 1:
		return r.conns[0], noopRelease
	default:
		idx, release := r.balancer.pick(n)
		return r.conns[idx], release
	}
}

func lookupProxyInvoker(fullRPC string) (ProxyInvoker, bool) {
	raw, ok := proxyInvokers.Load(fullRPC)
	if !ok {
		return nil, false
	}
	invoker, ok := raw.(ProxyInvoker)
	return invoker, ok
}

func unregisterProxyInvoker(rpc string) {
	fullRPC, _, err := normalizeRPC(rpc)
	if err != nil {
		return
	}
	proxyInvokers.Delete(fullRPC)
}

func (p *grpcProxy) dial(ctx context.Context, cfg config.GatewayRouteConfig) (*grpc.ClientConn, error) {
	if strings.TrimSpace(cfg.Target) != "" {
		return p.dialer.DialTarget(ctx, cfg.Target)
	}
	return p.dialer.DialService(ctx, cfg.Service)
}

func bindProtoRequest(c *fiber.Ctx, msg proto.Message) error {
	if !requestHasPayload(c) {
		return nil
	}
	data, err := requestPayloadJSON(c)
	if err != nil {
		return err
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(data, msg); err != nil {
		return sferrors.New(sferrors.CodeInvalidArgument, "request does not match grpc input").WithCause(err)
	}
	return nil
}

func requestHasPayload(c *fiber.Ctx) bool {
	if len(bytes.TrimSpace(c.Body())) > 0 {
		return true
	}
	if c.Context().QueryArgs().Len() > 0 {
		return true
	}
	return len(c.Route().Params) > 0
}

func requestPayloadJSON(c *fiber.Ctx) ([]byte, error) {
	payload := map[string]any{}
	if body := bytes.TrimSpace(c.Body()); len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, sferrors.New(sferrors.CodeInvalidArgument, "invalid json request body").WithCause(err)
		}
	}
	if c.Context().QueryArgs().Len() > 0 {
		for key, value := range c.Queries() {
			if _, exists := payload[key]; !exists {
				payload[key] = value
			}
		}
	}
	if len(c.Route().Params) > 0 {
		for key, value := range c.AllParams() {
			if _, exists := payload[key]; !exists {
				payload[key] = value
			}
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, sferrors.New(sferrors.CodeInvalidArgument, "invalid request payload").WithCause(err)
	}
	return data, nil
}

func writeProtoSuccess(c *fiber.Ctx, msg proto.Message) error {
	data, err := (protojson.MarshalOptions{
		EmitUnpopulated: true,
		UseProtoNames:   true,
	}).Marshal(msg)
	if err != nil {
		return err
	}
	return WriteSuccessJSON(c, data)
}

func WriteSuccessJSON(c *fiber.Ctx, data []byte) error {
	out := make([]byte, 0, len(data)+72)
	out = append(out, `{"code":"OK","message":"ok","data":`...)
	out = append(out, data...)
	out = append(out, `,"timestamp":`...)
	out = strconv.AppendInt(out, time.Now().Unix(), 10)
	out = append(out, '}')

	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
	return c.Status(fiber.StatusOK).Send(out)
}

func normalizeRPC(value string) (string, string, error) {
	service, method, err := splitFullRPC(value)
	if err != nil {
		return "", "", err
	}
	return "/" + service + "/" + method, service, nil
}

func splitFullRPC(value string) (string, string, error) {
	value = strings.TrimSpace(strings.TrimPrefix(value, "/"))
	parts := strings.Split(value, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("rpc must use /package.Service/Method format: %s", value)
	}
	return parts[0], parts[1], nil
}

func routeLabel(cfg config.GatewayRouteConfig) string {
	if cfg.Name != "" {
		return cfg.Name
	}
	if cfg.Path != "" {
		return cfg.Path
	}
	return cfg.RPC
}
