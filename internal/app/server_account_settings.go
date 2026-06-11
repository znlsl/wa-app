package app

import (
	"context"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	accountProfileNameMaxRunes = 25
)

func (s *Server) GetTwoFactorAuthStatus(ctx context.Context, req *waappv1.GetTwoFactorAuthStatusRequest) (*waappv1.GetTwoFactorAuthStatusResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.GetTwoFactorAuthStatusResponse{Error: ToProtoError(err)}, nil
	}
	result, err := s.queryAccountSettings(ctx, req.GetContext(), req.GetSelector(), waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_TWO_FACTOR_AUTH_STATUS_GET)
	if err != nil {
		return &waappv1.GetTwoFactorAuthStatusResponse{Error: ToProtoError(err)}, nil
	}
	if result.Err != nil {
		return &waappv1.GetTwoFactorAuthStatusResponse{Error: ToProtoError(result.Err)}, nil
	}
	status := result.TwoFactorStatus
	if status == nil {
		status = &waappv1.TwoFactorAuthStatus{}
	}
	return &waappv1.GetTwoFactorAuthStatusResponse{Status: status}, nil
}

func (s *Server) SetTwoFactorAuthSettings(ctx context.Context, req *waappv1.SetTwoFactorAuthSettingsRequest) (*waappv1.SetTwoFactorAuthSettingsResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.SetTwoFactorAuthSettingsResponse{Error: ToProtoError(err)}, nil
	}
	pin, err := accountSettingsSensitiveValue(req.GetPin(), "pin", true)
	if err != nil {
		return &waappv1.SetTwoFactorAuthSettingsResponse{Error: ToProtoError(err)}, nil
	}
	pin, err = requireSixDigits(pin, "pin")
	if err != nil {
		return &waappv1.SetTwoFactorAuthSettingsResponse{Error: ToProtoError(err)}, nil
	}
	op, err := s.applyAccountSettings(ctx, req.GetContext(), req.GetSelector(), waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_TWO_FACTOR_AUTH_SETTINGS, func(input EngineAccountSettingsInput) EngineAccountSettingsInput {
		input.Pin = pin
		return input
	})
	if err != nil {
		return &waappv1.SetTwoFactorAuthSettingsResponse{Error: ToProtoError(err)}, nil
	}
	return &waappv1.SetTwoFactorAuthSettingsResponse{Operation: op, Error: op.GetError()}, nil
}

func (s *Server) SetAccountEmail(ctx context.Context, req *waappv1.SetAccountEmailRequest) (*waappv1.SetAccountEmailResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.SetAccountEmailResponse{Error: ToProtoError(err)}, nil
	}
	emailAddress, err := requiredEmailAddress(req.GetEmailAddress(), "email_address")
	if err != nil {
		return &waappv1.SetAccountEmailResponse{Error: ToProtoError(err)}, nil
	}
	googleIDToken, err := accountSettingsSensitiveValue(req.GetGoogleIdToken(), "google_id_token", false)
	if err != nil {
		return &waappv1.SetAccountEmailResponse{Error: ToProtoError(err)}, nil
	}
	op, err := s.applyAccountSettings(ctx, req.GetContext(), req.GetSelector(), waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_EMAIL_SET, func(input EngineAccountSettingsInput) EngineAccountSettingsInput {
		input.EmailAddress = emailAddress
		input.GoogleIDToken = googleIDToken
		return input
	})
	if err != nil {
		return &waappv1.SetAccountEmailResponse{Error: ToProtoError(err)}, nil
	}
	return &waappv1.SetAccountEmailResponse{Operation: op, Error: op.GetError()}, nil
}

func (s *Server) RequestAccountEmailOtp(ctx context.Context, req *waappv1.RequestAccountEmailOtpRequest) (*waappv1.RequestAccountEmailOtpResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.RequestAccountEmailOtpResponse{Error: ToProtoError(err)}, nil
	}
	op, err := s.applyAccountSettings(ctx, req.GetContext(), req.GetSelector(), waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_EMAIL_OTP_REQUEST, func(input EngineAccountSettingsInput) EngineAccountSettingsInput {
		input.LocaleLanguage = accountSettingsLocale(req.GetLocaleLanguage(), "en")
		input.LocaleCountry = accountSettingsLocale(req.GetLocaleCountry(), "US")
		return input
	})
	if err != nil {
		return &waappv1.RequestAccountEmailOtpResponse{Error: ToProtoError(err)}, nil
	}
	return &waappv1.RequestAccountEmailOtpResponse{Operation: op, Error: op.GetError()}, nil
}

