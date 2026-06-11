package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
	"go.mozilla.org/pkcs7"
	"golang.org/x/crypto/pbkdf2"
)

const tokenSaltB64 = "PkTwKSZqUfAUyR0rPQ8hYJ0wNsQQ3dW1+3SCnyTXIfEAxxS75FwkDf47wNv/c8pP3p0GXKR6OOQmhyERwx74fw1RYSU10I4r1gyBVDbRJ40pidjM41G1I1oN"

var tokenLogoCandidates = []string{
	"res/drawable-hdpi/about_logo.png",
	"res/drawable-hdpi-v4/about_logo.png",
	"res/drawable-xxhdpi-v4/about_logo.png",
}

type ProtocolTooling interface {
	GeneratePhoneFingerprintProfile(context.Context, *waappv1.GeneratePhoneFingerprintProfileRequest) (*waappv1.PhoneFingerprintProfile, error)
	ImportWamsysCapture(context.Context, *waappv1.ImportWamsysCaptureRequest) (*waappv1.WamsysCapture, error)
	BuildRegistrationRequest(context.Context, *waappv1.BuildRegistrationRequestRequest) (*waappv1.BuildRegistrationRequestResponse, error)
	EncryptWASafeEnvelope(context.Context, *waappv1.EncryptWASafeEnvelopeRequest) (*waappv1.EncryptWASafeEnvelopeResponse, error)
	DeriveRegistrationToken(context.Context, *waappv1.DeriveRegistrationTokenRequest) (*waappv1.DeriveRegistrationTokenResponse, error)
	DeriveAuthKey(context.Context, *waappv1.DeriveAuthKeyRequest) (*waappv1.DeriveAuthKeyResponse, error)
}

func (s *Server) GeneratePhoneFingerprintProfile(ctx context.Context, req *waappv1.GeneratePhoneFingerprintProfileRequest) (*waappv1.GeneratePhoneFingerprintProfileResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.GeneratePhoneFingerprintProfileResponse{Error: ToProtoError(err)}, nil
	}
	tooling, err := s.tooling()
	if err != nil {
		return &waappv1.GeneratePhoneFingerprintProfileResponse{Error: ToProtoError(err)}, nil
	}
	profile, err := tooling.GeneratePhoneFingerprintProfile(ctx, req)
	return &waappv1.GeneratePhoneFingerprintProfileResponse{Profile: profile, Error: ToProtoError(err)}, nil
}

func (s *Server) ImportWamsysCapture(ctx context.Context, req *waappv1.ImportWamsysCaptureRequest) (*waappv1.ImportWamsysCaptureResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.ImportWamsysCaptureResponse{Error: ToProtoError(err)}, nil
	}
	tooling, err := s.tooling()
	if err != nil {
		return &waappv1.ImportWamsysCaptureResponse{Error: ToProtoError(err)}, nil
	}
	capture, err := tooling.ImportWamsysCapture(ctx, req)
	return &waappv1.ImportWamsysCaptureResponse{Capture: capture, Error: ToProtoError(err)}, nil
}

func (s *Server) BuildRegistrationRequest(ctx context.Context, req *waappv1.BuildRegistrationRequestRequest) (*waappv1.BuildRegistrationRequestResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.BuildRegistrationRequestResponse{Error: ToProtoError(err)}, nil
	}
	tooling, err := s.tooling()
	if err != nil {
		return &waappv1.BuildRegistrationRequestResponse{Error: ToProtoError(err)}, nil
	}
	resp, err := tooling.BuildRegistrationRequest(ctx, req)
	if resp == nil {
		resp = &waappv1.BuildRegistrationRequestResponse{}
	}
	resp.Error = ToProtoError(err)
	return resp, nil
}

func (s *Server) EncryptWASafeEnvelope(ctx context.Context, req *waappv1.EncryptWASafeEnvelopeRequest) (*waappv1.EncryptWASafeEnvelopeResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.EncryptWASafeEnvelopeResponse{Error: ToProtoError(err)}, nil
	}
	tooling, err := s.tooling()
	if err != nil {
		return &waappv1.EncryptWASafeEnvelopeResponse{Error: ToProtoError(err)}, nil
	}
	resp, err := tooling.EncryptWASafeEnvelope(ctx, req)
	if resp == nil {
		resp = &waappv1.EncryptWASafeEnvelopeResponse{}
	}
	resp.Error = ToProtoError(err)
	return resp, nil
}

