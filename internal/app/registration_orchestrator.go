package app

import (
	"context"
	"strings"
	"time"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
)

func (s *Server) StartRegistration(ctx context.Context, payload map[string]any) (map[string]any, error) {
	if s == nil {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_UNSUPPORTED_OPERATION, "wa-app service is not configured", false)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	gateway := &actionGateway{server: s}
	basePayload := cloneActionPayload(payload)
	basePayload["purpose"] = firstNonEmpty(textField(basePayload, "purpose"), "WA_REGISTRATION")
	basePayload["proxy_session_mode"] = firstNonEmpty(textField(basePayload, "proxy_session_mode"), "STICKY")

	fingerprint, err := gateway.generateTransientFingerprint(ctx, basePayload)
	if err != nil {
		return nil, err
	}
	fingerprintRef := firstNonEmpty(textField(fingerprint, "fingerprint_ref"), textField(fingerprint, "transient_fingerprint_ref"))
	state, err := gateway.loadTransientState(ctx, fingerprintRef)
	if err != nil {
		return nil, err
	}
	runner, route, managedRoute, err := gateway.registrationRequestRunner(ctx, basePayload)
	if err != nil {
		return nil, err
	}
	defer runner.CloseIdleConnections()
	defer func() {
		_ = gateway.server.runtime.DeleteTransientState(context.Background(), fingerprintRef)
	}()
	routeSaved := false
	defer func() {
		if managedRoute && !routeSaved {
			gateway.releaseProxyRoute(context.Background(), route)
		}
	}()
	phone := normalizePhone(phoneFromAction(basePayload))
	probeResult := runner.probeAccountWithState(ctx, EngineRegistrationInput{AppVersion: defaultWAAppVersion, Phone: phone, DeliveryMethod: waappv1.VerificationDeliveryMethod_VERIFICATION_DELIVERY_METHOD_SMS}, state)
	if !registrationProbeAllowsSMS(probeResult) {
		return rejectedRegistrationResult(basePayload, registrationProbeFailureMap(probeResult, route, managedRoute)), nil
	}
	codeResult, updatedState := runner.requestVerificationCodeWithState(ctx, EngineRegistrationInput{AppVersion: defaultWAAppVersion, Phone: phone, DeliveryMethod: waappv1.VerificationDeliveryMethod_VERIFICATION_DELIVERY_METHOD_SMS}, state)
	if !verificationCodeRequestAccepted(codeResult) {
		return rejectedRegistrationResult(basePayload, registrationRequestFailureMap(codeResult, route, managedRoute)), nil
	}
	account, profile, protocol, err := gateway.server.commitNativeState(ctx, phone, updatedState)
	if err != nil {
		return nil, err
	}
	record := gateway.server.newVerificationCodeRequestRecord(account, profile, waappv1.VerificationDeliveryMethod_VERIFICATION_DELIVERY_METHOD_SMS, codeResult)
	if err := gateway.server.store.SaveVerificationRequest(ctx, record); err != nil {
		_ = gateway.discardRejectedRegistration(context.Background(), basePayload, waAccountID(account), record.GetVerificationRequestId())
		return nil, err
	}
	verificationRequestID := record.GetVerificationRequestId()
	if managedRoute {
		if err := gateway.saveRegistrationProxyRoute(ctx, verificationRequestID, route); err != nil {
			_ = gateway.discardRejectedRegistration(context.Background(), basePayload, waAccountID(account), verificationRequestID)
			return nil, err
		}
		routeSaved = true
	}
	wait := registrationOTPWait{
		WAAccountID:           waAccountID(account),
		VerificationRequestID: verificationRequestID,
		CreatedAtUnix:         time.Now().UTC().Unix(),
	}
	if err := gateway.saveRegistrationOTPWait(ctx, wait, registrationOTPWaitDefaultTTL); err != nil {
		_ = gateway.releaseRegistrationProxyRoute(context.Background(), wait.VerificationRequestID)
		_ = gateway.discardRejectedRegistration(context.Background(), basePayload, waAccountID(account), verificationRequestID)
		return nil, err
	}
	response := map[string]any{
		"success":                 true,
		"status":                  record.GetStatus().String(),
		"error_message":           "",
		"phone":                   objectField(basePayload, "phone"),
		"wa_account_id":           waAccountID(account),
		"client_profile_id":       profile.GetClientProfileId(),
		"protocol_profile_id":     protocol.GetProtocolProfileId(),
		"verification_request_id": verificationRequestID,
		"verification_request":    protoMap(record),
		"registration_phase":      registrationPhase(true, verificationRequestID, durationFromProto(record.GetRetryAfter())),
		"fingerprint_persistence": "COMMITTED",
		"persisted":               true,
		"proxy":                   registrationOrchestratorProxySummary(registrationProxyRouteMap(route, managedRoute)),
	}
	if seconds := durationSeconds(record.GetRetryAfter()); seconds > 0 {
		response["retry_after_seconds"] = seconds
	}
	return response, nil
}

