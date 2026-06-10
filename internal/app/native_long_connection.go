package app

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
)

const (
	longConnectionChatdAttemptTimeout = 20 * time.Second
	longConnectionChatdOpenTimeout    = 45 * time.Second
)

type longConnectionNativeEngine struct {
	*NativeEngine

	mu          sync.Mutex
	session     *chatdSession
	input       EngineMessageInput
	pending     []chatdReceivedItem
	pendingUp   chatdSessionUpdate
	closed      bool
	fallback    *NativeEngine
	release     func()
	releaseOnce sync.Once
}

type longConnectionNativeEngineOptions struct {
	Release  func()
	Fallback *NativeEngine
	Input    EngineMessageInput
}

var longConnectionProxySessionFallbackLogs = proxyLogLimiter{last: map[string]time.Time{}}

func newLongConnectionNativeEngine(engine *NativeEngine, opts longConnectionNativeEngineOptions) *longConnectionNativeEngine {
	cleanup := opts.Release
	if cleanup == nil {
		cleanup = func() {}
	}
	return &longConnectionNativeEngine{NativeEngine: engine, fallback: opts.Fallback, input: opts.Input, release: cleanup}
}

func (e *longConnectionNativeEngine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	err := e.closeLocked()
	e.releaseProxyRoute()
	return err
}

func (e *longConnectionNativeEngine) ReceiveMessageBatch(ctx context.Context, input EngineMessageInput) EngineMessageBatchResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return EngineMessageBatchResult{Err: NewError(waappv1.WaErrorCode_WA_ERROR_CODE_CONFLICT, "WA long connection runner is closed", true)}
	}
	if input.MessageSessionID != "" {
		e.input = input
	}

	session, err := e.ensureSessionWithTimeoutLocked(ctx, input)
	if err != nil {
		e.closeLocked()
		return EngineMessageBatchResult{Err: chatdReceiveError(err)}
	}
	now := e.clock.Now()
	messages, payloads, update, drained := e.drainPendingLocked(input)
	if !drained {
		messages, payloads, update, err = session.receiveBatch(input, now)
		if err != nil {
			e.closeLocked()
			session, retryErr := e.ensureSessionWithTimeoutLocked(ctx, input)
			if retryErr != nil {
				return EngineMessageBatchResult{Err: chatdReceiveError(retryErr)}
			}
			now = e.clock.Now()
			messages, payloads, update, err = session.receiveBatch(input, now)
			if err != nil {
				e.closeLocked()
				return EngineMessageBatchResult{Err: chatdReceiveError(err)}
			}
		}
	}
	if len(payloads) > 0 || len(update.ContactHints) > 0 || update.RoutingInfo != "" || update.Endpoint.Host != "" || update.ServerStaticPublic != "" {
		state, err := e.loadState(ctx, input.ClientProfileID)
		if err != nil {
			e.closeLocked()
			return EngineMessageBatchResult{Err: err}
		}
		if applyChatdReceiveState(&state, input, payloads, update) {
			if err := e.saveState(ctx, input.ClientProfileID, state); err != nil {
				e.closeLocked()
				return EngineMessageBatchResult{Err: err}
			}
		}
	}
	return EngineMessageBatchResult{Messages: messages, Contacts: contactsFromContactHints(input.WAAccountID, nil, update.ContactHints, now)}
}

func (e *longConnectionNativeEngine) ResolveContactProfilePicture(ctx context.Context, input EngineContactProfilePictureInput) EngineContactProfilePictureResult {
	if e == nil || e.NativeEngine == nil {
		return EngineContactProfilePictureResult{Err: NewError(waappv1.WaErrorCode_WA_ERROR_CODE_INTERNAL, "native engine is required", false)}
	}
	return e.NativeEngine.resolveContactProfilePictureWithSender(ctx, input, e)
}

