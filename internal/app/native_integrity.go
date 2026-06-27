package app

import "strings"

type nativeIntegrityMode string

const (
	nativeIntegrityModeErrorCode        nativeIntegrityMode = "error_code"
	nativeIntegrityModePlayIntegrityAPI nativeIntegrityMode = "play_integrity_api"
)

func nativeIntegrityModeFromPayload(payload map[string]any) nativeIntegrityMode {
	mode := firstNonEmpty(
		textField(payload, "integrity_mode"),
		textField(payload, "integrityMode"),
		textField(payload, "gpia_mode"),
		textField(payload, "play_integrity_mode"),
		textField(objectField(payload, "registration"), "integrity_mode"),
	)
	return normalizeNativeIntegrityMode(mode)
}

func normalizeNativeIntegrityMode(value string) nativeIntegrityMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "error", "errorcode", "error_code", "error-code", "gpia_error_code":
		return nativeIntegrityModeErrorCode
	case "play_integrity", "play-integrity", "play_integrity_api", "play-integrity-api", "pi", "pi_api":
		return nativeIntegrityModePlayIntegrityAPI
	default:
		return nativeIntegrityModeErrorCode
	}
}

func (m nativeIntegrityMode) String() string {
	mode := normalizeNativeIntegrityMode(string(m))
	if mode == "" {
		return string(nativeIntegrityModeErrorCode)
	}
	return string(mode)
}