func (s *Server) DeriveRegistrationToken(ctx context.Context, req *waappv1.DeriveRegistrationTokenRequest) (*waappv1.DeriveRegistrationTokenResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.DeriveRegistrationTokenResponse{Error: ToProtoError(err)}, nil
	}
	tooling, err := s.tooling()
	if err != nil {
		return &waappv1.DeriveRegistrationTokenResponse{Error: ToProtoError(err)}, nil
	}
	resp, err := tooling.DeriveRegistrationToken(ctx, req)
	if resp == nil {
		resp = &waappv1.DeriveRegistrationTokenResponse{}
	}
	resp.Error = ToProtoError(err)
	return resp, nil
}

func (s *Server) DeriveAuthKey(ctx context.Context, req *waappv1.DeriveAuthKeyRequest) (*waappv1.DeriveAuthKeyResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.DeriveAuthKeyResponse{Error: ToProtoError(err)}, nil
	}
	tooling, err := s.tooling()
	if err != nil {
		return &waappv1.DeriveAuthKeyResponse{Error: ToProtoError(err)}, nil
	}
	resp, err := tooling.DeriveAuthKey(ctx, req)
	if resp == nil {
		resp = &waappv1.DeriveAuthKeyResponse{}
	}
	resp.Error = ToProtoError(err)
	return resp, nil
}

func (s *Server) tooling() (ProtocolTooling, error) {
	tooling, ok := s.runner.(ProtocolTooling)
	if !ok {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_UNSUPPORTED_OPERATION, "protocol tooling is not available", false)
	}
	return tooling, nil
}

func (e *NativeEngine) GeneratePhoneFingerprintProfile(ctx context.Context, req *waappv1.GeneratePhoneFingerprintProfileRequest) (*waappv1.PhoneFingerprintProfile, error) {
	_ = ctx
	phone := normalizePhone(req.GetPhone())
	if phone.GetE164Number() == "" && phoneCC(phone) == "" {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "phone is required", false)
	}
	profile := buildNativePhoneProfile(phone)
	return phoneProfileToProto(phone, profile), nil
}

func (e *NativeEngine) ImportWamsysCapture(ctx context.Context, req *waappv1.ImportWamsysCaptureRequest) (*waappv1.WamsysCapture, error) {
	_ = ctx
	capture, err := parseWamsysJSON(req.GetJsonText())
	if err != nil {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "invalid WAMSYS capture", false)
	}
	return capture, nil
}