func (s *Server) VerifyAccountEmailOtp(ctx context.Context, req *waappv1.VerifyAccountEmailOtpRequest) (*waappv1.VerifyAccountEmailOtpResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.VerifyAccountEmailOtpResponse{Error: ToProtoError(err)}, nil
	}
	code, err := accountSettingsSensitiveValue(req.GetCode(), "code", true)
	if err != nil {
		return &waappv1.VerifyAccountEmailOtpResponse{Error: ToProtoError(err)}, nil
	}
	code, err = requireSixDigits(code, "code")
	if err != nil {
		return &waappv1.VerifyAccountEmailOtpResponse{Error: ToProtoError(err)}, nil
	}
	op, err := s.applyAccountSettings(ctx, req.GetContext(), req.GetSelector(), waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_EMAIL_OTP_VERIFY, func(input EngineAccountSettingsInput) EngineAccountSettingsInput {
		input.Code = code
		return input
	})
	if err != nil {
		return &waappv1.VerifyAccountEmailOtpResponse{Error: ToProtoError(err)}, nil
	}
	return &waappv1.VerifyAccountEmailOtpResponse{Operation: op, Error: op.GetError()}, nil
}

func (s *Server) SetAccountProfileName(ctx context.Context, req *waappv1.SetAccountProfileNameRequest) (*waappv1.SetAccountProfileNameResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.SetAccountProfileNameResponse{Error: ToProtoError(err)}, nil
	}
	displayName, err := requiredAccountProfileName(req.GetDisplayName())
	if err != nil {
		return &waappv1.SetAccountProfileNameResponse{Error: ToProtoError(err)}, nil
	}
	op, err := s.applyAccountSettings(ctx, req.GetContext(), req.GetSelector(), waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_PROFILE_NAME_SET, func(input EngineAccountSettingsInput) EngineAccountSettingsInput {
		input.DisplayName = displayName
		return input
	})
	if err != nil {
		return &waappv1.SetAccountProfileNameResponse{Error: ToProtoError(err)}, nil
	}
	return &waappv1.SetAccountProfileNameResponse{Operation: op, Error: op.GetError()}, nil
}

func (s *Server) SetAccountProfilePicture(ctx context.Context, req *waappv1.SetAccountProfilePictureRequest) (*waappv1.SetAccountProfilePictureResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.SetAccountProfilePictureResponse{Error: ToProtoError(err)}, nil
	}
	image, err := requiredAccountProfilePicture(req.GetImage(), req.GetContentType())
	if err != nil {
		return &waappv1.SetAccountProfilePictureResponse{Error: ToProtoError(err)}, nil
	}
	contentType, _ := profilePictureContentType(image, req.GetContentType())
	op, result, err := s.applyAccountSettingsResult(ctx, req.GetContext(), req.GetSelector(), waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_PROFILE_PICTURE_SET, func(input EngineAccountSettingsInput) EngineAccountSettingsInput {
		input.ProfilePicture = image
		return input
	})
	if err != nil {
		return &waappv1.SetAccountProfilePictureResponse{Error: ToProtoError(err)}, nil
	}
	if op.GetError() == nil {
		s.cacheWAAccountProfilePicture(ctx, op.GetWaAccountId(), WAContactProfilePicture{ProfilePictureID: result.ProfilePictureID, ContentType: contentType, Data: image})
	}
	return &waappv1.SetAccountProfilePictureResponse{Operation: op, ProfilePictureId: result.ProfilePictureID, HasStaging: result.HasStaging, Error: op.GetError()}, nil
}

func (s *Server) RemoveAccountProfilePicture(ctx context.Context, req *waappv1.RemoveAccountProfilePictureRequest) (*waappv1.RemoveAccountProfilePictureResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.RemoveAccountProfilePictureResponse{Error: ToProtoError(err)}, nil
	}
	op, err := s.applyAccountSettings(ctx, req.GetContext(), req.GetSelector(), waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_PROFILE_PICTURE_REMOVE, nil)
	if err != nil {
		return &waappv1.RemoveAccountProfilePictureResponse{Error: ToProtoError(err)}, nil
	}
	if op.GetError() == nil {
		s.deleteWAAccountProfilePictureCache(ctx, op.GetWaAccountId())
	}
	return &waappv1.RemoveAccountProfilePictureResponse{Operation: op, Error: op.GetError()}, nil
}

