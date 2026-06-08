package app

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	longConnectionWaitTimeout          = 25 * time.Second
	longConnectionMaxBackoff           = 30 * time.Second
	longConnectionLeaseTTL             = 90 * time.Second
	longConnectionLeaseRenewInterval   = 30 * time.Second
	longConnectionLeaseWaitLogInterval = 20
	longConnectionDecryptLimit         = 100
	staleMessageSessionTTL             = 10 * time.Minute
	staleMessageSessionCleanupInterval = 5 * time.Minute
)

const (
	longConnectionLeaseUnavailableMessage = "WA long connection lease is held by another worker"
	longConnectionLeaseLostMessage        = "WA long connection lease was lost"
	longConnectionLeaseOperationMessage   = "WA long connection lease operation failed"
)

type LongConnectionManager struct {
	server *Server

	mu      sync.Mutex
	rootCtx context.Context
	cancel  context.CancelFunc
	entries map[string]*longConnectionEntry
}

type longConnectionEntry struct {
	cancel   context.CancelFunc
	snapshot *waappv1.LongConnectionState
}

func NewLongConnectionManager(server *Server) *LongConnectionManager {
	return &LongConnectionManager{server: server, entries: map[string]*longConnectionEntry{}}
}

func (m *LongConnectionManager) Run(ctx context.Context) error {
	if m == nil || m.server == nil {
		return nil
	}
	rootCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.rootCtx = rootCtx
	m.cancel = cancel
	m.mu.Unlock()
	defer func() {
		cancel()
		m.stopAll()
	}()
	if err := m.restore(rootCtx); err != nil {
		return err
	}
	m.closeStaleMessageSessions(rootCtx)
	go m.cleanupStaleMessageSessions(rootCtx)
	<-rootCtx.Done()
	return nil
}

func (m *LongConnectionManager) Ensure(ctx context.Context, loginState *waappv1.LoginState) {
	if m == nil || loginState == nil || loginState.GetStatus() != waappv1.LoginStateStatus_LOGIN_STATE_STATUS_ACTIVE {
		return
	}
	m.mu.Lock()
	rootCtx := m.rootCtx
	if rootCtx == nil {
		m.mu.Unlock()
		return
	}
	key := longConnectionKey(loginState)
	if existing, ok := m.entries[key]; ok && existing.cancel != nil {
		m.mu.Unlock()
		return
	}
	entryCtx, cancel := context.WithCancel(rootCtx)
	snapshot := &waappv1.LongConnectionState{
		LoginStateId:         loginState.GetLoginStateId(),
		WaAccountId:          loginState.GetWaAccountId(),
		ClientProfileId:      loginState.GetClientProfileId(),
		RegisteredIdentityId: loginState.GetRegisteredIdentityId(),
		Status:               waappv1.LongConnectionStatus_LONG_CONNECTION_STATUS_STARTING,
		HeartbeatSupported:   true,
		StartedAt:            timestamppb.New(m.server.clock.Now()),
	}
	m.entries[key] = &longConnectionEntry{cancel: cancel, snapshot: snapshot}
	m.mu.Unlock()
	go m.runEntry(entryCtx, proto.Clone(loginState).(*waappv1.LoginState), key)
	_ = ctx
}

func (m *LongConnectionManager) Snapshots(req *waappv1.GetLongConnectionStatusRequest) []*waappv1.LongConnectionState {
	if m == nil || req == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*waappv1.LongConnectionState{}
	for _, entry := range m.entries {
		if entry == nil || entry.snapshot == nil {
			continue
		}
		s := entry.snapshot
		if req.GetLoginStateId() != "" && s.GetLoginStateId() != req.GetLoginStateId() {
			continue
		}
		if req.GetRegisteredIdentityId() != "" && s.GetRegisteredIdentityId() != req.GetRegisteredIdentityId() {
			continue
		}
		if req.GetWaAccountId() != "" && s.GetWaAccountId() != req.GetWaAccountId() {
			continue
		}
		if req.GetClientProfileId() != "" && s.GetClientProfileId() != req.GetClientProfileId() {
			continue
		}
		out = append(out, proto.Clone(s).(*waappv1.LongConnectionState))
	}
	return out
}