func (e *NativeEngine) BuildRegistrationRequest(ctx context.Context, req *waappv1.BuildRegistrationRequestRequest) (*waappv1.BuildRegistrationRequestResponse, error) {
	params := orderedParams{}
	rawKeys := map[string]struct{}{}
	phone := normalizePhone(req.GetPhone())
	var state nativeState
	var hasState bool
	if req.GetClientProfileId() != "" {
		loaded, err := e.loadState(ctx, req.GetClientProfileId())
		if err != nil {
			return nil, err
		}
		state = loaded
		state.ensureMaps()
		hasState = true
		phone.CountryCallingCode = firstNonEmpty(phone.GetCountryCallingCode(), state.CC)
		phone.NationalNumber = firstNonEmpty(phone.GetNationalNumber(), state.Phone)
		if phone.E164Number == "" && phone.CountryCallingCode != "" && phone.NationalNumber != "" {
			phone.E164Number = "+" + phone.CountryCallingCode + phone.NationalNumber
		}
	}
	if phoneCC(phone) == "" && phoneNational(phone) == "" {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "phone is required", false)
	}
	kind := req.GetKind()
	if kind == waappv1.RegistrationRequestKind_REGISTRATION_REQUEST_KIND_UNSPECIFIED {
		kind = waappv1.RegistrationRequestKind_REGISTRATION_REQUEST_KIND_CODE
	}
	method := registrationMethodFromName(req.GetMethod())
	methodName := registrationMethodName(method, "sms")
	language := firstNonEmpty(req.GetLanguage(), "en")
	locale := firstNonEmpty(req.GetLocale(), "US")
	switch kind {
	case waappv1.RegistrationRequestKind_REGISTRATION_REQUEST_KIND_EXIST:
		if hasState {
			base, raw := e.existParams(phone, state)
			params.merge(base, raw)
		} else {
			profile := buildNativePhoneProfile(phone)
			state = nativeState{CC: phoneCC(phone), Phone: phoneNational(phone), Profile: profile}
			params.set("cc", phoneCC(phone), false)
			params.set("in", phoneNational(phone), false)
			params.set("lg", language, false)
			params.set("lc", locale, false)
			applyNativeProfileParams(&params, rawKeys, profile, false, true)
			applyNativeRawMapParams(&params, rawKeys, existDeviceMap(nativeState{Profile: profile}), true)
		}
	case waappv1.RegistrationRequestKind_REGISTRATION_REQUEST_KIND_REGISTER:
		if strings.TrimSpace(req.GetVerificationCode()) == "" {
			return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "verification_code is required", false)
		}
		if hasState {
			base, raw := e.registerParams(phone, method, req.GetVerificationCode(), state)
			params.merge(base, raw)
		} else {
			profile := buildNativePhoneProfile(phone)
			state = nativeState{CC: phoneCC(phone), Phone: phoneNational(phone), Profile: profile}
			params.set("cc", phoneCC(phone), false)
			params.set("in", phoneNational(phone), false)
			params.set("method", methodName, false)
			params.set("code", req.GetVerificationCode(), false)
			applyNativeProfileParams(&params, rawKeys, profile, false, true)
			applyNativeRawMapParams(&params, rawKeys, registerDeviceMap(methodName, nativeState{Profile: profile}), true)
		}
	default:
		if hasState {
			base, raw := e.codeParams(phone, method, state)
			params.merge(base, raw)
		} else {
			profile := buildNativePhoneProfile(phone)
			state = nativeState{CC: phoneCC(phone), Phone: phoneNational(phone), Profile: profile}
			params.set("cc", phoneCC(phone), false)
			params.set("in", phoneNational(phone), false)
			params.set("method", methodName, false)
			params.set("lg", language, false)
			params.set("lc", locale, false)
			applyNativeProfileParams(&params, rawKeys, profile, false, true)
			applyNativeRawMapParams(&params, rawKeys, codeDeviceMap(methodName, nativeState{Profile: profile}), true)
		}
	}
	if kind != waappv1.RegistrationRequestKind_REGISTRATION_REQUEST_KIND_EXIST {
		params.set("method", firstNonEmpty(params.get("method"), methodName), false)
	}
	wamsysCapture, err := e.wamsysProvider().RegistrationMaterial(ctx, wamsysMaterialInput{Capture: req.GetWamsysCapture(), Kind: kind, Phone: phone, State: state})
	if err != nil {
		return nil, err
	}
	applyWamsysToParams(&params, rawKeys, wamsysCapture, req.GetApplyWamsysScalars(), req.GetIncludeWamsysMap(), req.GetIncludeWamsysIdBackup())
	for _, item := range req.GetExtraParams() {
		value := sensitiveInput(item.GetValue())
		params.set(item.GetKey(), value, item.GetRawPercentEncoded())
		if item.GetRawPercentEncoded() {
			rawKeys[item.GetKey()] = struct{}{}
		}
	}
	for _, key := range params.rawKeys() {
		rawKeys[key] = struct{}{}
	}
	plain := params.render()
	userAgent := nativeUserAgentForState(state, defaultWAAppVersion)
	resp := &waappv1.BuildRegistrationRequestResponse{RawParamKeys: sortedSet(rawKeys), UserAgent: userAgent, Headers: registrationHeaders(userAgent)}
	resp.Params = params.toProto(req.GetIncludeSensitiveValues())
	resp.Plaintext = sensitiveOutput(plain, "registration-plaintext", req.GetIncludeSensitiveValues())
	if req.GetEncryptRequest() {
		enc, err := encryptWASafe([]byte(plain), defaultWASafeServerPublicKeyHex)
		if err != nil {
			return nil, err
		}
		body := "ENC=" + enc
		resp.Body = sensitiveOutput(body, "registration-body", req.GetIncludeSensitiveValues())
		resp.EncSha256 = encHash(enc)
		resp.EncLength = int32(len(enc))
	}
	return resp, nil
}

