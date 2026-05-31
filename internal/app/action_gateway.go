package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const transientStateTTL = 30 * time.Minute

type actionGateway struct{ server *Server }

func NewActionGateway(server *Server) http.Handler {
	return &actionGateway{server: server}
}

func (g *actionGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeActionJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}
	payload, ok := readActionPayload(w, r)
	if !ok {
		return
	}
	action := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/wa/actions/"), "/")
	var result map[string]any
	var err error
	switch action {
	case "proxy-settings":
		result, err = g.proxySettings(payload)
	case "fingerprints/random":
		result, err = g.generateTransientFingerprint(r.Context(), payload)
	case "number-probe/account":
		result, err = g.probeAccount(r.Context(), payload)
	case "number-probe/sms":
		result, err = g.probeSMSRoute(payload)
	case "fingerprints/commit":
		result, err = g.commitFingerprint(r.Context(), payload)
	case "registration/request-sms-otp":
		result, err = g.requestSMSOTP(r.Context(), payload)
	case "registration/await-otp":
		result, err = g.awaitOTP(payload)
	case "registration/submit-otp":
		result, err = g.submitOTP(r.Context(), payload)
	case "registration/persist-login-state":
		result, err = g.persistLoginState(r.Context(), payload)
	case "registration/check-login-state":
		result, err = g.checkLoginState(r.Context(), payload)
	default:
		writeActionJSON(w, http.StatusNotFound, map[string]string{"error": "unknown WA action"})
		return
	}
	if err != nil {
		writeActionJSON(w, http.StatusOK, actionError(err))
		return
	}
	writeActionJSON(w, http.StatusOK, result)
}

func (g *actionGateway) proxySettings(payload map[string]any) (map[string]any, error) {
	_ = payload
	return map[string]any{"success": true, "proxy_mode": "US_RANDOM_DYNAMIC_IP", "country_code": "US", "preflight": false}, nil
}

func (g *actionGateway) generateTransientFingerprint(ctx context.Context, payload map[string]any) (map[string]any, error) {
	engine, err := g.nativeEngine()
	if err != nil {
		return nil, err
	}
	phone := normalizePhone(phoneFromAction(payload))
	if phone.GetE164Number() == "" {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "phone is required", false)
	}
	state, err := engine.newState(phone)
	if err != nil {
		return nil, err
	}
	data, err := marshalNativeState(state)
	if err != nil {
		return nil, err
	}
	ref := g.server.ids.NewID("wafp_")
	if err := g.server.runtime.SaveTransientState(ctx, ref, data, transientStateTTL); err != nil {
		return nil, err
	}
	profile := phoneProfileToProto(phone, state.Profile)
	return map[string]any{
		"success":                   true,
		"fingerprint_ref":           ref,
		"transient_fingerprint_ref": ref,
		"fingerprint": map[string]any{
			"schema":         profile.GetSchema(),
			"profile_sha256": profile.GetProfileSha256(),
			"phone_sha256":   profile.GetPhoneSha256(),
			"user_agent":     profile.GetUserAgent(),
		},
	}, nil
}

func (g *actionGateway) probeAccount(ctx context.Context, payload map[string]any) (map[string]any, error) {
	engine, err := g.nativeEngineForPayload(payload)
	if err != nil {
		return nil, err
	}
	state, err := g.loadTransientState(ctx, textField(payload, "fingerprint_ref"))
	if err != nil {
		return nil, err
	}
	reqCtx := actionContext(payload)
	phone := normalizePhone(phoneFromAction(payload))
	result := engine.probeAccountWithState(ctx, EngineRegistrationInput{WorkspaceID: reqCtx.GetWorkspaceId(), Phone: phone}, state)
	if result.Err != nil {
		return map[string]any{"success": false, "status": result.Status.String(), "error": protoMap(ToProtoError(result.Err))}, nil
	}
	return map[string]any{
		"success":           result.Status == waappv1.AccountProbeStatus_ACCOUNT_PROBE_STATUS_REACHABLE,
		"status":            result.Status.String(),
		"account_status":    result.Status.String(),
		"supported_methods": enumNames(result.SupportedMethods),
	}, nil
}

