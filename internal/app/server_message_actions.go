package app

import (
	"context"
	"strings"
	"time"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
)

const maxMessageActionBatchSize = 100

type waMessageReadReceiptSender interface {
	SendReadReceipts(context.Context, EngineMessageReadReceiptInput) EngineMessageReadReceiptResult
}

type messageActionRecord struct {
	message *waappv1.InboundMessage
	session *waappv1.MessageSession
}

func (s *Server) MarkAccountMessagesRead(ctx context.Context, req *waappv1.MarkAccountMessagesReadRequest) (*waappv1.MarkAccountMessagesReadResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.MarkAccountMessagesReadResponse{Error: ToProtoError(err)}, nil
	}
	accountID, err := requireWAAccountID(req.GetWaAccountId())
	if err != nil {
		return &waappv1.MarkAccountMessagesReadResponse{Error: ToProtoError(err)}, nil
	}
	if _, err := s.getWAAccount(ctx, accountID); err != nil {
		return &waappv1.MarkAccountMessagesReadResponse{Error: ToProtoError(err)}, nil
	}
	records, err := s.loadMessageReadRecords(ctx, accountID, req.GetAccountMessageIds(), req.GetContactRef())
	if err != nil {
		return &waappv1.MarkAccountMessagesReadResponse{Error: ToProtoError(err)}, nil
	}
	now := s.clock.Now()
	changed := markMessagesRead(records, now)
	if changed > 0 {
		if err := s.store.SaveInboundMessages(ctx, actionRecordMessages(records)); err != nil {
			return &waappv1.MarkAccountMessagesReadResponse{Error: ToProtoError(err)}, nil
		}
	}
	resp := &waappv1.MarkAccountMessagesReadResponse{UpdatedCount: int32(changed)}
	if req.GetLocalOnly() {
		return resp, nil
	}
	sent, sendErr := s.sendReadReceipts(ctx, req.GetContext(), accountID, records)
	resp.ReceiptSentCount = int32(sent)
	if sendErr != nil {
		resp.ReceiptError = ToProtoError(sendErr)
	}
	return resp, nil
}

func (s *Server) DeleteAccountMessages(ctx context.Context, req *waappv1.DeleteAccountMessagesRequest) (*waappv1.DeleteAccountMessagesResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.DeleteAccountMessagesResponse{Error: ToProtoError(err)}, nil
	}
	accountID, err := requireWAAccountID(req.GetWaAccountId())
	if err != nil {
		return &waappv1.DeleteAccountMessagesResponse{Error: ToProtoError(err)}, nil
	}
	if _, err := s.getWAAccount(ctx, accountID); err != nil {
		return &waappv1.DeleteAccountMessagesResponse{Error: ToProtoError(err)}, nil
	}
	mode := req.GetMode()
	if mode == waappv1.AccountMessageDeleteMode_ACCOUNT_MESSAGE_DELETE_MODE_UNSPECIFIED {
		mode = waappv1.AccountMessageDeleteMode_ACCOUNT_MESSAGE_DELETE_MODE_FOR_ME
	}
	if mode == waappv1.AccountMessageDeleteMode_ACCOUNT_MESSAGE_DELETE_MODE_FOR_EVERYONE {
		return &waappv1.DeleteAccountMessagesResponse{Error: ToProtoError(NewError(waappv1.WaErrorCode_WA_ERROR_CODE_UNSUPPORTED_OPERATION, "WA revoke requires the E2E send pipeline and is not enabled yet", false))}, nil
	}
	if mode != waappv1.AccountMessageDeleteMode_ACCOUNT_MESSAGE_DELETE_MODE_FOR_ME {
		return &waappv1.DeleteAccountMessagesResponse{Error: ToProtoError(NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "unsupported delete mode", false))}, nil
	}
	records, err := s.loadMessageActionRecords(ctx, accountID, req.GetAccountMessageIds())
	if err != nil {
		return &waappv1.DeleteAccountMessagesResponse{Error: ToProtoError(err)}, nil
	}
	changed := markMessagesDeletedForMe(records, s.clock.Now())
	if changed > 0 {
		if err := s.store.SaveInboundMessages(ctx, actionRecordMessages(records)); err != nil {
			return &waappv1.DeleteAccountMessagesResponse{Error: ToProtoError(err)}, nil
		}
	}
	return &waappv1.DeleteAccountMessagesResponse{UpdatedCount: int32(changed)}, nil
}

func (s *Server) loadMessageReadRecords(ctx context.Context, accountID string, requestedIDs []string, contactRef string) ([]messageActionRecord, error) {
	if len(normalizeActionMessageIDs(requestedIDs)) > 0 {
		return s.loadMessageActionRecords(ctx, accountID, requestedIDs)
	}
	contactRef = strings.TrimSpace(contactRef)
	if contactRef == "" {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "account_message_ids or contact_ref is required", false)
	}
	messages, err := s.store.ListUnreadInboundMessagesByContactRefs(ctx, accountID, s.resolveContactActionRefs(ctx, accountID, contactRef), maxMessageActionBatchSize)
	if err != nil {
		return nil, err
	}
	records := make([]messageActionRecord, 0, len(messages))
	for _, msg := range messages {
		records = append(records, messageActionRecord{message: msg})
	}
	return records, nil
}