func (e *longConnectionNativeEngine) sendIQ(ctx context.Context, state nativeState, registeredIdentityID string, appVersion string, request chatdNode, timeoutMessage string) (chatdNode, chatdSessionUpdate, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return chatdNode{}, chatdSessionUpdate{}, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_CONFLICT, "WA long connection runner is closed", true)
	}
	input := e.input
	if input.RegisteredIdentityID == "" {
		input.RegisteredIdentityID = registeredIdentityID
	}
	session, err := e.ensureSessionForIQLocked(ctx, input, state)
	if err != nil {
		e.closeLocked()
		return chatdNode{}, chatdSessionUpdate{}, err
	}
	response, items, update, err := session.sendIQ(ctx, input, request, contextBoundTimeout(ctx, defaultContactProfilePictureTimeout), timeoutMessage)
	e.bufferPendingLocked(items, update)
	if err != nil {
		e.closeLocked()
		return chatdNode{}, update, err
	}
	return response, update, nil
}

func (e *longConnectionNativeEngine) ensureSessionForIQLocked(ctx context.Context, input EngineMessageInput, state nativeState) (*chatdSession, error) {
	if e.session != nil {
		return e.session, nil
	}
	if input.ClientProfileID != "" {
		openCtx, cancel := context.WithTimeout(ctx, longConnectionChatdOpenTimeout)
		defer cancel()
		return e.ensureSessionLocked(openCtx, input)
	}
	openCtx, cancel := context.WithTimeout(ctx, longConnectionChatdOpenTimeout)
	defer cancel()
	session, err := e.openSessionWithEngine(openCtx, e.NativeEngine, input, state)
	if err == nil {
		e.session = session
		return session, nil
	}
	if reason := longConnectionProxySessionFallbackReason(err); reason != "" && e.fallback != nil {
		if session, fallbackErr := e.openSessionWithEngine(openCtx, e.fallback, input, state); fallbackErr == nil {
			e.releaseProxyRoute()
			e.NativeEngine = e.fallback
			e.fallback = nil
			e.session = session
			logLongConnectionProxySessionFallback(reason)
			return session, nil
		}
	}
	return nil, err
}

func (e *longConnectionNativeEngine) drainPendingLocked(input EngineMessageInput) ([]*waappv1.InboundMessage, []chatdEncPayload, chatdSessionUpdate, bool) {
	if len(e.pending) == 0 && !hasChatdSessionUpdate(e.pendingUp) {
		return nil, nil, chatdSessionUpdate{}, false
	}
	limit := input.MaxMessages
	if limit <= 0 {
		limit = 10
	}
	count := len(e.pending)
	if count > limit {
		count = limit
	}
	items := append([]chatdReceivedItem(nil), e.pending[:count]...)
	e.pending = append([]chatdReceivedItem(nil), e.pending[count:]...)
	update := e.pendingUp
	e.pendingUp = chatdSessionUpdate{}
	messages, payloads := splitReceivedItems(items)
	return messages, payloads, update, true
}

func (e *longConnectionNativeEngine) bufferPendingLocked(items []chatdReceivedItem, update chatdSessionUpdate) {
	if len(items) == 0 && len(update.ContactHints) == 0 {
		return
	}
	e.pending = append(e.pending, items...)
	e.pendingUp = mergeChatdSessionUpdate(e.pendingUp, update)
}

func contextBoundTimeout(ctx context.Context, fallback time.Duration) time.Duration {
	if fallback <= 0 {
		fallback = defaultChatdReadWindow
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 && remaining < fallback {
			return remaining
		}
	}
	return fallback
}

func (e *longConnectionNativeEngine) ensureSessionWithTimeoutLocked(ctx context.Context, input EngineMessageInput) (*chatdSession, error) {
	if e.session != nil {
		return e.session, nil
	}
	openCtx, cancel := context.WithTimeout(ctx, longConnectionChatdOpenTimeout)
	defer cancel()
	return e.ensureSessionLocked(openCtx, input)
}