func (g *actionGateway) probeSMSRoute(payload map[string]any) (map[string]any, error) {
	account := objectField(payload, "account_probe")
	status := firstNonEmpty(textField(account, "account_status"), textField(account, "status"))
	reachable := status == waappv1.AccountProbeStatus_ACCOUNT_PROBE_STATUS_REACHABLE.String() || strings.EqualFold(status, "REACHABLE") || strings.EqualFold(status, "ok")
	if !reachable {
		return map[string]any{"success": false, "status": "UNAVAILABLE", "sms_status": "UNAVAILABLE", "can_send_sms": false}, nil
	}
	return map[string]any{"success": true, "status": "AVAILABLE", "sms_status": "AVAILABLE", "can_send_sms": true}, nil
}

func (g *actionGateway) commitFingerprint(ctx context.Context, payload map[string]any) (map[string]any, error) {
	ref := textField(payload, "transient_fingerprint_ref")
	state, err := g.loadTransientState(ctx, ref)
	if err != nil {
		return nil, err
	}
	account, profile, protocol, err := g.server.commitNativeState(ctx, actionContext(payload), normalizePhone(phoneFromAction(payload)), state)
	if err != nil {
		return nil, err
	}
	_ = g.server.runtime.DeleteTransientState(ctx, ref)
	return map[string]any{
		"success":             true,
		"wa_account_id":       waAccountID(account),
		"client_profile_id":   profile.GetClientProfileId(),
		"protocol_profile_id": protocol.GetProtocolProfileId(),
		"client_profile":      protoMap(profile),
	}, nil
}

func (g *actionGateway) requestSMSOTP(ctx context.Context, payload map[string]any) (map[string]any, error) {
	runner, err := g.nativeEngineForPayload(payload)
	if err != nil {
		return nil, err
	}
	reqCtx := actionContext(payload)
	resp, err := g.server.requestVerificationCode(ctx, &waappv1.RequestVerificationCodeRequest{
		Context:           reqCtx,
		WaAccountId:       textField(payload, "wa_account_id"),
		ClientProfileId:   textField(payload, "client_profile_id"),
		ProtocolProfileId: textField(payload, "protocol_profile_id"),
		DeliveryMethod:    waappv1.VerificationDeliveryMethod_VERIFICATION_DELIVERY_METHOD_SMS,
	}, runner)
	if err != nil {
		return nil, err
	}
	if resp.GetError() != nil {
		return map[string]any{"success": false, "error": protoMap(resp.GetError()), "error_message": resp.GetError().GetMessage()}, nil
	}
	record := resp.GetVerificationRequest()
	return map[string]any{
		"success":                 record.GetStatus() == waappv1.VerificationRequestStatus_VERIFICATION_REQUEST_STATUS_SENT || record.GetStatus() == waappv1.VerificationRequestStatus_VERIFICATION_REQUEST_STATUS_WAITING,
		"status":                  record.GetStatus().String(),
		"verification_request_id": record.GetVerificationRequestId(),
		"verification_request":    protoMap(record),
	}, nil
}

func (g *actionGateway) awaitOTP(payload map[string]any) (map[string]any, error) {
	return map[string]any{
		"success":                 textField(payload, "verification_request_id") != "",
		"verification_request_id": textField(payload, "verification_request_id"),
		"timeout_seconds":         numberField(payload, "timeout_seconds"),
	}, nil
}

func (g *actionGateway) submitOTP(ctx context.Context, payload map[string]any) (map[string]any, error) {
	runner, err := g.nativeEngineForPayload(payload)
	if err != nil {
		return nil, err
	}
	resp, err := g.server.submitVerificationCode(ctx, &waappv1.SubmitVerificationCodeRequest{
		Context:               actionContext(payload),
		VerificationRequestId: textField(payload, "verification_request_id"),
		SubmittedCode:         &waappv1.SubmitVerificationCodeRequest_Code{Code: textField(payload, "code")},
	}, runner)
	if err != nil {
		return nil, err
	}
	if resp.GetError() != nil {
		return map[string]any{"success": false, "error": protoMap(resp.GetError()), "error_message": resp.GetError().GetMessage(), "registration": protoMap(resp.GetRegistration())}, nil
	}
	return map[string]any{
		"success":      resp.GetRegistration().GetStatus() == waappv1.RegistrationStatus_REGISTRATION_STATUS_REGISTERED && resp.GetLoginState().GetStatus() == waappv1.LoginStateStatus_LOGIN_STATE_STATUS_ACTIVE,
		"status":       resp.GetRegistration().GetStatus().String(),
		"registration": protoMap(resp.GetRegistration()),
		"login_state":  protoMap(resp.GetLoginState()),
	}, nil
}