func (m *LongConnectionManager) restore(ctx context.Context) error {
	records, err := m.server.store.ListActiveLoginStates(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if ctx.Err() != nil {
			return nil
		}
		m.Ensure(ctx, record.LoginState)
	}
	return nil
}

func (m *LongConnectionManager) cleanupStaleMessageSessions(ctx context.Context) {
	ticker := time.NewTicker(staleMessageSessionCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.closeStaleMessageSessions(ctx)
		}
	}
}

func (m *LongConnectionManager) closeStaleMessageSessions(ctx context.Context) {
	if m == nil || m.server == nil || m.server.store == nil {
		return
	}
	closed, err := m.server.store.CloseStaleOpenMessageSessions(ctx, m.server.clock.Now().Add(-staleMessageSessionTTL))
	if err != nil {
		log.Printf("WA stale message session cleanup failed: %v", sanitizeLogError(err))
		return
	}
	if closed > 0 {
		log.Printf("WA stale message session cleanup closed=%d", closed)
	}
}

func (m *LongConnectionManager) runEntry(ctx context.Context, loginState *waappv1.LoginState, key string) {
	backoff := 2 * time.Second
	reconnects := int32(0)
	leaseKey := longConnectionLeaseKey(key)
	leaseHolder := m.server.ids.NewID("walc_lease_")
	defer m.markStopped(key)
	for ctx.Err() == nil {
		m.update(key, func(snapshot *waappv1.LongConnectionState) {
			if reconnects > 0 {
				snapshot.Status = waappv1.LongConnectionStatus_LONG_CONNECTION_STATUS_RECONNECTING
			} else {
				snapshot.Status = waappv1.LongConnectionStatus_LONG_CONNECTION_STATUS_STARTING
			}
			snapshot.ReconnectCount = reconnects
		})
		claimed, err := m.claimLongConnectionLease(ctx, leaseKey, leaseHolder)
		if err != nil {
			m.recordLoopError(key, reconnects, err)
			if !sleepContext(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			reconnects++
			continue
		}
		if !claimed {
			m.recordLoopError(key, reconnects, longConnectionLeaseUnavailableError())
			if !sleepContext(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			reconnects++
			continue
		}
		connectionCtx, stopLeaseRenewal, leaseLost := m.startLongConnectionLeaseRenewal(ctx, leaseKey, leaseHolder)
		session, err := m.openSession(connectionCtx, loginState)
		if err != nil {
			stopLeaseRenewal()
			m.releaseLongConnectionLease(ctx, leaseKey, leaseHolder)
			if leaseErr := readLongConnectionLeaseLoss(leaseLost); leaseErr != nil {
				err = leaseErr
			}
			m.recordLoopError(key, reconnects, err)
			if !sleepContext(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			reconnects++
			continue
		}
		m.update(key, func(snapshot *waappv1.LongConnectionState) {
			snapshot.MessageSessionId = session.GetMessageSessionId()
			snapshot.LastError = nil
		})
		runner, err := m.server.longConnectionRunner(connectionCtx, loginState)
		if err != nil {
			stopLeaseRenewal()
			if leaseErr := readLongConnectionLeaseLoss(leaseLost); leaseErr != nil {
				err = leaseErr
			}
			m.recordLoopError(key, reconnects, err)
			_, _ = m.server.CloseMessageSession(context.WithoutCancel(ctx), &waappv1.CloseMessageSessionRequest{Context: &waappv1.RequestContext{}, MessageSessionId: session.GetMessageSessionId(), Reason: "long connection runner unavailable"})
			m.releaseLongConnectionLease(ctx, leaseKey, leaseHolder)
			if !sleepContext(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			reconnects++
			continue
		}
		m.decryptPendingMessages(connectionCtx, session, runner)
		receivedHeartbeat := false
		for connectionCtx.Err() == nil {
			if err := readLongConnectionLeaseLoss(leaseLost); err != nil {
				m.recordLoopError(key, reconnects, err)
				break
			}
			resp, err := m.server.receiveMessageBatch(connectionCtx, &waappv1.ReceiveMessageBatchRequest{Context: &waappv1.RequestContext{RequestId: m.server.ids.NewID("wa-rx_"), CorrelationId: loginState.GetLoginStateId()}, MessageSessionId: session.GetMessageSessionId(), MaxMessages: 10, WaitTimeout: durationpb.New(longConnectionWaitTimeout)}, runner)
			if err != nil {
				if leaseErr := readLongConnectionLeaseLoss(leaseLost); leaseErr != nil {
					err = leaseErr
				}
				m.recordLoopError(key, reconnects, err)
				break
			}
			if resp.GetError() != nil {
				var err error = errorFromProto(resp.GetError())
				if leaseErr := readLongConnectionLeaseLoss(leaseLost); leaseErr != nil {
					err = leaseErr
				}
				m.recordLoopError(key, reconnects, err)
				break
			}
			now := m.server.clock.Now()
			messages := resp.GetMessages()
			m.update(key, func(snapshot *waappv1.LongConnectionState) {
				if snapshot.GetStatus() != waappv1.LongConnectionStatus_LONG_CONNECTION_STATUS_CONNECTED && snapshot.GetStatus() != waappv1.LongConnectionStatus_LONG_CONNECTION_STATUS_HEARTBEAT_WAITING {
					snapshot.LastConnectedAt = timestamppb.New(now)
				}
				snapshot.Status = waappv1.LongConnectionStatus_LONG_CONNECTION_STATUS_HEARTBEAT_WAITING
				snapshot.LastHeartbeatAt = timestamppb.New(now)
				snapshot.LastError = nil
				if len(messages) > 0 {
					snapshot.Status = waappv1.LongConnectionStatus_LONG_CONNECTION_STATUS_CONNECTED
					snapshot.LastMessageAt = timestamppb.New(now)
				}
			})
			receivedHeartbeat = true
			backoff = 2 * time.Second
			m.decryptReceivedMessages(connectionCtx, session, messages, runner)
		}
		if err := readLongConnectionLeaseLoss(leaseLost); err != nil {
			m.recordLoopError(key, reconnects, err)
		}
		stopLeaseRenewal()
		if ctx.Err() != nil {
			closeLongConnectionRunner(runner)
			m.releaseLongConnectionLease(ctx, leaseKey, leaseHolder)
			return
		}
		closeLongConnectionRunner(runner)
		if !receivedHeartbeat {
			backoff = nextBackoff(backoff)
		}
		reconnects++
		_, _ = m.server.CloseMessageSession(context.WithoutCancel(ctx), &waappv1.CloseMessageSessionRequest{Context: &waappv1.RequestContext{}, MessageSessionId: session.GetMessageSessionId(), Reason: "long connection reconnect"})
		m.releaseLongConnectionLease(ctx, leaseKey, leaseHolder)
		if !sleepContext(ctx, backoff) {
			return
		}
	}
}

func (m *LongConnectionManager) claimLongConnectionLease(ctx context.Context, leaseKey string, leaseHolder string) (bool, error) {
	if m == nil || m.server == nil || m.server.runtime == nil || leaseKey == "" || leaseHolder == "" {
		return true, nil
	}
	claimed, err := m.server.runtime.ClaimLease(ctx, leaseKey, leaseHolder, longConnectionLeaseTTL)
	if err != nil {
		return false, longConnectionLeaseOperationError()
	}
	return claimed, nil
}

func (m *LongConnectionManager) startLongConnectionLeaseRenewal(ctx context.Context, leaseKey string, leaseHolder string) (context.Context, context.CancelFunc, <-chan error) {
	connectionCtx, cancel := context.WithCancel(ctx)
	leaseLost := make(chan error, 1)
	if m == nil || m.server == nil || m.server.runtime == nil || leaseKey == "" || leaseHolder == "" {
		return connectionCtx, cancel, leaseLost
	}
	go func() {
		ticker := time.NewTicker(longConnectionLeaseRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-connectionCtx.Done():
				return
			case <-ticker.C:
				renewed, err := m.server.runtime.RenewLease(connectionCtx, leaseKey, leaseHolder, longConnectionLeaseTTL)
				if err != nil {
					if connectionCtx.Err() != nil {
						return
					}
					sendLongConnectionLeaseLoss(leaseLost, longConnectionLeaseOperationError())
					cancel()
					return
				}
				if !renewed {
					if connectionCtx.Err() != nil {
						return
					}
					sendLongConnectionLeaseLoss(leaseLost, longConnectionLeaseLostError())
					cancel()
					return
				}
			}
		}
	}()
	return connectionCtx, cancel, leaseLost
}

func (m *LongConnectionManager) releaseLongConnectionLease(ctx context.Context, leaseKey string, leaseHolder string) {
	if m == nil || m.server == nil || m.server.runtime == nil || leaseKey == "" || leaseHolder == "" {
		return
	}
	if err := m.server.runtime.ReleaseLease(context.WithoutCancel(ctx), leaseKey, leaseHolder); err != nil {
		log.Printf("WA long connection lease release failed: %v", sanitizeLogError(longConnectionLeaseOperationError()))
	}
}

func sendLongConnectionLeaseLoss(ch chan<- error, err error) {
	select {
	case ch <- err:
	default:
	}
}

func readLongConnectionLeaseLoss(ch <-chan error) error {
	select {
	case err := <-ch:
		return err
	default:
		return nil
	}
}

func longConnectionLeaseUnavailableError() error {
	return NewError(waappv1.WaErrorCode_WA_ERROR_CODE_CONFLICT, longConnectionLeaseUnavailableMessage, true)
}

func longConnectionLeaseLostError() error {
	return NewError(waappv1.WaErrorCode_WA_ERROR_CODE_CONFLICT, longConnectionLeaseLostMessage, true)
}

func longConnectionLeaseOperationError() error {
	return NewError(waappv1.WaErrorCode_WA_ERROR_CODE_CONFLICT, longConnectionLeaseOperationMessage, true)
}

func closeLongConnectionRunner(runner ProtocolEngine) {
	if closer, ok := runner.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}

func (m *LongConnectionManager) openSession(ctx context.Context, loginState *waappv1.LoginState) (*waappv1.MessageSession, error) {
	resp, err := m.server.OpenMessageSession(ctx, &waappv1.OpenMessageSessionRequest{
		Context:              &waappv1.RequestContext{RequestId: m.server.ids.NewID("wa-open_"), CorrelationId: loginState.GetLoginStateId()},
		WaAccountId:          loginState.GetWaAccountId(),
		ClientProfileId:      loginState.GetClientProfileId(),
		RegisteredIdentityId: loginState.GetRegisteredIdentityId(),
	})
	if err != nil {
		return nil, err
	}
	if resp.GetError() != nil {
		return nil, errorFromProto(resp.GetError())
	}
	return resp.GetSession(), nil
}

func (m *LongConnectionManager) decryptPendingMessages(ctx context.Context, session *waappv1.MessageSession, runner ProtocolEngine) {
	messages, err := m.server.store.ListPendingEncryptedInboundMessages(ctx, session.GetWaAccountId(), session.GetClientProfileId(), longConnectionDecryptLimit)
	if err != nil {
		log.Printf("WA long connection pending decrypt load failed: %v", sanitizeLogError(err))
		return
	}
	if len(messages) == 0 {
		return
	}
	log.Printf("WA long connection retry pending decrypt: count=%d", len(messages))
	m.decryptReceivedMessages(ctx, session, messages, runner)
}

func (m *LongConnectionManager) decryptReceivedMessages(ctx context.Context, session *waappv1.MessageSession, messages []*waappv1.InboundMessage, runner ProtocolEngine) {
	for _, msg := range messages {
		if msg.GetEncryptionState() == waappv1.MessageEncryptionState_MESSAGE_ENCRYPTION_STATE_PLAINTEXT && !strings.HasPrefix(msg.GetPayloadRef(), "plaintext:") {
			continue
		}
		resp, err := m.server.decryptMessage(ctx, &waappv1.DecryptMessageRequest{Context: &waappv1.RequestContext{RequestId: m.server.ids.NewID("wa-dec_"), CorrelationId: session.GetRegisteredIdentityId()}, MessageId: msg.GetMessageId(), SessionCommitPolicy: waappv1.SessionCommitPolicy_SESSION_COMMIT_POLICY_COMMIT_LEARNED_STATE, IncludeSensitivePlaintext: true}, runner, waappv1.WaOtpSource_WA_OTP_SOURCE_LONG_CONNECTION)
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("WA long connection decrypt failed: message_id=%s error=%v", msg.GetMessageId(), sanitizeLogError(err))
		}
		if resp != nil && resp.GetError() != nil {
			log.Printf("WA long connection decrypt failed: message_id=%s code=%s retryable=%t", msg.GetMessageId(), resp.GetError().GetCode().String(), resp.GetError().GetRetryable())
		}
	}
}

func (m *LongConnectionManager) recordLoopError(key string, reconnects int32, err error) {
	protoErr := ToProtoError(err)
	m.update(key, func(snapshot *waappv1.LongConnectionState) {
		snapshot.Status = waappv1.LongConnectionStatus_LONG_CONNECTION_STATUS_RECONNECTING
		snapshot.ReconnectCount = reconnects
		snapshot.LastError = protoErr
	})
	if isLongConnectionLeaseUnavailable(protoErr) {
		if reconnects == 0 || reconnects%longConnectionLeaseWaitLogInterval == 0 {
			log.Printf("WA long connection lease waiting count=%d", reconnects)
		}
		return
	}
	if reconnects < 5 || reconnects%20 == 0 {
		log.Printf("WA long connection reconnecting count=%d code=%s retryable=%t message=%s", reconnects, protoErr.GetCode().String(), protoErr.GetRetryable(), longConnectionLogErrorMessage(protoErr.GetMessage()))
	}
}

func isLongConnectionLeaseUnavailable(err *waappv1.WaError) bool {
	if err == nil {
		return false
	}
	return err.GetCode() == waappv1.WaErrorCode_WA_ERROR_CODE_CONFLICT && err.GetMessage() == longConnectionLeaseUnavailableMessage
}

func longConnectionLogErrorMessage(message string) string {
	if strings.HasPrefix(message, "native chatd receive failed:") || strings.HasPrefix(message, "login state remote check failed:") {
		return message
	}
	return safeResponseSnippet(message)
}

func (m *LongConnectionManager) update(key string, mutate func(*waappv1.LongConnectionState)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.entries[key]
	if entry == nil || entry.snapshot == nil {
		return
	}
	mutate(entry.snapshot)
}

func (m *LongConnectionManager) markStopped(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.entries[key]
	if entry == nil || entry.snapshot == nil {
		return
	}
	entry.cancel = nil
	entry.snapshot.Status = waappv1.LongConnectionStatus_LONG_CONNECTION_STATUS_STOPPED
}

func (m *LongConnectionManager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, entry := range m.entries {
		if entry != nil && entry.cancel != nil {
			entry.cancel()
		}
	}
}

func (s *Server) ensureLongConnection(ctx context.Context, loginState *waappv1.LoginState) {
	if s != nil && s.longConnections != nil {
		s.longConnections.Ensure(ctx, loginState)
	}
}

func (s *Server) longConnectionRunner(ctx context.Context, loginState *waappv1.LoginState) (ProtocolEngine, error) {
	engine, ok := s.runner.(*NativeEngine)
	if !ok {
		return s.runner, nil
	}
	if strings.TrimSpace(engine.activeProxyURL) != "" {
		return newLongConnectionNativeEngine(engine), nil
	}
	proxyEngine, release, _ := s.optionalGatewayProxyEngine(ctx, engine, gatewayProxyEngineRequest{
		Username:      s.longProxyUsername,
		Purpose:       "WA_LONG_CONNECTION",
		CorrelationID: longConnectionProxyCorrelationID(loginState),
		TTL:           longConnectionWaitTimeout + longConnectionChatdOpenTimeout,
		Mode:          DynamicProxySessionModeSticky,
	})
	return newLongConnectionNativeEngine(proxyEngine, release), nil
}

func longConnectionProxyCorrelationID(loginState *waappv1.LoginState) string {
	if loginState == nil {
		return ""
	}
	return firstNonEmpty(loginState.GetLoginStateId(), loginState.GetWaAccountId())
}

func longConnectionLeaseKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	return "long-connection:" + stableID(key)
}

func longConnectionKey(loginState *waappv1.LoginState) string {
	return firstNonEmpty(loginState.GetRegisteredIdentityId(), loginState.GetLoginStateId())
}

func nextBackoff(current time.Duration) time.Duration {
	if current <= 0 {
		return 2 * time.Second
	}
	current *= 2
	if current > longConnectionMaxBackoff {
		return longConnectionMaxBackoff
	}
	return current
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
