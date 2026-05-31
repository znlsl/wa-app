package app

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/byte-v-forge/common-lib/eventbus"
	"github.com/byte-v-forge/common-lib/eventcatalog"
	wav1 "github.com/byte-v-forge/common-lib/gen/go/byte/v/forge/contracts/wa/v1"
	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const waPlatformEventSource = "wa-app-service"

func (s *Server) publishOTPCandidates(ctx context.Context, reqCtx *waappv1.RequestContext, workspaceID string, msg *waappv1.InboundMessage, session *waappv1.MessageSession, candidates []*waappv1.ExtractedCandidate, source wav1.WaOtpSource) {
	if s == nil || s.platformPublisher == nil || len(candidates) == 0 {
		return
	}
	if source == wav1.WaOtpSource_WA_OTP_SOURCE_UNSPECIFIED {
		source = wav1.WaOtpSource_WA_OTP_SOURCE_MANUAL_EXTRACTION
	}
	account, _ := s.getWAAccount(ctx, workspaceID, session.GetWaAccountId())
	for _, candidate := range candidates {
		if candidate.GetKind() != waappv1.CandidateKind_CANDIDATE_KIND_OTP {
			continue
		}
		otp := strings.TrimSpace(candidate.GetText().GetValue())
		if otp == "" {
			continue
		}
		receivedAtTS := firstTimestamp(candidate.GetExtractedAt(), msg.GetReceivedAt())
		receivedAt := timeFromProto(receivedAtTS)
		eventCtx := eventbus.NewEventContext(eventbus.EventContextConfig{
			EventID:        eventbus.StableEventID("wa-otp-", workspaceID, msg.GetMessageId(), otp),
			EventName:      eventcatalog.WAOTPReceived.EventName,
			EventVersion:   eventcatalog.WAOTPReceived.EventVersion,
			OccurredAt:     receivedAt,
			SourceService:  waPlatformEventSource,
			CorrelationID:  firstNonEmpty(reqCtx.GetCorrelationId(), session.GetRegisteredIdentityId(), session.GetWaAccountId()),
			TraceID:        reqCtx.GetTraceId(),
			IdempotencyKey: eventbus.StableEventID("wa-otp-", workspaceID, msg.GetMessageId(), otp),
		})
		e164Number := ""
		if account != nil && account.GetPhone() != nil {
			e164Number = account.GetPhone().GetE164Number()
		}
		event := &wav1.WaOtpReceivedEvent{
			Context:              eventCtx,
			WorkspaceId:          workspaceID,
			E164Number:           e164Number,
			Source:               source,
			Otp:                  otp,
			WaAccountId:          session.GetWaAccountId(),
			ClientProfileId:      session.GetClientProfileId(),
			RegisteredIdentityId: session.GetRegisteredIdentityId(),
			MessageId:            msg.GetMessageId(),
			CandidateId:          candidate.GetCandidateId(),
			ReceivedAt:           receivedAtTS,
		}
		attrs := eventbus.Attributes(
			"workspace_id", workspaceID,
			"wa_account_id", session.GetWaAccountId(),
			"client_profile_id", session.GetClientProfileId(),
			"registered_identity_id", session.GetRegisteredIdentityId(),
			"message_id", msg.GetMessageId(),
			"source", source.String(),
		)
		publishCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_, err := s.platformPublisher.Publish(publishCtx, eventbus.Message{Subject: eventcatalog.WAOTPReceived.Subject, Event: event, Context: eventCtx, Attributes: attrs})
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("publish WA OTP event failed: %v", sanitizeEventPublishError(err))
		}
	}
}

func firstTimestamp(values ...*timestamppb.Timestamp) *timestamppb.Timestamp {
	for _, value := range values {
		if value != nil && value.IsValid() {
			return value
		}
	}
	return nil
}

func sanitizeEventPublishError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), "\n", " "))
}