func (g *actionGateway) persistLoginState(ctx context.Context, payload map[string]any) (map[string]any, error) {
	registration := objectField(payload, "registration")
	if nested := objectField(registration, "registration"); len(nested) > 0 {
		registration = nested
	}
	registrationID := textField(registration, "registration_id")
	var loginState *waappv1.LoginState
	var err error
	workspaceID := actionContext(payload).GetWorkspaceId()
	if registrationID != "" {
		loginState, err = g.server.store.GetLoginStateByRegistration(ctx, workspaceID, registrationID)
	} else if clientProfileID := textField(payload, "client_profile_id"); clientProfileID != "" {
		loginState, err = g.server.store.GetActiveLoginState(ctx, workspaceID, textField(registration, "wa_account_id"), clientProfileID)
	} else {
		err = NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "registration_id or client_profile_id is required", false)
	}
	if err != nil {
		return map[string]any{"success": false, "error": protoMap(ToProtoError(err)), "error_message": ToProtoError(err).GetMessage()}, nil
	}
	ok := loginState.GetStatus() == waappv1.LoginStateStatus_LOGIN_STATE_STATUS_ACTIVE
	return map[string]any{"success": ok, "status": loginState.GetStatus().String(), "login_state": protoMap(loginState)}, nil
}

func (g *actionGateway) checkLoginState(ctx context.Context, payload map[string]any) (map[string]any, error) {
	runner, err := g.nativeEngineForPayload(payload)
	if err != nil {
		return nil, err
	}
	loginStatePayload := objectField(payload, "login_state")
	req := &waappv1.CheckLoginStateRequest{
		Context:              actionContext(payload),
		LoginStateId:         firstNonEmpty(textField(payload, "login_state_id"), textField(loginStatePayload, "login_state_id")),
		WaAccountId:          firstNonEmpty(textField(payload, "wa_account_id"), textField(loginStatePayload, "wa_account_id")),
		ClientProfileId:      firstNonEmpty(textField(payload, "client_profile_id"), textField(loginStatePayload, "client_profile_id")),
		RegisteredIdentityId: firstNonEmpty(textField(payload, "registered_identity_id"), textField(loginStatePayload, "registered_identity_id")),
	}
	if timeout := numberField(payload, "remote_timeout_seconds"); timeout > 0 {
		req.RemoteTimeout = durationpb.New(time.Duration(timeout) * time.Second)
	}
	resp, err := g.server.checkLoginState(ctx, req, runner)
	if err != nil {
		return nil, err
	}
	check := resp.GetCheck()
	ok := resp.GetError() == nil && check.GetStatus() == waappv1.LoginStateCheckStatus_LOGIN_STATE_CHECK_STATUS_ACTIVE && resp.GetLoginState().GetStatus() == waappv1.LoginStateStatus_LOGIN_STATE_STATUS_ACTIVE
	out := map[string]any{
		"success":     ok,
		"status":      check.GetStatus().String(),
		"login_state": protoMap(resp.GetLoginState()),
		"check":       protoMap(check),
	}
	if resp.GetError() != nil {
		out["error"] = protoMap(resp.GetError())
		out["error_message"] = resp.GetError().GetMessage()
	}
	return out, nil
}

