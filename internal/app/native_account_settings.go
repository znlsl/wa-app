package app

import (
	"context"
	"errors"
	"strings"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
)

type accountSettingsIQSender interface {
	sendIQ(context.Context, nativeState, string, string, chatdNode, string) (chatdNode, chatdSessionUpdate, error)
}

func (e *NativeEngine) ApplyAccountSettings(ctx context.Context, input EngineAccountSettingsInput) EngineAccountSettingsResult {
	state, err := e.loadState(ctx, input.ClientProfileID)
	if err != nil {
		return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_REJECTED, Err: err}
	}
	if state.ChatStatic.Private == "" || state.ChatStatic.Public == "" {
		state.ChatStatic = ensureChatStatic(state.ChatStatic)
		_ = e.saveState(ctx, input.ClientProfileID, state)
	}
	proxyURL, err := e.proxyURL()
	if err != nil {
		return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_REJECTED, Err: err}
	}
	return e.applyAccountSettingsWithSender(ctx, input, state, newChatdClient(chatdConfigForState(proxyURL, state, defaultAccountIQTimeout)))
}

func (e *NativeEngine) applyAccountSettingsWithSender(ctx context.Context, input EngineAccountSettingsInput, state nativeState, sender accountSettingsIQSender) EngineAccountSettingsResult {
	if input.Kind == waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_PROFILE_NAME_SET {
		return e.applyAccountProfileName(ctx, input, state, sender)
	}
	request := buildAccountSettingsIQ(e.ids.NewID("waiq_"), input)
	if request.Tag == "" {
		return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_REJECTED, Err: NewError(waappv1.WaErrorCode_WA_ERROR_CODE_UNSUPPORTED_OPERATION, "account settings operation is not supported", false)}
	}
	response, update, err := sender.sendIQ(ctx, state, input.RegisteredIdentityID, defaultWAAppVersion, request, "account settings iq timed out")
	if applyChatdSessionUpdateState(&state, update) {
		_ = e.saveState(ctx, input.ClientProfileID, state)
	}
	if err != nil {
		return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_REJECTED, Err: NewError(waappv1.WaErrorCode_WA_ERROR_CODE_REJECTED, "native account settings request failed", accountSettingsRetryableError(err))}
	}
	return accountSettingsResultFromIQ(input, response)
}

func (e *NativeEngine) applyAccountProfileName(ctx context.Context, input EngineAccountSettingsInput, state nativeState, sender accountSettingsIQSender) EngineAccountSettingsResult {
	state.PushName = input.DisplayName
	if err := e.saveState(ctx, input.ClientProfileID, state); err != nil {
		return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_REJECTED, Err: NewError(waappv1.WaErrorCode_WA_ERROR_CODE_INTERNAL, "native account profile name could not be saved", true)}
	}
	timestampMS := e.clock.Now().UnixMilli()
	if timestampMS < 0 {
		timestampMS = 0
	}
	request, collection, err := buildNativePushNamePatch(&state, input.DisplayName, uint64(timestampMS))
	if err != nil {
		if isNativePushNamePatchOptionalError(err) {
			return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_ACCEPTED}
		}
		return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_REJECTED, Err: err}
	}
	request.Attrs["id"] = e.ids.NewID("waiq_")
	response, update, err := sender.sendIQ(ctx, state, input.RegisteredIdentityID, defaultWAAppVersion, request, "account settings iq timed out")
	changed := applyChatdSessionUpdateState(&state, update)
	if err != nil {
		if changed {
			_ = e.saveState(ctx, input.ClientProfileID, state)
		}
		return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_ACCEPTED}
	}
	if err := chatdIQError(response); err != nil {
		if changed {
			_ = e.saveState(ctx, input.ClientProfileID, state)
		}
		return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_ACCEPTED}
	}
	state.ensureMaps()
	state.AppState.Collections[waAppStatePushNameCollection] = collection
	_ = e.saveState(ctx, input.ClientProfileID, state)
	return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_ACCEPTED}
}

func isNativePushNamePatchOptionalError(err error) bool {
	var appErr *AppError
	if !errors.As(err, &appErr) {
		return false
	}
	if appErr.Code != waappv1.WaErrorCode_WA_ERROR_CODE_CONFLICT {
		return false
	}
	switch appErr.Message {
	case waAppStateKeyUnavailable,
		"WA app-state key id is invalid",
		"WA app-state key data is invalid",
		"WA app-state mutation key is invalid":
		return true
	default:
		return false
	}
}