func cloneActionPayload(payload map[string]any) map[string]any {
	cloned := make(map[string]any, len(payload))
	for key, value := range payload {
		cloned[key] = value
	}
	return cloned
}

func registrationPhase(success bool, verificationRequestID string, retryAfter time.Duration) string {
	if !success || strings.TrimSpace(verificationRequestID) == "" {
		return "OTP_REQUEST_FAILED"
	}
	if retryAfter > 0 {
		return "OTP_COOLDOWN"
	}
	return "OTP_WAITING"
}

func verificationCodeRequestAccepted(result EngineCodeResult) bool {
	return result.Err == nil && (result.Status == waappv1.VerificationRequestStatus_VERIFICATION_REQUEST_STATUS_SENT || result.Status == waappv1.VerificationRequestStatus_VERIFICATION_REQUEST_STATUS_WAITING)
}

func registrationProbeAllowsSMS(result EngineProbeResult) bool {
	return result.Err == nil && result.Status == waappv1.AccountProbeStatus_ACCOUNT_PROBE_STATUS_REACHABLE && result.CanSendSMS
}

func registrationRequestFailureMap(result EngineCodeResult, route DynamicProxyRoute, managedRoute bool) map[string]any {
	err := result.Err
	if err == nil {
		err = registrationCodeRequestError(result)
	}
	protoErr := ToProtoError(err)
	seconds := int64(result.RetryAfter / time.Second)
	accountFlow := registrationCodeRequestFlow(result, protoErr)
	response := map[string]any{
		"success":        false,
		"request_failed": true,
		"status":         firstNonEmpty(result.Status.String(), "VERIFICATION_REQUEST_STATUS_REJECTED"),
		"error":          protoMap(protoErr),
		"error_message":  protoErr.GetMessage(),
		"reject_reason":  registrationRejectReason(protoErr.GetMessage()),
		"proxy":          registrationProxyRouteMap(route, managedRoute),
		"sms_probe": map[string]any{
			"status":              result.Status.String(),
			"raw_status":          result.RawStatus,
			"raw_reason":          result.RawReason,
			"retry_after_seconds": seconds,
		},
		"phone_status": map[string]any{
			"account_status":       waappv1.AccountProbeStatus_ACCOUNT_PROBE_STATUS_REJECTED.String(),
			"account_flow":         accountFlow,
			"account_raw_status":   result.RawStatus,
			"account_raw_reason":   result.RawReason,
			"account_error":        protoErr.GetMessage(),
			"account_reachable":    false,
			"request_failed":       true,
			"blocked":              accountFlow == accountProbeFlowBlocked,
			"sms_status":           result.Status.String(),
			"sms_available":        false,
			"sms_wait_seconds":     seconds,
			"reject_reason":        protoErr.GetMessage(),
			"can_register":         false,
			"registration_phase":   "OTP_REQUEST_FAILED",
			"verification_status":  result.Status.String(),
			"verification_reason":  result.RawReason,
			"verification_outcome": result.RawStatus,
		},
	}
	if seconds > 0 {
		response["retry_after_seconds"] = seconds
		response["registration_phase"] = "OTP_COOLDOWN"
	}
	return response
}