func (s *Server) applyAccountSettings(ctx context.Context, requestContext *waappv1.RequestContext, selector *waappv1.AccountLoginSelector, kind waappv1.AccountSettingsOperationKind, enrich func(EngineAccountSettingsInput) EngineAccountSettingsInput) (*waappv1.AccountSettingsOperation, error) {
	op, _, err := s.applyAccountSettingsResult(ctx, requestContext, selector, kind, enrich)
	return op, err
}

func (s *Server) applyAccountSettingsResult(ctx context.Context, requestContext *waappv1.RequestContext, selector *waappv1.AccountLoginSelector, kind waappv1.AccountSettingsOperationKind, enrich func(EngineAccountSettingsInput) EngineAccountSettingsInput) (*waappv1.AccountSettingsOperation, EngineAccountSettingsResult, error) {
	loginState, err := s.accountSettingsLoginState(ctx, selector)
	if err != nil {
		return nil, EngineAccountSettingsResult{}, err
	}
	input := EngineAccountSettingsInput{
		WAAccountID:          loginState.GetWaAccountId(),
		ClientProfileID:      loginState.GetClientProfileId(),
		RegisteredIdentityID: loginState.GetRegisteredIdentityId(),
		LoginStateID:         loginState.GetLoginStateId(),
		Kind:                 kind,
	}
	if enrich != nil {
		input = enrich(input)
	}
	runner, release, err := s.accountSettingsRunner(ctx, requestContext, kind)
	if err != nil {
		return nil, EngineAccountSettingsResult{}, err
	}
	defer release()
	result := runner.ApplyAccountSettings(ctx, input)
	completedAt := s.clock.Now()
	op := &waappv1.AccountSettingsOperation{
		AccountSettingsOperationId: s.ids.NewID("waacctset_"),
		WaAccountId:                loginState.GetWaAccountId(),
		ClientProfileId:            loginState.GetClientProfileId(),
		LoginStateId:               loginState.GetLoginStateId(),
		RegisteredIdentityId:       loginState.GetRegisteredIdentityId(),
		Kind:                       kind,
		Status:                     accountSettingsStatus(kind, result),
		CompletedAt:                timestamppb.New(completedAt),
		Error:                      ToProtoError(result.Err),
	}
	if result.WaitTime > 0 {
		op.WaitTime = durationpb.New(result.WaitTime)
	}
	return op, result, nil
}

func (s *Server) queryAccountSettings(ctx context.Context, requestContext *waappv1.RequestContext, selector *waappv1.AccountLoginSelector, kind waappv1.AccountSettingsOperationKind) (EngineAccountSettingsResult, error) {
	loginState, err := s.accountSettingsLoginState(ctx, selector)
	if err != nil {
		return EngineAccountSettingsResult{}, err
	}
	runner, release, err := s.accountSettingsRunner(ctx, requestContext, kind)
	if err != nil {
		return EngineAccountSettingsResult{}, err
	}
	defer release()
	return runner.ApplyAccountSettings(ctx, EngineAccountSettingsInput{
		WAAccountID:          loginState.GetWaAccountId(),
		ClientProfileID:      loginState.GetClientProfileId(),
		RegisteredIdentityID: loginState.GetRegisteredIdentityId(),
		LoginStateID:         loginState.GetLoginStateId(),
		Kind:                 kind,
	}), nil
}

func (s *Server) accountSettingsRunner(ctx context.Context, requestContext *waappv1.RequestContext, kind waappv1.AccountSettingsOperationKind) (ProtocolEngine, func(), error) {
	runner := s.runner
	native, ok := runner.(*NativeEngine)
	if !ok || !accountSettingsUsesGatewayProxy(kind) {
		return runner, func() {}, nil
	}
	proxied, release, _ := s.optionalGatewayProxyEngine(ctx, native, gatewayProxyEngineRequest{
		Username:      s.accountSettingsProxyUsername,
		Purpose:       "WA_ACCOUNT_SETTINGS",
		CorrelationID: firstNonEmpty(requestContext.GetCorrelationId(), requestContext.GetRequestId()),
		TTL:           defaultAccountIQTimeout + 10*time.Second,
		Mode:          DynamicProxySessionModeSticky,
	})
	return proxied, release, nil
}