func buildAccountSettingsIQ(id string, input EngineAccountSettingsInput) chatdNode {
	switch input.Kind {
	case waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_TWO_FACTOR_AUTH_STATUS_GET:
		return buildGetTwoFactorAuthStatusIQ(id)
	case waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_TWO_FACTOR_AUTH_SETTINGS:
		return buildTwoFactorAuthSettingsIQ(id, input.Pin)
	case waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_EMAIL_SET:
		return buildSetAccountEmailIQ(id, input.EmailAddress, input.GoogleIDToken)
	case waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_EMAIL_OTP_REQUEST:
		return buildRequestAccountEmailOtpIQ(id, firstNonEmpty(input.LocaleLanguage, "en"), firstNonEmpty(input.LocaleCountry, "US"))
	case waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_EMAIL_OTP_VERIFY:
		return buildVerifyAccountEmailOtpIQ(id, input.Code)
	case waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_PROFILE_PICTURE_SET:
		return buildAccountProfilePictureIQ(id, input.ProfilePicture)
	case waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_PROFILE_PICTURE_REMOVE:
		return buildAccountProfilePictureIQ(id, nil)
	default:
		return chatdNode{}
	}
}

func buildAccountIQ(id string, iqType string, children []chatdNode) chatdNode {
	return chatdNode{Tag: "iq", Attrs: map[string]string{"to": "s.whatsapp.net", "id": id, "xmlns": "urn:xmpp:whatsapp:account", "type": iqType}, Content: children}
}

func buildGetTwoFactorAuthStatusIQ(id string) chatdNode {
	return buildAccountIQ(id, "get", []chatdNode{{Tag: "2fa"}})
}

func buildTwoFactorAuthSettingsIQ(id string, pin string) chatdNode {
	return buildAccountIQ(id, "set", []chatdNode{{Tag: "2fa", Content: []chatdNode{{Tag: "code", Content: pin}}}})
}

func buildSetAccountEmailIQ(id string, emailAddress string, googleIDToken string) chatdNode {
	children := []chatdNode{}
	if strings.TrimSpace(googleIDToken) != "" {
		children = append(children, chatdNode{Tag: "id_token", Content: googleIDToken})
	}
	children = append(children, chatdNode{Tag: "email_address", Content: emailAddress})
	return buildAccountSettingsEmailIQ(id, []chatdNode{{Tag: "email", Content: children}})
}

func buildAccountSettingsEmailIQ(id string, children []chatdNode) chatdNode {
	return chatdNode{
		Tag:     "iq",
		Attrs:   map[string]string{"to": "s.whatsapp.net", "id": id, "xmlns": "urn:xmpp:whatsapp:account"},
		Content: children,
	}
}

func buildRequestAccountEmailOtpIQ(id string, language string, country string) chatdNode {
	return buildAccountIQ(id, "set", []chatdNode{{Tag: "verify_email", Content: []chatdNode{{Tag: "lg", Content: language}, {Tag: "lc", Content: country}}}})
}

func buildVerifyAccountEmailOtpIQ(id string, code string) chatdNode {
	return buildAccountIQ(id, "get", []chatdNode{{Tag: "verify_email", Content: []chatdNode{{Tag: "code", Content: code}}}})
}

func buildAccountProfilePictureIQ(id string, image []byte) chatdNode {
	picture := chatdNode{Tag: "picture", Attrs: map[string]string{"type": "image"}}
	if len(image) > 0 {
		picture.Content = append([]byte(nil), image...)
	}
	return chatdNode{
		Tag:     "iq",
		Attrs:   map[string]string{"xmlns": "w:profile:picture", "id": id, "to": "s.whatsapp.net", "type": "set"},
		Content: []chatdNode{picture},
	}
}

func accountSettingsResultFromIQ(input EngineAccountSettingsInput, node chatdNode) EngineAccountSettingsResult {
	if err := chatdIQError(node); err != nil {
		return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_REJECTED, Err: err}
	}
	switch input.Kind {
	case waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_TWO_FACTOR_AUTH_STATUS_GET:
		return twoFactorAuthStatusFromIQ(node)
	case waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_EMAIL_SET:
		return emailSetResultFromIQ(node, input.EmailAddress)
	case waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_EMAIL_OTP_REQUEST:
		return emailOtpRequestResultFromIQ(node)
	case waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_EMAIL_OTP_VERIFY:
		return emailOtpVerifyResultFromIQ(node)
	case waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_PROFILE_PICTURE_SET:
		return accountProfilePictureSetResultFromIQ(node)
	default:
		return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_ACCEPTED}
	}
}