func registrationCodeRequestError(result EngineCodeResult) error {
	switch {
	case result.RetryAfter > 0 || existRateLimitedReason(result.RawReason):
		return NewError(waappv1.WaErrorCode_WA_ERROR_CODE_RATE_LIMITED, "verification request is cooling down", true)
	case existInvalidNumberReason(result.RawReason):
		return NewError(waappv1.WaErrorCode_WA_ERROR_CODE_REJECTED, "verification request rejected: phone format is invalid", false)
	case strings.EqualFold(result.RawReason, "no_routes") || strings.EqualFold(result.RawStatus, "no_routes"):
		return NewError(waappv1.WaErrorCode_WA_ERROR_CODE_ROUTE_UNAVAILABLE, "no_routes: verification route is unavailable", false)
	case strings.TrimSpace(result.RawReason) != "":
		return NewError(waappv1.WaErrorCode_WA_ERROR_CODE_REJECTED, "verification request was rejected: reason="+result.RawReason, false)
	case strings.TrimSpace(result.RawStatus) != "":
		return NewError(waappv1.WaErrorCode_WA_ERROR_CODE_REJECTED, "verification request was rejected: status="+result.RawStatus, false)
	default:
		return NewError(waappv1.WaErrorCode_WA_ERROR_CODE_REJECTED, "verification request was rejected", false)
	}
}

func registrationCodeRequestFlow(result EngineCodeResult, protoErr *waappv1.WaError) string {
	raw := strings.ToLower(strings.TrimSpace(result.RawReason + " " + result.RawStatus + " " + protoErr.GetMessage()))
	switch {
	case existInvalidNumberReason(raw) || strings.Contains(raw, "format_wrong") || strings.Contains(raw, "length_short") || strings.Contains(raw, "length_long"):
		return accountProbeFlowInvalidNumber
	case existRateLimitedReason(raw) || strings.Contains(raw, "cooling down"):
		return accountProbeFlowRateLimited
	case strings.Contains(raw, "blocked"):
		return accountProbeFlowBlocked
	default:
		return accountProbeFlowProbeFailed
	}
}

func registrationProbeFailureMap(result EngineProbeResult, route DynamicProxyRoute, managedRoute bool) map[string]any {
	err := result.Err
	if err == nil {
		err = registrationProbeError(result)
	}
	protoErr := ToProtoError(err)
	response := map[string]any{
		"success":       false,
		"status":        firstNonEmpty(result.Status.String(), "ACCOUNT_PROBE_STATUS_UNREACHABLE"),
		"error":         protoMap(protoErr),
		"error_message": protoErr.GetMessage(),
		"proxy":         registrationProxyRouteMap(route, managedRoute),
		"phone_status": map[string]any{
			"account_status":     result.Status.String(),
			"account_flow":       result.AccountFlow,
			"account_raw_status": result.RawStatus,
			"account_raw_reason": result.RawReason,
			"blocked":            result.Blocked,
			"registered":         result.Registered,
			"sms_available":      result.CanSendSMS,
			"sms_status":         registrationProbeSMSStatus(result),
			"sms_wait_seconds":   result.SMSWaitSeconds,
		},
	}
	if result.SMSWaitSeconds > 0 {
		response["retry_after_seconds"] = result.SMSWaitSeconds
		response["registration_phase"] = "OTP_COOLDOWN"
	}
	return response
}

