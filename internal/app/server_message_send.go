package app

import (
	"context"
	"strings"
	"time"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type waTextMessageSender interface {
	SendTextMessage(context.Context, EngineTextMessageInput) EngineTextMessageResult
}

func (s *Server) SendTextMessage(ctx context.Context, req *waappv1.SendTextMessageRequest) (*waappv1.SendTextMessageResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.SendTextMessageResponse{Error: ToProtoError(err)}, nil
	}
	accountID, err := requireWAAccountID(req.GetWaAccountId())
	if err != nil {
		return &waappv1.SendTextMessageResponse{Error: ToProtoError(err)}, nil
	}
	if _, err := s.getWAAccount(ctx, accountID); err != nil {
		return &waappv1.SendTextMessageResponse{Error: ToProtoError(err)}, nil
	}
	text := strings.TrimSpace(req.GetText().GetValue())
	if text == "" {
		return &waappv1.SendTextMessageResponse{Error: ToProtoError(NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "text is required", false))}, nil
	}
	contactJID := s.textMessageContactJID(ctx, accountID, req.GetContactRef())
	if contactJID == "" {
		return &waappv1.SendTextMessageResponse{Error: ToProtoError(NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "contact_ref is required", false))}, nil
	}
	loginState, err := s.activeContactResolveLoginState(ctx, accountID)
	if err != nil {
		return &waappv1.SendTextMessageResponse{Error: ToProtoError(err)}, nil
	}
	runner, release, err := s.textMessageRunner(ctx, req.GetContext(), loginState)
	if err != nil {
		return &waappv1.SendTextMessageResponse{Error: ToProtoError(err)}, nil
	}
	defer release()
	sender, ok := runner.(waTextMessageSender)
	if !ok {
		return &waappv1.SendTextMessageResponse{Error: ToProtoError(NewError(waappv1.WaErrorCode_WA_ERROR_CODE_UNSUPPORTED_OPERATION, "WA text message sender is not configured", false))}, nil
	}
	providerID := newTextProviderMessageID(req.GetClientMessageId())
	result := sender.SendTextMessage(ctx, EngineTextMessageInput{
		WAAccountID:          accountID,
		ClientProfileID:      loginState.GetClientProfileId(),
		RegisteredIdentityID: loginState.GetRegisteredIdentityId(),
		AppVersion:           s.loginStateAppVersion(ctx, loginState),
		ContactJID:           contactJID,
		Text:                 text,
		ClientMessageID:      providerID,
		RemoteTimeout:        defaultTextMessageSendTimeout,
	})
	if result.ProviderMessageID != "" {
		providerID = result.ProviderMessageID
	}
	if result.Err != nil {
		return &waappv1.SendTextMessageResponse{ProviderMessageId: providerID, SentAt: timestamp(result.SentAt), Error: ToProtoError(result.Err)}, nil
	}
	sentAt := result.SentAt
	if sentAt.IsZero() {
		sentAt = s.clock.Now()
	}
	ackStatus := result.AckStatus
	if ackStatus == waappv1.MessageAckStatus_MESSAGE_ACK_STATUS_UNSPECIFIED {
		ackStatus = waappv1.MessageAckStatus_MESSAGE_ACK_STATUS_PENDING
	}
	if err := s.saveOutboundTextMessage(ctx, loginState, contactJID, providerID, text, sentAt, ackStatus); err != nil {
		return &waappv1.SendTextMessageResponse{ProviderMessageId: providerID, SentAt: timestamppb.New(sentAt.UTC()), Error: ToProtoError(err)}, nil
	}
	return &waappv1.SendTextMessageResponse{ProviderMessageId: providerID, SentAt: timestamppb.New(sentAt.UTC())}, nil
}

func (s *Server) textMessageContactJID(ctx context.Context, accountID string, contactRef string) string {
	contactRef = strings.TrimSpace(contactRef)
	if contactRef == "" {
		return ""
	}
	contact, err := s.store.GetWAContactByRef(ctx, accountID, contactRef)
	if err == nil && contact.GetWaAccountId() == accountID {
		if jid := normalizeWAJID(contact.GetJid()); jid != "" {
			return jid
		}
		if number := strings.TrimSpace(contact.GetNumber()); number != "" {
			return normalizeWAJID(number)
		}
	}
	return normalizeWAJID(contactRef)
}