func (e *NativeEngine) EncryptWASafeEnvelope(ctx context.Context, req *waappv1.EncryptWASafeEnvelopeRequest) (*waappv1.EncryptWASafeEnvelopeResponse, error) {
	_ = ctx
	plain := sensitiveInput(req.GetPlaintext())
	if plain == "" {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "plaintext is required", false)
	}
	enc, err := encryptWASafe([]byte(plain), firstNonEmpty(req.GetServerPublicKeyHex(), defaultWASafeServerPublicKeyHex))
	if err != nil {
		return nil, err
	}
	return &waappv1.EncryptWASafeEnvelopeResponse{Enc: sensitiveOutput(enc, "wasafe-enc", req.GetIncludeSensitiveValues()), EncSha256: encHash(enc), EncLength: int32(len(enc))}, nil
}

func (e *NativeEngine) DeriveRegistrationToken(ctx context.Context, req *waappv1.DeriveRegistrationTokenRequest) (*waappv1.DeriveRegistrationTokenResponse, error) {
	_ = ctx
	token, err := deriveRegistrationTokenFromAPK(req.GetApk(), phoneNational(normalizePhone(req.GetPhone())), firstNonEmpty(req.GetPackageName(), "com.whatsapp"))
	if err != nil {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "registration token derivation failed", false)
	}
	return &waappv1.DeriveRegistrationTokenResponse{Token: sensitiveOutput(token, "registration-token", req.GetIncludeSensitiveValues())}, nil
}

func (e *NativeEngine) DeriveAuthKey(ctx context.Context, req *waappv1.DeriveAuthKeyRequest) (*waappv1.DeriveAuthKeyResponse, error) {
	_ = ctx
	authkey, err := deriveAuthKeyFromKeystoreXML(req.GetKeystoreXml())
	if err != nil {
		return nil, NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "authkey derivation failed", false)
	}
	return &waappv1.DeriveAuthKeyResponse{Authkey: sensitiveOutput(authkey, "authkey", req.GetIncludeSensitiveValues())}, nil
}

type orderedParam struct {
	key string
	val string
	raw bool
}

type orderedParams []orderedParam

func (p *orderedParams) set(key string, value string, raw bool) {
	if strings.TrimSpace(key) == "" {
		return
	}
	for i := range *p {
		if (*p)[i].key == key {
			(*p)[i].val = value
			(*p)[i].raw = (*p)[i].raw || raw
			return
		}
	}
	*p = append(*p, orderedParam{key: key, val: value, raw: raw})
}

func (p *orderedParams) remove(key string) {
	for i := range *p {
		if (*p)[i].key == key {
			*p = append((*p)[:i], (*p)[i+1:]...)
			return
		}
	}
}

func (p *orderedParams) merge(values map[string]string, raw map[string]struct{}) {
	for _, key := range stableParamOrder(values) {
		_, isRaw := raw[key]
		p.set(key, values[key], isRaw)
	}
}

func (p orderedParams) get(key string) string {
	for _, item := range p {
		if item.key == key {
			return item.val
		}
	}
	return ""
}

func (p orderedParams) rawKeys() []string {
	out := []string{}
	for _, item := range p {
		if item.raw {
			out = append(out, item.key)
		}
	}
	return out
}

func (p orderedParams) render() string {
	parts := make([]string, 0, len(p))
	for _, item := range p {
		value := quoteForm(item.val)
		if item.raw {
			value = item.val
		}
		parts = append(parts, quoteForm(item.key)+"="+value)
	}
	return strings.Join(parts, "&")
}