func registrationProbeError(result EngineProbeResult) error {
	switch {
	case result.SMSWaitSeconds > 0 || result.AccountFlow == accountProbeFlowRateLimited:
		return NewError(waappv1.WaErrorCode_WA_ERROR_CODE_RATE_LIMITED, "verification request is cooling down", true)
	case result.Blocked:
		return NewError(waappv1.WaErrorCode_WA_ERROR_CODE_REJECTED, "number is blocked", false)
	case result.Status != waappv1.AccountProbeStatus_ACCOUNT_PROBE_STATUS_REACHABLE:
		return NewError(waappv1.WaErrorCode_WA_ERROR_CODE_REJECTED, "account probe is not reachable", false)
	default:
		return NewError(waappv1.WaErrorCode_WA_ERROR_CODE_ROUTE_UNAVAILABLE, "SMS route unavailable", true)
	}
}

func registrationProbeSMSStatus(result EngineProbeResult) string {
	if result.CanSendSMS {
		return "AVAILABLE"
	}
	if result.SMSWaitSeconds > 0 {
		return "COOLDOWN"
	}
	return "UNAVAILABLE"
}

func (g *actionGateway) discardRejectedRegistration(ctx context.Context, basePayload map[string]any, waAccountID string, verificationRequestID string) error {
	if strings.TrimSpace(verificationRequestID) != "" {
		_ = g.releaseRegistrationProxyRoute(context.Background(), verificationRequestID)
	}
	if strings.TrimSpace(waAccountID) == "" {
		return nil
	}
	result, err := g.cleanupFailedRegistration(ctx, map[string]any{
		"wa_account_id":           waAccountID,
		"verification_request_id": verificationRequestID,
	})
	if err != nil {
		return err
	}
	if boolField(result, "success") {
		return nil
	}
	return NewError(waappv1.WaErrorCode_WA_ERROR_CODE_INTERNAL, firstNonEmpty(textField(result, "error_message"), "discard rejected WA registration failed"), false)
}

func rejectedRegistrationResult(basePayload map[string]any, requested map[string]any) map[string]any {
	errorMessage := firstNonEmpty(textField(requested, "error_message"), textField(objectField(requested, "error"), "message"), "WA registration request was rejected")
	response := map[string]any{
		"success":                 false,
		"status":                  firstNonEmpty(textField(requested, "status"), "OTP_REQUEST_REJECTED"),
		"error":                   objectField(requested, "error"),
		"error_message":           errorMessage,
		"reject_reason":           registrationRejectReason(errorMessage),
		"phone":                   objectField(basePayload, "phone"),
		"registration_phase":      firstNonEmpty(textField(requested, "registration_phase"), "OTP_REQUEST_REJECTED"),
		"fingerprint_persistence": "DISCARDED",
		"persisted":               false,
		"phone_status":            objectField(requested, "phone_status"),
		"proxy":                   registrationOrchestratorProxySummary(objectField(requested, "proxy")),
	}
	if seconds := numberField(requested, "retry_after_seconds"); seconds > 0 {
		response["retry_after_seconds"] = seconds
	}
	return response
}

func registrationRejectReason(errorMessage string) string {
	normalized := strings.ToLower(errorMessage)
	if strings.Contains(normalized, "format_wrong") || strings.Contains(normalized, "length_short") || strings.Contains(normalized, "length_long") || strings.Contains(normalized, "phone format") {
		return "invalid_number"
	}
	if strings.Contains(normalized, "too_recent") || strings.Contains(normalized, "too_many") || strings.Contains(normalized, "temporarily_unavailable") || strings.Contains(normalized, "cooldown") || strings.Contains(normalized, "cooling down") {
		return "rate_limited"
	}
	if strings.Contains(normalized, "no_routes") {
		return "no_routes"
	}
	if strings.Contains(normalized, "blocked") {
		return "blocked"
	}
	return "rejected"
}

func registrationOrchestratorProxySummary(proxy map[string]any) map[string]any {
	mode := firstNonEmpty(textField(proxy, "proxy_mode"), "DIRECT")
	countryCode := firstNonEmpty(textField(proxy, "country_code"), "LOCAL")
	return map[string]any{"proxy_mode": mode, "country_code": countryCode}
}