func (s *Server) textMessageRunner(ctx context.Context, requestContext *waappv1.RequestContext, loginState *waappv1.LoginState) (ProtocolEngine, func(), error) {
	if s.longConnections != nil {
		if runner := s.longConnections.Runner(loginState); runner != nil {
			return runner, func() {}, nil
		}
	}
	runner := s.runner
	native, ok := runner.(*NativeEngine)
	if !ok {
		return runner, func() {}, nil
	}
	proxied, release, _ := s.optionalGatewayProxyEngine(ctx, native, gatewayProxyEngineRequest{
		Username:      s.longProxyUsername,
		Purpose:       "WA_MESSAGE_SEND",
		CorrelationID: firstNonEmpty(requestContext.GetCorrelationId(), requestContext.GetRequestId()),
		TTL:           defaultTextMessageSendTimeout + 10*time.Second,
		Mode:          DynamicProxySessionModeSticky,
	})
	return proxied, release, nil
}

func (s *Server) saveOutboundTextMessage(ctx context.Context, loginState *waappv1.LoginState, contactJID string, providerID string, text string, sentAt time.Time, ackStatus waappv1.MessageAckStatus) error {
	session, err := s.outboundMessageSession(ctx, loginState, sentAt)
	if err != nil {
		return err
	}
	messageID := outboundMessageID(session.GetWaAccountId(), providerID)
	message := &waappv1.InboundMessage{
		MessageId:         messageID,
		MessageSessionId:  session.GetMessageSessionId(),
		Kind:              waappv1.InboundMessageKind_INBOUND_MESSAGE_KIND_MESSAGE,
		EncryptionState:   waappv1.MessageEncryptionState_MESSAGE_ENCRYPTION_STATE_DECRYPTED,
		AckStatus:         ackStatus,
		ContactRef:        contactJID,
		PayloadRef:        "outbound:" + providerID,
		ProviderMessageId: providerID,
		ProviderTimestamp: timestamppb.New(sentAt.UTC()),
		ReceivedAt:        timestamppb.New(sentAt.UTC()),
		DeleteStatus:      waappv1.MessageDeleteStatus_MESSAGE_DELETE_STATUS_NOT_DELETED,
		Direction:         waappv1.AccountMessageDirection_ACCOUNT_MESSAGE_DIRECTION_OUTBOUND,
		Source:            waappv1.AccountMessageSource_ACCOUNT_MESSAGE_SOURCE_LOCAL_SEND,
	}
	if err := s.saveInboundMessagesForSession(ctx, session, []*waappv1.InboundMessage{message}); err != nil {
		return err
	}
	return s.store.SaveDecryptedMessage(ctx, &waappv1.DecryptedMessage{
		DecryptedMessageId: "wadec_" + stableID(messageID+":outbound"),
		MessageId:          messageID,
		Status:             waappv1.DecryptionStatus_DECRYPTION_STATUS_DECRYPTED,
		PlaintextRef:       "outbound:" + messageID,
		PlaintextText:      sensitiveOutput(text, "outbound-message", true),
		DecryptedAt:        timestamppb.New(sentAt.UTC()),
	})
}

func (s *Server) outboundMessageSession(ctx context.Context, loginState *waappv1.LoginState, now time.Time) (*waappv1.MessageSession, error) {
	if s.longConnections != nil {
		if sessionID := s.longConnections.MessageSessionID(loginState); sessionID != "" {
			if session, err := s.store.GetMessageSession(ctx, sessionID); err == nil {
				return session, nil
			}
		}
	}
	profile, err := s.store.GetClientProfile(ctx, loginState.GetClientProfileId())
	if err != nil {
		return nil, err
	}
	session := &waappv1.MessageSession{
		MessageSessionId:     s.ids.NewID("wasess_"),
		WaAccountId:          loginState.GetWaAccountId(),
		ClientProfileId:      loginState.GetClientProfileId(),
		RegisteredIdentityId: loginState.GetRegisteredIdentityId(),
		ProtocolProfileId:    profile.GetProtocolProfileId(),
		Status:               waappv1.MessageSessionStatus_MESSAGE_SESSION_STATUS_CLOSED,
		OpenedAt:             timestamppb.New(now.UTC()),
		LastSeenAt:           timestamppb.New(now.UTC()),
		ClosedAt:             timestamppb.New(now.UTC()),
	}
	if err := s.store.SaveMessageSession(ctx, session); err != nil {
		return nil, err
	}
	return session, nil
}

func outboundMessageID(accountID string, providerID string) string {
	return "wamsg_" + stableID(strings.Join([]string{accountID, providerID, "outbound"}, ":"))
}