func accountSettingsUsesGatewayProxy(kind waappv1.AccountSettingsOperationKind) bool {
	return kind != waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_UNSPECIFIED
}

func (s *Server) accountSettingsLoginState(ctx context.Context, selector *waappv1.AccountLoginSelector) (*waappv1.LoginState, error) {
	if selector.GetLoginStateId() != "" {
		return requireActiveLoginState(func() (*waappv1.LoginState, error) {
			return s.store.GetLoginState(ctx, selector.GetLoginStateId())
		})
	}
	if selector.GetRegisteredIdentityId() != "" {
		return requireActiveLoginState(func() (*waappv1.LoginState, error) {
			return s.store.GetLoginStateByRegisteredIdentity(ctx, selector.GetRegisteredIdentityId())
		})
	}
	accountID, err := requireWAAccountID(selector.GetWaAccountId())
	if err != nil {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "login_state_id, registered_identity_id, or wa_account_id is required", false)
	}
	if selector.GetClientProfileId() != "" {
		return requireActiveLoginState(func() (*waappv1.LoginState, error) {
			return s.store.GetActiveLoginState(ctx, accountID, selector.GetClientProfileId())
		})
	}
	records, err := s.store.ListActiveLoginStates(ctx)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		loginState := record.LoginState
		if loginState.GetWaAccountId() == accountID {
			return loginState, nil
		}
	}
	return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_REGISTRATION_NOT_FOUND, "active login state not found", false)
}

func requireActiveLoginState(load func() (*waappv1.LoginState, error)) (*waappv1.LoginState, error) {
	loginState, err := load()
	if err != nil {
		return nil, err
	}
	if loginState.GetStatus() != waappv1.LoginStateStatus_LOGIN_STATE_STATUS_ACTIVE {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_CONFLICT, "login state is not active", false)
	}
	return loginState, nil
}

func accountSettingsStatus(kind waappv1.AccountSettingsOperationKind, result EngineAccountSettingsResult) waappv1.AccountSettingsOperationStatus {
	if result.Status != waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_UNSPECIFIED {
		return result.Status
	}
	if result.Err != nil {
		return waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_REJECTED
	}
	if kind == waappv1.AccountSettingsOperationKind_ACCOUNT_SETTINGS_OPERATION_KIND_ACCOUNT_EMAIL_OTP_REQUEST {
		return waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_WAITING
	}
	return waappv1.AccountSettingsOperationStatus_ACCOUNT_SETTINGS_OPERATION_STATUS_ACCEPTED
}

func accountSettingsSensitiveValue(value *waappv1.SensitiveText, label string, required bool) (string, error) {
	plain := strings.TrimSpace(value.GetValue())
	if plain != "" {
		return plain, nil
	}
	if strings.TrimSpace(value.GetSecretRef()) != "" {
		return "", NewError(waappv1.WaErrorCode_WA_ERROR_CODE_UNSUPPORTED_OPERATION, label+" secret_ref is not supported by native account settings", false)
	}
	if required {
		return "", NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, label+" is required", false)
	}
	return "", nil
}

func requireSixDigits(value string, label string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) != 6 || digitsOnly(trimmed) != trimmed {
		return "", NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, label+" must be 6 digits", false)
	}
	return trimmed, nil
}

func requiredEmailAddress(value string, label string) (string, error) {
	trimmed, err := optionalEmailAddress(value, label)
	if err != nil {
		return "", err
	}
	if trimmed == "" {
		return "", NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, label+" is required", false)
	}
	return trimmed, nil
}

func optionalEmailAddress(value string, label string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", nil
	}
	address, err := mail.ParseAddress(trimmed)
	if err != nil || address.Address != trimmed {
		return "", NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, label+" is invalid", false)
	}
	return trimmed, nil
}

func accountSettingsLocale(value string, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func requiredAccountProfileName(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "display_name is required", false)
	}
	if utf8.RuneCountInString(trimmed) > accountProfileNameMaxRunes {
		return "", NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "display_name is too long", false)
	}
	return trimmed, nil
}

func requiredAccountProfilePicture(image []byte, contentType string) ([]byte, error) {
	if len(image) == 0 {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "image is required", false)
	}
	if len(image) > profilePictureDownloadMaxBytes {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_REJECTED, "WA profile picture is too large", false)
	}
	if _, err := profilePictureContentType(image, contentType); err != nil {
		return nil, err
	}
	return append([]byte(nil), image...), nil
}