func twoFactorAuthStatusFromIQ(node chatdNode) EngineAccountSettingsResult {
	status := &waappv1.TwoFactorAuthStatus{}
	twoFactorNode, ok := chatdChild(node, "2fa")
	if !ok {
		if emailNode, hasEmail := chatdChild(node, "email"); hasEmail {
			mergeEmailStatusFromIQ(status, emailNode)
		}
		return EngineAccountSettingsResult{TwoFactorStatus: status}
	}
	_, hasCode := chatdChild(twoFactorNode, "code")
	emailNode, hasEmail := chatdChild(twoFactorNode, "email")
	status.Configured = hasCode || chatdNodeBool(twoFactorNode, "configured") || chatdNodeBool(twoFactorNode, "enabled")
	status.EmailConfigured = hasEmail || chatdNodeBool(twoFactorNode, "email_configured") || chatdNodeBool(twoFactorNode, "email_set")
	if hasEmail {
		mergeEmailStatusFromIQ(status, emailNode)
	}
	if emailNode, hasEmail := chatdChild(node, "email"); hasEmail {
		mergeEmailStatusFromIQ(status, emailNode)
	}
	return EngineAccountSettingsResult{TwoFactorStatus: status}
}

func mergeEmailStatusFromIQ(status *waappv1.TwoFactorAuthStatus, emailNode chatdNode) {
	if status == nil {
		return
	}
	status.EmailConfigured = true
	if value, ok := chatdNodeStringValue(emailNode, "email_address"); ok {
		if emailAddress := strings.TrimSpace(value); emailAddress != "" {
			status.EmailAddress = emailAddress
		}
	}
	if verified, ok := chatdNodeBoolValue(emailNode, "verified"); ok {
		status.EmailVerified = verified
	}
	if confirmed, ok := chatdNodeBoolValue(emailNode, "confirmed"); ok {
		status.EmailConfirmed = confirmed
	}
}

func accountProfilePictureSetResultFromIQ(node chatdNode) EngineAccountSettingsResult {
	picture, ok := chatdChild(node, "picture")
	if !ok {
		return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_ACCEPTED}
	}
	return EngineAccountSettingsResult{
		Status:           waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_ACCEPTED,
		ProfilePictureID: contactProfilePictureIDFromPicture(picture),
		HasStaging:       chatdNodeBool(picture, "has_staging"),
	}
}

func emailSetResultFromIQ(node chatdNode, emailAddress string) EngineAccountSettingsResult {
	emailNode, ok := chatdChild(node, "email")
	if !ok {
		return malformedAccountSettingsResult("WA set email response is missing email")
	}
	doVerify, ok := chatdNodeBoolValue(emailNode, "do_verify")
	if !ok {
		return malformedAccountSettingsResult("WA set email response is missing email verification status")
	}
	status := &waappv1.TwoFactorAuthStatus{EmailConfigured: true, EmailAddress: strings.TrimSpace(emailAddress)}
	if doVerify {
		return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_NEEDS_VERIFICATION, TwoFactorStatus: status}
	}
	autoVerifyStatus := chatdNodeStatus(emailNode, "auto_verify")
	if autoVerifyStatus == "success" || chatdNodeBool(emailNode, "verified") || chatdNodeBool(emailNode, "confirmed") {
		status.EmailVerified = true
		status.EmailConfirmed = chatdNodeBool(emailNode, "confirmed")
		return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_VERIFIED, TwoFactorStatus: status}
	}
	return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_REJECTED, Err: NewError(waappv1.WaErrorCode_WA_ERROR_CODE_REJECTED, "WA set email was not accepted for verification", false)}
}

func emailOtpRequestResultFromIQ(node chatdNode) EngineAccountSettingsResult {
	verifyNode, ok := chatdChild(node, "verify_email")
	if !ok {
		return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_WAITING}
	}
	return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_WAITING, WaitTime: chatdNodeDuration(verifyNode, "wait_time")}
}

func emailOtpVerifyResultFromIQ(node chatdNode) EngineAccountSettingsResult {
	verifyNode, ok := chatdChild(node, "verify_email")
	if !ok {
		return malformedAccountSettingsResult("WA email OTP verify response is missing verify_email")
	}
	codeMatch, ok := chatdNodeBoolValue(verifyNode, "code_match")
	if !ok {
		return malformedAccountSettingsResult("WA email OTP verify response is missing code_match")
	}
	status := waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_CODE_MISMATCH
	if codeMatch {
		status = waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_VERIFIED
	}
	result := EngineAccountSettingsResult{Status: status, WaitTime: chatdNodeDuration(verifyNode, "wait_time")}
	if status == waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_VERIFIED {
		result.TwoFactorStatus = &waappv1.TwoFactorAuthStatus{EmailConfigured: true, EmailVerified: true}
	}
	return result
}

func malformedAccountSettingsResult(message string) EngineAccountSettingsResult {
	return EngineAccountSettingsResult{Status: waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_REJECTED, Err: NewError(waappv1.WaErrorCode_WA_ERROR_CODE_REJECTED, message, false)}
}

func accountSettingsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	for _, marker := range []string{"timeout", "deadline", "proxy", "dial", "connect", "network", "tls", "no such host", "connection refused", "temporary"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