func (p orderedParams) toProto(include bool) []*waappv1.RegistrationRequestParam {
	out := make([]*waappv1.RegistrationRequestParam, 0, len(p))
	for _, item := range p {
		out = append(out, &waappv1.RegistrationRequestParam{Key: item.key, Value: sensitiveOutput(item.val, "param:"+item.key, include), RawPercentEncoded: item.raw})
	}
	return out
}

func applyNativeProfileParams(params *orderedParams, rawKeys map[string]struct{}, profile nativePhoneProfile, includeMap bool, includeBackup bool) {
	params.set("fdid", profile.FDID, false)
	params.set("expid", profile.ExpID, false)
	params.set("access_session_id", profile.AccessSessionID, false)
	params.set("id", profile.ID, true)
	rawKeys["id"] = struct{}{}
	if includeBackup {
		params.set("backup_token", profile.BackupToken, true)
		rawKeys["backup_token"] = struct{}{}
	}
	if includeMap {
		keys := make([]string, 0, len(profile.AdditionalMapFields))
		for key := range profile.AdditionalMapFields {
			if isOpaqueWamsysMapKey(key) {
				continue
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			params.set(key, pctBytes([]byte(profile.AdditionalMapFields[key])), true)
			rawKeys[key] = struct{}{}
		}
	}
}

func applyNativeRawMapParams(params *orderedParams, rawKeys map[string]struct{}, values map[string]string, omitEmptyOperator bool) {
	for key, value := range values {
		if isOpaqueWamsysMapKey(key) {
			continue
		}
		if omitEmptyOperator && omitEmptyNativeOperatorField(key, value) {
			continue
		}
		if key == "token" {
			if value != "" {
				params.set(key, value, false)
			}
			continue
		}
		params.set(key, pctBytes([]byte(value)), true)
		rawKeys[key] = struct{}{}
	}
}

func applyWamsysToParams(params *orderedParams, rawKeys map[string]struct{}, capture *waappv1.WamsysCapture, scalars bool, includeMap bool, includeIDBackup bool) {
	if capture == nil {
		return
	}
	if scalars {
		for _, item := range capture.GetScalarFields() {
			switch item.GetFieldName() {
			case "A08":
				params.set("cc", item.GetValue(), false)
			case "A09":
				params.set("in", item.GetValue(), false)
			case "A07":
				params.set("method", item.GetValue(), false)
			case "A0A":
				params.set("token", item.GetValue(), false)
			}
		}
	}
	if includeIDBackup {
		for _, item := range capture.GetByteFields() {
			switch item.GetFieldName() {
			case "A0F":
				params.set("id", pctBytes(item.GetValue()), true)
				rawKeys["id"] = struct{}{}
			case "A0D":
				params.set("backup_token", pctBytes(item.GetValue()), true)
				rawKeys["backup_token"] = struct{}{}
			}
		}
	}
	if includeMap {
		for _, item := range capture.GetMapParams() {
			params.set(item.GetKey(), pctBytes(item.GetValue()), true)
			rawKeys[item.GetKey()] = struct{}{}
		}
	}
}

func phoneProfileToProto(phone *waappv1.PhoneTarget, profile nativePhoneProfile) *waappv1.PhoneFingerprintProfile {
	base := map[string]string{"fdid": profile.FDID, "expid": profile.ExpID, "expid_uuid": profile.ExpIDUUID, "access_session_id": profile.AccessSessionID, "access_session_id_uuid": profile.AccessSessionIDUUID}
	raw := map[string]string{"id": profile.ID, "id_hex": profile.IDHex, "backup_token": profile.BackupToken, "backup_token_hex": profile.BackupTokenHex}
	device := map[string]string{}
	for key, value := range profile.AdditionalMapFields {
		if isOpaqueWamsysMapKey(key) {
			continue
		}
		device[key] = value
	}
	canonical, _ := json.Marshal(struct {
		Schema  string            `json:"schema"`
		Phone   string            `json:"phone"`
		Vendor  string            `json:"vendor"`
		Model   string            `json:"model"`
		Android string            `json:"android"`
		Base    map[string]string `json:"base"`
		Raw     map[string]string `json:"raw"`
		Map     map[string]string `json:"map"`
	}{Schema: profile.Schema, Phone: phone.GetE164Number(), Vendor: profile.DeviceVendor, Model: profile.DeviceModel, Android: profile.AndroidVersion, Base: base, Raw: raw, Map: device})
	sum := sha256.Sum256(canonical)
	return &waappv1.PhoneFingerprintProfile{Schema: profile.Schema, Phone: phone, PhoneSha256: profile.PhoneSHA256, BaseParams: base, RawParams: raw, DeviceMapParams: device, ProfileSha256: hex.EncodeToString(sum[:])}
}

func parseWamsysJSON(text string) (*waappv1.WamsysCapture, error) {
	var root map[string]any
	if err := json.Unmarshal([]byte(text), &root); err != nil {
		return nil, err
	}
	out := &waappv1.WamsysCapture{}
	if scalars, ok := root["scalar_fields"].(map[string]any); ok {
		keys := make([]string, 0, len(scalars))
		for key := range scalars {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			obj, _ := scalars[key].(map[string]any)
			value, ok := obj["value"]
			if !ok || value == nil || fmt.Sprint(value) == "null" {
				continue
			}
			out.ScalarFields = append(out.ScalarFields, &waappv1.WamsysScalarField{FieldName: key, Value: fmt.Sprint(value)})
		}
	}
	if items, ok := root["map_params"].([]any); ok {
		for _, raw := range items {
			obj, _ := raw.(map[string]any)
			key := fmt.Sprint(obj["key"])
			hexValue := fmt.Sprint(obj["hex"])
			if key == "" || hexValue == "" {
				continue
			}
			value, err := hex.DecodeString(hexValue)
			if err != nil {
				return nil, err
			}
			out.MapParams = append(out.MapParams, &waappv1.WamsysMapParam{Key: key, Value: value})
		}
	}
	if fields, ok := root["byte_fields"].(map[string]any); ok {
		keys := make([]string, 0, len(fields))
		for key := range fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			obj, _ := fields[key].(map[string]any)
			hexValue := fmt.Sprint(obj["hex"])
			if hexValue == "" {
				continue
			}
			value, err := hex.DecodeString(hexValue)
			if err != nil {
				return nil, err
			}
			out.ByteFields = append(out.ByteFields, &waappv1.WamsysByteField{FieldName: key, Value: value})
		}
	}
	return out, nil
}