func (s *Server) loadMessageActionRecords(ctx context.Context, accountID string, requestedIDs []string) ([]messageActionRecord, error) {
	ids := normalizeActionMessageIDs(requestedIDs)
	if len(ids) == 0 {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "account_message_ids is required", false)
	}
	records := make([]messageActionRecord, 0, len(ids))
	for _, id := range ids {
		msg, err := s.store.GetInboundMessage(ctx, id)
		if err != nil {
			return nil, err
		}
		session, err := s.store.GetMessageSession(ctx, msg.GetMessageSessionId())
		if err != nil {
			return nil, err
		}
		if session.GetWaAccountId() != accountID {
			return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "message does not belong to WA account", false)
		}
		records = append(records, messageActionRecord{message: msg, session: session})
	}
	return records, nil
}

func normalizeActionMessageIDs(values []string) []string {
	out := []string{}
	seen := map[string]struct{}{}
	for _, value := range values {
		id := strings.TrimSpace(value)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		if len(out) >= maxMessageActionBatchSize {
			break
		}
	}
	return out
}

func markMessagesRead(records []messageActionRecord, at time.Time) int {
	changed := 0
	for _, record := range records {
		msg := record.message
		if msg == nil || msg.GetDeleteStatus() == waappv1.MessageDeleteStatus_MESSAGE_DELETE_STATUS_DELETED_FOR_ME || msg.GetReadAt() != nil {
			continue
		}
		msg.ReadAt = timestamp(at)
		changed++
	}
	return changed
}

func markMessagesDeletedForMe(records []messageActionRecord, at time.Time) int {
	changed := 0
	for _, record := range records {
		msg := record.message
		if msg == nil || msg.GetDeleteStatus() == waappv1.MessageDeleteStatus_MESSAGE_DELETE_STATUS_DELETED_FOR_ME {
			continue
		}
		msg.DeleteStatus = waappv1.MessageDeleteStatus_MESSAGE_DELETE_STATUS_DELETED_FOR_ME
		msg.DeletedAt = timestamp(at)
		changed++
	}
	return changed
}

func actionRecordMessages(records []messageActionRecord) []*waappv1.InboundMessage {
	messages := make([]*waappv1.InboundMessage, 0, len(records))
	for _, record := range records {
		if record.message != nil {
			messages = append(messages, record.message)
		}
	}
	return messages
}

func (s *Server) sendReadReceipts(ctx context.Context, requestContext *waappv1.RequestContext, accountID string, records []messageActionRecord) (int, error) {
	receipts := readReceiptMessages(records)
	if len(receipts) == 0 {
		return 0, nil
	}
	loginState, err := s.activeContactResolveLoginState(ctx, accountID)
	if err != nil {
		return 0, err
	}
	runner, release, err := s.messageActionRunner(ctx, requestContext)
	if err != nil {
		return 0, err
	}
	defer release()
	sender, ok := runner.(waMessageReadReceiptSender)
	if !ok {
		return 0, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_UNSUPPORTED_OPERATION, "WA read receipt sender is not configured", false)
	}
	result := sender.SendReadReceipts(ctx, EngineMessageReadReceiptInput{
		WAAccountID:          accountID,
		ClientProfileID:      loginState.GetClientProfileId(),
		RegisteredIdentityID: loginState.GetRegisteredIdentityId(),
		AppVersion:           s.loginStateAppVersion(ctx, loginState),
		Messages:             receipts,
		RemoteTimeout:        defaultMessageReadReceiptTimeout,
	})
	return result.Sent, result.Err
}

func readReceiptMessages(records []messageActionRecord) []EngineMessageReadReceipt {
	out := []EngineMessageReadReceipt{}
	for _, record := range records {
		msg := record.message
		if msg == nil || msg.GetKind() != waappv1.InboundMessageKind_INBOUND_MESSAGE_KIND_MESSAGE || msg.GetDeleteStatus() == waappv1.MessageDeleteStatus_MESSAGE_DELETE_STATUS_DELETED_FOR_ME || msg.GetProviderMessageId() == "" {
			continue
		}
		chatJID := strings.TrimSpace(msg.GetContactRef())
		participantJID := strings.TrimSpace(msg.GetSenderRef())
		if chatJID == "" {
			chatJID = participantJID
		}
		if chatJID == "" {
			continue
		}
		if participantJID == chatJID {
			participantJID = ""
		}
		out = append(out, EngineMessageReadReceipt{ChatJID: chatJID, ParticipantJID: participantJID, ProviderMessageID: msg.GetProviderMessageId()})
	}
	return out
}

func (s *Server) messageActionRunner(ctx context.Context, requestContext *waappv1.RequestContext) (ProtocolEngine, func(), error) {
	runner := s.runner
	native, ok := runner.(*NativeEngine)
	if !ok {
		return runner, func() {}, nil
	}
	proxied, release, _ := s.optionalGatewayProxyEngine(ctx, native, gatewayProxyEngineRequest{
		Username:      s.longProxyUsername,
		Purpose:       "WA_MESSAGE_ACTION",
		CorrelationID: firstNonEmpty(requestContext.GetCorrelationId(), requestContext.GetRequestId()),
		TTL:           defaultMessageReadReceiptTimeout + 10*time.Second,
		Mode:          DynamicProxySessionModeSticky,
	})
	return proxied, release, nil
}