func (e *longConnectionNativeEngine) ensureSessionLocked(ctx context.Context, input EngineMessageInput) (*chatdSession, error) {
	if e.session != nil {
		return e.session, nil
	}
	state, err := e.loadState(ctx, input.ClientProfileID)
	if err != nil {
		return nil, err
	}
	state.ensureMaps()
	if state.ChatStatic.Private == "" || state.ChatStatic.Public == "" {
		state.ChatStatic = ensureChatStatic(state.ChatStatic)
		if err := e.saveState(ctx, input.ClientProfileID, state); err != nil {
			return nil, err
		}
	}
	session, err := e.openSessionWithEngine(ctx, e.NativeEngine, input, state)
	if err == nil {
		e.session = session
		return session, nil
	}
	if reason := longConnectionProxySessionFallbackReason(err); reason != "" && e.fallback != nil {
		if session, fallbackErr := e.openSessionWithEngine(ctx, e.fallback, input, state); fallbackErr == nil {
			e.releaseProxyRoute()
			e.NativeEngine = e.fallback
			e.fallback = nil
			e.session = session
			logLongConnectionProxySessionFallback(reason)
			return session, nil
		}
	}
	return nil, err
}

func (e *longConnectionNativeEngine) openSessionWithEngine(ctx context.Context, engine *NativeEngine, input EngineMessageInput, state nativeState) (*chatdSession, error) {
	if engine == nil {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_INTERNAL, "native engine is required", false)
	}
	proxyURL, err := engine.proxyURL()
	if err != nil {
		return nil, err
	}
	cfg := chatdConfigForState(proxyURL, state, longConnectionChatdAttemptTimeout)
	cfg.Endpoints = longConnectionChatdEndpoints(state)
	client := newChatdClient(cfg)
	session, err := client.openSession(ctx, state, input.RegisteredIdentityID, defaultLoginPayload, defaultWAAppVersion)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func (e *longConnectionNativeEngine) releaseProxyRoute() {
	if e == nil {
		return
	}
	e.releaseOnce.Do(e.release)
}

func longConnectionProxySessionFallbackReason(err error) string {
	if err == nil {
		return ""
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "connection reset by peer"):
		return "connection_reset"
	case strings.Contains(text, "i/o timeout") || strings.Contains(text, "deadline") || strings.Contains(text, "timeout"):
		return "timeout"
	case strings.Contains(text, "socks5"):
		return "socks5_failed"
	case strings.Contains(text, "proxy"):
		return "proxy_failed"
	case strings.Contains(text, "connection refused"):
		return "connection_refused"
	case strings.Contains(text, "eof"):
		return "eof"
	default:
		return ""
	}
}

func logLongConnectionProxySessionFallback(reason string) {
	reason = safeProxyLogToken(reason, "session_failed")
	if !longConnectionProxySessionFallbackLogs.allow("wa_long_connection_session", reason, time.Now().UTC()) {
		return
	}
	log.Printf("WA long connection proxy session fallback reason=%s", reason)
}

func longConnectionChatdEndpoints(state nativeState) []chatdEndpoint {
	endpoints := []chatdEndpoint{}
	seen := map[string]struct{}{}
	add := func(host string, port int) {
		endpoint := chatdEndpoint{Host: host, Port: port}.normalized(defaultChatdHost, defaultChatdPort)
		if endpoint.Host == "" || endpoint.Port != defaultChatdPort {
			return
		}
		key := endpoint.address()
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		endpoints = append(endpoints, endpoint)
	}
	if state.ChatConnection.LastHost != "" {
		add(state.ChatConnection.LastHost, state.ChatConnection.LastPort)
	}
	add(defaultChatdHost, defaultChatdPort)
	add(chatdFallbackHost, defaultChatdPort)
	return endpoints
}

func (e *longConnectionNativeEngine) closeLocked() error {
	if e.session == nil {
		return nil
	}
	err := e.session.Close()
	e.session = nil
	return err
}

var _ ProtocolEngine = (*longConnectionNativeEngine)(nil)
var _ interface{ Close() error } = (*longConnectionNativeEngine)(nil)