func registrationHeaders(userAgent string) map[string]string {
	return map[string]string{"Content-Type": "application/x-www-form-urlencoded", "User-Agent": userAgent, "WaMsysRequest": "1", "X-Forwarded-Host": defaultNativeHTTPHost, "request_token": strings.ToUpper(newUUIDString())}
}

func deriveRegistrationTokenFromAPK(apk []byte, phone string, packageName string) (string, error) {
	if len(apk) == 0 {
		return "", fmt.Errorf("apk is empty")
	}
	reader, err := zip.NewReader(bytes.NewReader(apk), int64(len(apk)))
	if err != nil {
		return "", err
	}
	files := map[string]*zip.File{}
	for _, f := range reader.File {
		files[f.Name] = f
	}
	var logo []byte
	for _, name := range tokenLogoCandidates {
		if f := files[name]; f != nil {
			logo, err = readZipFile(f)
			if err != nil {
				return "", err
			}
			break
		}
	}
	if logo == nil {
		return "", fmt.Errorf("about_logo.png not found")
	}
	classes, err := readZipFile(files["classes.dex"])
	if err != nil {
		return "", err
	}
	certDER, err := firstAPKCertDER(reader.File)
	if err != nil {
		return "", err
	}
	classesMD5 := md5.Sum(classes)
	salt, err := base64.StdEncoding.DecodeString(tokenSaltB64)
	if err != nil {
		return "", err
	}
	key := pbkdf2.Key(append([]byte(packageName), logo...), salt, 128, 64, sha1.New)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(certDER)
	_, _ = mac.Write(classesMD5[:])
	_, _ = mac.Write([]byte(phone))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

func firstAPKCertDER(files []*zip.File) ([]byte, error) {
	names := make([]string, 0, len(files))
	byName := map[string]*zip.File{}
	for _, file := range files {
		upper := strings.ToUpper(file.Name)
		if strings.HasPrefix(upper, "META-INF/") && (strings.HasSuffix(upper, ".RSA") || strings.HasSuffix(upper, ".DSA") || strings.HasSuffix(upper, ".EC")) {
			names = append(names, file.Name)
			byName[file.Name] = file
		}
	}
	sort.Strings(names)
	for _, name := range names {
		data, err := readZipFile(byName[name])
		if err != nil {
			return nil, err
		}
		p7, err := pkcs7.Parse(data)
		if err == nil && len(p7.Certificates) > 0 {
			return p7.Certificates[0].Raw, nil
		}
	}
	return nil, fmt.Errorf("APK signing certificate not found")
}

func readZipFile(file *zip.File) ([]byte, error) {
	if file == nil {
		return nil, fmt.Errorf("zip entry not found")
	}
	r, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

func deriveAuthKeyFromKeystoreXML(text string) (string, error) {
	values, err := readAndroidXMLStrings(text)
	if err != nil {
		return "", err
	}
	raw := values["client_static_keypair_pwd_enc"]
	if raw == "" {
		return "", fmt.Errorf("client_static_keypair_pwd_enc not found")
	}
	var arr []any
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		return "", err
	}
	if len(arr) < 5 || intFromAny(arr[0]) != 2 {
		return "", fmt.Errorf("unsupported keypair envelope")
	}
	ct, err := decodeB64Any(fmt.Sprint(arr[1]))
	if err != nil {
		return "", err
	}
	iv, err := decodeB64Any(fmt.Sprint(arr[2]))
	if err != nil {
		return "", err
	}
	salt, err := decodeB64Any(fmt.Sprint(arr[3]))
	if err != nil {
		return "", err
	}
	suffix := fmt.Sprint(arr[4])
	obf := "A\u0004\u001d@\u0011\u0018V\u0091\u0002\u0090\u0088\u009f\u009eT(3{;ES"
	var base strings.Builder
	for _, r := range obf {
		base.WriteRune(r ^ 0x12)
	}
	key := pbkdf2.Key([]byte(base.String()+suffix), salt, 16, 16, sha1.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	if len(iv) != aes.BlockSize {
		return "", fmt.Errorf("invalid OFB IV length")
	}
	plain := make([]byte, len(ct))
	cipher.NewOFB(block, iv).XORKeyStream(plain, ct)
	if len(plain) != 64 {
		return "", fmt.Errorf("unexpected keypair length")
	}
	return b64u(plain[32:]), nil
}

type xmlNode struct {
	XMLName xml.Name
	Name    string    `xml:"name,attr"`
	Value   string    `xml:"value,attr"`
	Text    string    `xml:",chardata"`
	Nodes   []xmlNode `xml:",any"`
}

func readAndroidXMLStrings(text string) (map[string]string, error) {
	var root xmlNode
	if err := xml.Unmarshal([]byte(text), &root); err != nil {
		return nil, err
	}
	out := map[string]string{}
	var walk func(xmlNode)
	walk = func(node xmlNode) {
		if node.Name != "" {
			value := node.Value
			if value == "" {
				value = strings.TrimSpace(node.Text)
			}
			out[node.Name] = value
		}
		for _, child := range node.Nodes {
			walk(child)
		}
	}
	walk(root)
	return out, nil
}

func sensitiveInput(value *waappv1.SensitiveText) string {
	if value == nil {
		return ""
	}
	return firstNonEmpty(value.GetValue(), value.GetRedactedValue())
}

func sensitiveOutput(value string, refPrefix string, include bool) *waappv1.SensitiveText {
	out := &waappv1.SensitiveText{RedactedValue: redacted(value), SecretRef: refPrefix + ":" + stableID(value)}
	if include {
		out.Value = value
	}
	return out
}

func sortedSet(values map[string]struct{}) []string {
	out := make([]string, 0, len(values))
	for key := range values {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		return 0
	}
}