func (s *Server) commitNativeState(ctx context.Context, reqCtx *waappv1.RequestContext, phone *waappv1.PhoneTarget, state nativeState) (*waappv1.WAAccount, *waappv1.ClientProfile, *waappv1.ProtocolProfile, error) {
	engine, ok := s.runner.(*NativeEngine)
	if !ok {
		return nil, nil, nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_UNSUPPORTED_OPERATION, "native engine is required", false)
	}
	workspaceID := reqCtx.GetWorkspaceId()
	if phone.GetE164Number() == "" {
		return nil, nil, nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "phone is required", false)
	}
	account, err := s.store.FindWAAccountByPhone(ctx, workspaceID, phone.GetE164Number())
	if err != nil {
		now := s.clock.Now()
		account = newWAAccount(s.ids.NewID("waacc_"), workspaceID, phone, waappv1.WAAccountStatus_WA_ACCOUNT_STATUS_ACTIVE, &waappv1.AuditStamp{CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)})
		account, err = s.saveWAAccount(ctx, workspaceID, account)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	protocol, err := s.ensureDefaultProtocolProfile(ctx, workspaceID)
	if err != nil {
		return nil, nil, nil, err
	}
	now := s.clock.Now()
	profile := &waappv1.ClientProfile{ClientProfileId: s.ids.NewID("wacp_"), WaAccountId: waAccountID(account), ProtocolProfileId: protocol.GetProtocolProfileId(), Status: waappv1.ClientProfileStatus_CLIENT_PROFILE_STATUS_PREPARING, RegistrationKeyState: waappv1.KeyMaterialStatus_KEY_MATERIAL_STATUS_PENDING, MessagingKeyState: waappv1.KeyMaterialStatus_KEY_MATERIAL_STATUS_PENDING, Audit: &waappv1.AuditStamp{CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)}}
	if err := s.store.SaveClientProfile(ctx, profile, workspaceID); err != nil {
		return nil, nil, nil, err
	}
	state.CC = firstNonEmpty(state.CC, phoneCC(phone))
	state.Phone = firstNonEmpty(state.Phone, phoneNational(phone))
	if err := engine.saveState(profile.GetClientProfileId(), state); err != nil {
		profile.Status = waappv1.ClientProfileStatus_CLIENT_PROFILE_STATUS_REJECTED
		profile.LastError = ToProtoError(err)
		_ = s.store.SaveClientProfile(ctx, profile, workspaceID)
		return nil, nil, nil, err
	}
	profile.Status = waappv1.ClientProfileStatus_CLIENT_PROFILE_STATUS_READY
	profile.RegistrationKeyState = waappv1.KeyMaterialStatus_KEY_MATERIAL_STATUS_READY
	profile.MessagingKeyState = waappv1.KeyMaterialStatus_KEY_MATERIAL_STATUS_READY
	profile.Audit.UpdatedAt = timestamppb.New(s.clock.Now())
	if err := s.store.SaveClientProfile(ctx, profile, workspaceID); err != nil {
		return nil, nil, nil, err
	}
	return account, profile, protocol, nil
}

func (s *Server) ensureDefaultProtocolProfile(ctx context.Context, workspaceID string) (*waappv1.ProtocolProfile, error) {
	suffix := stableID(workspaceID)
	protocolID := "waproto_native_" + suffix
	if profile, err := s.store.GetProtocolProfile(ctx, workspaceID, protocolID); err == nil {
		return profile, nil
	}
	now := s.clock.Now()
	artifactID := "waart_native_" + suffix
	artifact := &waappv1.AppArtifact{ArtifactId: artifactID, Label: "WA native app", VersionLabel: "native", ObservedAt: timestamppb.New(now)}
	if err := s.store.SaveAppArtifact(ctx, artifact, workspaceID); err != nil {
		return nil, err
	}
	profile := &waappv1.ProtocolProfile{
		ProtocolProfileId: protocolID,
		AppArtifactId:     artifactID,
		DisplayName:       "WA native protocol",
		Status:            waappv1.ProtocolProfileStatus_PROTOCOL_PROFILE_STATUS_ACTIVE,
		Capabilities: []waappv1.ProtocolCapability{
			waappv1.ProtocolCapability_PROTOCOL_CAPABILITY_ACCOUNT_PROBE,
			waappv1.ProtocolCapability_PROTOCOL_CAPABILITY_CODE_REQUEST,
			waappv1.ProtocolCapability_PROTOCOL_CAPABILITY_CODE_SUBMIT,
			waappv1.ProtocolCapability_PROTOCOL_CAPABILITY_MESSAGE_SESSION,
		},
		RegistrationFlows: []waappv1.RegistrationFlowKind{waappv1.RegistrationFlowKind_REGISTRATION_FLOW_KIND_NEW_ACCOUNT},
		MessageTransports: []waappv1.MessageTransportKind{waappv1.MessageTransportKind_MESSAGE_TRANSPORT_KIND_LONG_CONNECTION},
		DiscoveredAt:      timestamppb.New(now),
		Audit:             &waappv1.AuditStamp{CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)},
	}
	if err := s.store.SaveProtocolProfile(ctx, profile, workspaceID); err != nil {
		return nil, err
	}
	return profile, nil
}

func (g *actionGateway) nativeEngineForPayload(payload map[string]any) (*NativeEngine, error) {
	engine, err := g.nativeEngine()
	if err != nil {
		return nil, err
	}
	proxyURL := actionProxyURL(payload)
	if proxyURL == "" {
		return engine, nil
	}
	return engine.WithProxyURL(proxyURL)
}

func actionProxyURL(payload map[string]any) string {
	return firstNonEmpty(textField(payload, "proxy_url"), textField(objectField(payload, "proxy"), "proxy_url"))
}

func (g *actionGateway) nativeEngine() (*NativeEngine, error) {
	engine, ok := g.server.runner.(*NativeEngine)
	if !ok {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_UNSUPPORTED_OPERATION, "native engine is required", false)
	}
	return engine, nil
}

func (g *actionGateway) loadTransientState(ctx context.Context, ref string) (nativeState, error) {
	data, err := g.server.runtime.GetTransientState(ctx, ref)
	if err != nil {
		return nativeState{}, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_PROFILE_NOT_FOUND, "transient fingerprint state not found", false)
	}
	return unmarshalNativeState(data)
}

func readActionPayload(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeActionJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return nil, false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return map[string]any{}, true
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	payload := map[string]any{}
	if err := dec.Decode(&payload); err != nil {
		writeActionJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must be json"})
		return nil, false
	}
	return payload, true
}

func writeActionJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func actionContext(payload map[string]any) *waappv1.RequestContext {
	return &waappv1.RequestContext{
		RequestId:     textField(payload, "request_id"),
		WorkspaceId:   firstNonEmpty(textField(payload, "workspace_id"), "default"),
		ActorId:       textField(payload, "actor_id"),
		CorrelationId: firstNonEmpty(textField(payload, "correlation_id"), textField(payload, "job_id")),
		TraceId:       textField(payload, "trace_id"),
	}
}

func phoneFromAction(payload map[string]any) *waappv1.PhoneTarget {
	phone := objectField(payload, "phone")
	if len(phone) == 0 {
		phone = payload
	}
	return &waappv1.PhoneTarget{
		E164Number:         firstNonEmpty(textField(phone, "e164_number"), textField(payload, "e164_number")),
		CountryCallingCode: firstNonEmpty(textField(phone, "country_calling_code"), textField(payload, "country_calling_code"), textField(payload, "cc")),
		NationalNumber:     firstNonEmpty(textField(phone, "national_number"), textField(payload, "national_number"), textField(payload, "phone"), textField(payload, "number")),
		CountryIso2:        firstNonEmpty(textField(phone, "country_iso2"), textField(payload, "country_iso2"), textField(payload, "country_code")),
	}
}

func objectField(data map[string]any, key string) map[string]any {
	if data == nil {
		return map[string]any{}
	}
	if value, ok := data[key].(map[string]any); ok {
		return value
	}
	return map[string]any{}
}

func textField(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, ok := data[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	case float64:
		return fmt.Sprintf("%.0f", typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func numberField(data map[string]any, key string) int64 {
	switch value := data[key].(type) {
	case json.Number:
		n, _ := value.Int64()
		return n
	case float64:
		return int64(value)
	case string:
		var n int64
		_, _ = fmt.Sscan(value, &n)
		return n
	default:
		return 0
	}
}

func protoMap(msg proto.Message) map[string]any {
	if msg == nil {
		return map[string]any{}
	}
	data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(msg)
	if err != nil {
		return map[string]any{}
	}
	out := map[string]any{}
	_ = json.Unmarshal(data, &out)
	return out
}

func actionError(err error) map[string]any {
	protoErr := ToProtoError(err)
	return map[string]any{"success": false, "error": protoMap(protoErr), "error_message": protoErr.GetMessage()}
}

func enumNames[T interface{ String() string }](values []T) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value.String())
	}
	return out
}
