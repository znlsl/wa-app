package app

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	mrand "math/rand"
	"regexp"
	"sort"
	"strings"
	"time"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
)

const nativeStateSchema = "byte-v-forge-wa-app-native-state/v1"

type nativeState struct {
	Schema          string                          `json:"schema"`
	CreatedAtUnix   int64                           `json:"created_at_unix"`
	CC              string                          `json:"cc"`
	Phone           string                          `json:"phone"`
	AuthKey         string                          `json:"authkey"`
	UserAgent       string                          `json:"user_agent"`
	PushName        string                          `json:"push_name,omitempty"`
	Profile         nativePhoneProfile              `json:"profile"`
	KeyBundle       nativeKeyBundle                 `json:"key_bundle"`
	LastCodeParams  map[string]string               `json:"last_code_params,omitempty"`
	LastCodeResult  map[string]any                  `json:"last_code_result,omitempty"`
	LastRegister    map[string]any                  `json:"last_register,omitempty"`
	RegistrationJID string                          `json:"registration_jid,omitempty"`
	ChatRoutingInfo string                          `json:"chat_routing_info,omitempty"`
	ChatConnection  nativeChatConnectionState       `json:"chat_connection,omitempty"`
	ChatStatic      nativeCurveKeyPair              `json:"chat_static"`
	Signal          nativeSignalState               `json:"signal"`
	AppState        nativeAppState                  `json:"app_state,omitempty"`
	ContactHints    []waContactHint                 `json:"contact_hints,omitempty"`
	MessagePayloads map[string]nativeMessagePayload `json:"message_payloads,omitempty"`
	MessagePlainRef map[string]string               `json:"message_plain_ref,omitempty"`
	PrivacyTokens   map[string]nativePrivacyToken   `json:"privacy_tokens,omitempty"`
}

type nativePhoneProfile struct {
	Schema              string            `json:"schema"`
	CreatedAtUnix       int64             `json:"created_at_unix"`
	PhoneSHA256         string            `json:"phone_sha256"`
	UserAgent           string            `json:"user_agent"`
	FDID                string            `json:"fdid"`
	ExpID               string            `json:"expid"`
	ExpIDUUID           string            `json:"expid_uuid"`
	AccessSessionID     string            `json:"access_session_id"`
	AccessSessionIDUUID string            `json:"access_session_id_uuid"`
	ID                  string            `json:"id"`
	IDHex               string            `json:"id_hex"`
	BackupToken         string            `json:"backup_token"`
	BackupTokenHex      string            `json:"backup_token_hex"`
	AdditionalMapFields map[string]string `json:"additional_map_fields"`
}

type nativeKeyBundle struct {
	RegistrationID int32  `json:"registration_id"`
	SignedPreKeyID int32  `json:"signed_prekey_id"`
	IdentityPublic string `json:"e_ident"`
	KeyType        string `json:"e_keytype"`
	RegID          string `json:"e_regid"`
	SignedKeyID    string `json:"e_skey_id"`
	SignedKeyValue string `json:"e_skey_val"`
	SignedKeySig   string `json:"e_skey_sig"`
}

type nativeSignalPreKey struct {
	ID      int32              `json:"id"`
	KeyPair nativeCurveKeyPair `json:"key_pair"`
}

type nativeSignalState struct {
	RegistrationID   int32                          `json:"registration_id"`
	Identity         nativeCurveKeyPair             `json:"identity"`
	SignedPreKey     nativeSignalPreKey             `json:"signed_prekey"`
	OneTimePreKeys   []nativeSignalPreKey           `json:"one_time_prekeys,omitempty"`
	RemoteIdentities map[string]string              `json:"remote_identities,omitempty"`
	Sessions         map[string]nativeSignalSession `json:"sessions,omitempty"`
}

type nativeSignalSession struct {
	Sender               string                         `json:"sender"`
	Version              int                            `json:"version"`
	RemoteIdentityPublic string                         `json:"remote_identity_public"`
	RootKey              string                         `json:"root_key,omitempty"`
	SenderRatchetPublic  string                         `json:"sender_ratchet_public,omitempty"`
	SenderRatchetPrivate string                         `json:"sender_ratchet_private,omitempty"`
	SenderChain          *nativeSenderChain             `json:"sender_chain,omitempty"`
	ReceiverChains       map[string]nativeReceiverChain `json:"receiver_chains,omitempty"`
	PreviousCounter      *int                           `json:"previous_counter,omitempty"`
	RemoteRegistrationID *int                           `json:"remote_registration_id,omitempty"`
	AliceBaseKey         string                         `json:"alice_base_key,omitempty"`
}

type nativeAppState struct {
	Keys        map[string]nativeAppStateKey        `json:"keys,omitempty"`
	Collections map[string]nativeAppStateCollection `json:"collections,omitempty"`
}

type nativeAppStateKey struct {
	KeyID       string `json:"key_id"`
	KeyData     string `json:"key_data"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Timestamp   int64  `json:"timestamp,omitempty"`
}

type nativeAppStateCollection struct {
	Version        uint64            `json:"version,omitempty"`
	Hash           string            `json:"hash,omitempty"`
	IndexValueMACs map[string]string `json:"index_value_macs,omitempty"`
}

type nativeReceiverChain struct {
	RatchetKey string `json:"ratchet_key"`
	ChainKey   string `json:"chain_key"`
	Index      int    `json:"index"`
	RootKey    string `json:"root_key,omitempty"`
}

type nativeSenderChain struct {
	RatchetKey string `json:"ratchet_key"`
	ChainKey   string `json:"chain_key"`
	Index      int    `json:"index"`
}

type nativeMessagePayload struct {
	Contact             string          `json:"contact,omitempty"`
	Sender              string          `json:"sender,omitempty"`
	ContactPN           string          `json:"contact_pn,omitempty"`
	SenderPN            string          `json:"sender_pn,omitempty"`
	NotifyName          string          `json:"notify_name,omitempty"`
	ParticipantUsername string          `json:"participant_username,omitempty"`
	ContactHints        []waContactHint `json:"contact_hints,omitempty"`
	EncType             string          `json:"enc_type,omitempty"`
	Path                string          `json:"path,omitempty"`
	Payload             string          `json:"payload"`
}

type nativePrivacyToken struct {
	Token     string `json:"token"`
	Timestamp int64  `json:"timestamp,omitempty"`
}

type nativeChatConnectionState struct {
	LastHost           string `json:"last_host,omitempty"`
	LastPort           int    `json:"last_port,omitempty"`
	ServerStaticPublic string `json:"server_static_public,omitempty"`
}

func (s nativeState) codeParams() map[string]string {
	params := map[string]string{}
	for k, v := range s.LastCodeParams {
		params[k] = v
	}
	return params
}

func (s *nativeState) ensureMaps() {
	if s.MessagePayloads == nil {
		s.MessagePayloads = map[string]nativeMessagePayload{}
	}
	if s.MessagePlainRef == nil {
		s.MessagePlainRef = map[string]string{}
	}
	if s.PrivacyTokens == nil {
		s.PrivacyTokens = map[string]nativePrivacyToken{}
	}
	if s.Signal.RemoteIdentities == nil {
		s.Signal.RemoteIdentities = map[string]string{}
	}
	if s.Signal.Sessions == nil {
		s.Signal.Sessions = map[string]nativeSignalSession{}
	}
	if s.AppState.Keys == nil {
		s.AppState.Keys = map[string]nativeAppStateKey{}
	}
	if s.AppState.Collections == nil {
		s.AppState.Collections = map[string]nativeAppStateCollection{}
	}
}

func buildNativeOneTimePreKeys(count int) []nativeSignalPreKey {
	out := make([]nativeSignalPreKey, 0, count)
	for i := 0; i < count; i++ {
		kp, err := newNativeCurveKeyPair()
		if err != nil {
			break
		}
		out = append(out, nativeSignalPreKey{ID: int32(i + 1), KeyPair: kp})
	}
	return out
}

func marshalNativeState(state nativeState) ([]byte, error) {
	return json.MarshalIndent(state, "", "  ")
}

func unmarshalNativeState(data []byte) (nativeState, error) {
	var state nativeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nativeState{}, err
	}
	state.ensureMaps()
	return state, nil
}

func newNativeState(phone *waappv1.PhoneTarget, appVersion string) (nativeState, error) {
	chatStatic, err := newNativeCurveKeyPair()
	if err != nil {
		return nativeState{}, err
	}
	identity, err := newNativeCurveKeyPair()
	if err != nil {
		return nativeState{}, err
	}
	signedPreKey, err := newNativeCurveKeyPair()
	if err != nil {
		return nativeState{}, err
	}
	regID := randomInt32()
	spkID := randomInt24()
	identityPublic, err := identity.publicBytes()
	if err != nil {
		return nativeState{}, err
	}
	identityPrivate, err := identity.privateBytes()
	if err != nil {
		return nativeState{}, err
	}
	signedPublic, err := signedPreKey.publicBytes()
	if err != nil {
		return nativeState{}, err
	}
	signedPublicWithPrefix, err := withSignalCurvePrefix(signedPublic)
	if err != nil {
		return nativeState{}, err
	}
	signature, err := xeddsaSignCurve25519(identityPrivate, signedPublicWithPrefix)
	if err != nil {
		return nativeState{}, err
	}
	verified, err := xeddsaVerifyCurve25519(identityPublic, signedPublicWithPrefix, signature)
	if err != nil {
		return nativeState{}, err
	}
	if !verified {
		return nativeState{}, fmt.Errorf("generated signed prekey signature did not verify")
	}
	profile := buildNativePhoneProfile(phone, appVersion)
	state := nativeState{
		Schema:        nativeStateSchema,
		CreatedAtUnix: time.Now().UTC().Unix(),
		CC:            phoneCC(phone),
		Phone:         phoneNational(phone),
		AuthKey:       chatStatic.Public,
		UserAgent:     firstNonEmpty(profile.UserAgent, nativeUserAgent(appVersion)),
		Profile:       profile,
		ChatStatic:    chatStatic,
		Signal: nativeSignalState{
			RegistrationID:   regID,
			Identity:         identity,
			SignedPreKey:     nativeSignalPreKey{ID: spkID, KeyPair: signedPreKey},
			OneTimePreKeys:   buildNativeOneTimePreKeys(20),
			RemoteIdentities: map[string]string{},
			Sessions:         map[string]nativeSignalSession{},
		},
		KeyBundle: nativeKeyBundle{
			RegistrationID: regID,
			SignedPreKeyID: spkID,
			IdentityPublic: b64u(identityPublic),
			KeyType:        b64u([]byte{0x05}),
			RegID:          b64u(binary.BigEndian.AppendUint32(nil, uint32(regID))),
			SignedKeyID:    b64u([]byte{byte(spkID >> 16), byte(spkID >> 8), byte(spkID)}),
			SignedKeyValue: b64u(signedPublic),
			SignedKeySig:   b64u(signature),
		},
		MessagePayloads: map[string]nativeMessagePayload{},
		MessagePlainRef: map[string]string{},
	}
	return state, nil
}

type nativeDeviceModel struct {
	Vendor    string
	Model     string
	Android   string
	MinRAMGiB float64
	MaxRAMGiB float64
}

var nativeDeviceModels = []nativeDeviceModel{
	{Vendor: "HUAWEI", Model: "TRT-AL00A", Android: "7.0", MinRAMGiB: 2.8, MaxRAMGiB: 3.9},
	{Vendor: "Xiaomi", Model: "M2007J3SC", Android: "11", MinRAMGiB: 5.5, MaxRAMGiB: 7.8},
	{Vendor: "samsung", Model: "SM-G991B", Android: "13", MinRAMGiB: 6.8, MaxRAMGiB: 7.6},
	{Vendor: "OPPO", Model: "CPH2305", Android: "12", MinRAMGiB: 3.6, MaxRAMGiB: 7.4},
	{Vendor: "vivo", Model: "V2145A", Android: "12", MinRAMGiB: 5.5, MaxRAMGiB: 7.7},
}

var nativeOperators = map[string][][2]string{
	"US":   {{"310", "260"}, {"310", "410"}, {"311", "480"}},
	"CN":   {{"460", "00"}, {"460", "01"}, {"460", "11"}},
	"PL":   {{"260", "01"}, {"260", "02"}, {"260", "06"}},
	"NONE": {{"", ""}},
}

var nativeRadioTypes = []string{"1", "2", "3", "9", "13", "20"}

func buildNativePhoneProfile(phone *waappv1.PhoneTarget, appVersion string) nativePhoneProfile {
	seed := int64(binary.BigEndian.Uint64(randomBytes(8)))
	rng := mrand.New(mrand.NewSource(seed))
	model := nativeDeviceModels[rng.Intn(len(nativeDeviceModels))]
	country := strings.ToUpper(strings.TrimSpace(phone.GetCountryIso2()))
	ops := nativeOperators[country]
	if len(ops) == 0 {
		ops = nativeOperators["NONE"]
	}
	op := ops[rng.Intn(len(ops))]
	simOp := ops[rng.Intn(len(ops))]
	expIDUUID, expID := uuidPair()
	accessUUID, accessSessionID := uuidPair()
	id := randomBytes(20)
	backup := randomBytes(20)
	phoneHash := sha256.Sum256([]byte(fullPhoneKey(phoneCC(phone), phoneNational(phone))))
	simnum := "0"
	if op[0] != "" && rng.Intn(2) == 1 {
		simnum = "1"
	}
	ram := model.MinRAMGiB + rng.Float64()*(model.MaxRAMGiB-model.MinRAMGiB)
	additionalFields := map[string]string{
		"network_radio_type":    "1",
		"pid":                   fmt.Sprintf("%d", 10000+rng.Intn(50000)),
		"simnum":                simnum,
		"hasinrc":               "1",
		"rc":                    "0",
		"device_ram":            fmt.Sprintf("%.2f", ram),
		"db":                    "1",
		"recaptcha":             `{"stage":"ABPROP_DISABLED"}`,
		"feo2_query_status":     "error_security_exception",
		"network_operator_name": "",
		"sim_operator_name":     "",
	}
	if op[0] != "" {
		additionalFields["mcc"] = op[0]
		additionalFields["mnc"] = op[1]
	}
	if simOp[0] != "" {
		additionalFields["sim_mcc"] = simOp[0]
		additionalFields["sim_mnc"] = simOp[1]
	}
	return nativePhoneProfile{
		Schema:              "ctf-whatsapp-phone-profile/v1",
		CreatedAtUnix:       time.Now().UTC().Unix(),
		PhoneSHA256:         hex.EncodeToString(phoneHash[:]),
		UserAgent:           fmt.Sprintf("WhatsApp/%s Android/%s Device/%s-%s", firstNonEmpty(appVersion, defaultWAAppVersion), model.Android, model.Vendor, model.Model),
		FDID:                newUUIDString(),
		ExpID:               expID,
		ExpIDUUID:           expIDUUID,
		AccessSessionID:     accessSessionID,
		AccessSessionIDUUID: accessUUID,
		ID:                  pctBytes(id),
		IDHex:               hex.EncodeToString(id),
		BackupToken:         pctBytes(backup),
		BackupTokenHex:      hex.EncodeToString(backup),
		AdditionalMapFields: additionalFields,
	}
}

func withNativeAppVersion(state nativeState, appVersion string) nativeState {
	if strings.TrimSpace(appVersion) == "" {
		appVersion = defaultWAAppVersion
	}
	state.UserAgent = withNativeUserAgentVersion(state.UserAgent, appVersion)
	state.Profile.UserAgent = withNativeUserAgentVersion(state.Profile.UserAgent, appVersion)
	if state.UserAgent == "" {
		state.UserAgent = firstNonEmpty(state.Profile.UserAgent, nativeUserAgent(appVersion))
	}
	if state.Profile.UserAgent == "" {
		state.Profile.UserAgent = state.UserAgent
	}
	return state
}

func withNativeUserAgentVersion(userAgent string, appVersion string) string {
	if strings.TrimSpace(userAgent) == "" {
		return ""
	}
	parts := strings.SplitN(userAgent, " ", 2)
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "WhatsApp/") {
		return userAgent
	}
	if len(parts) == 1 {
		return "WhatsApp/" + appVersion
	}
	return "WhatsApp/" + appVersion + " " + parts[1]
}

func uuidPair() (string, string) {
	raw := randomBytes(16)
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	text := fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:])
	return text, b64u(raw)
}

func randomBytes(length int) []byte {
	out := make([]byte, length)
	_, _ = rand.Read(out)
	return out
}

func randomInt32() int32 {
	max := big.NewInt(0x7ffffffe)
	value, err := rand.Int(rand.Reader, max)
	if err != nil {
		return int32(time.Now().UnixNano() & 0x7fffffff)
	}
	return int32(value.Int64() + 1)
}

func randomInt24() int32 {
	max := big.NewInt(0xfffffe)
	value, err := rand.Int(rand.Reader, max)
	if err != nil {
		return int32(time.Now().UnixNano() & 0xffffff)
	}
	return int32(value.Int64() + 1)
}

func newUUIDString() string {
	raw := randomBytes(16)
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:])
}

func uuidB64u() string {
	raw := randomBytes(16)
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return b64u(raw)
}

func b64u(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

func b64Std(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}

var formSafe = map[byte]bool{}

func init() {
	for _, ch := range []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-._~") {
		formSafe[ch] = true
	}
}

func pctBytes(raw []byte) string {
	var b strings.Builder
	for _, ch := range raw {
		if formSafe[ch] {
			b.WriteByte(ch)
		} else {
			b.WriteByte('%')
			b.WriteString(strings.ToUpper(hex.EncodeToString([]byte{ch})))
		}
	}
	return b.String()
}

func quoteForm(value string) string {
	return pctBytes([]byte(value))
}

func renderNativePlain(params map[string]string, rawKeys map[string]struct{}) string {
	keys := stableParamOrder(params)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := params[key]
		encodedValue := quoteForm(value)
		if _, ok := rawKeys[key]; ok {
			encodedValue = value
		}
		parts = append(parts, quoteForm(key)+"="+encodedValue)
	}
	return strings.Join(parts, "&")
}

func stableParamOrder(params map[string]string) []string {
	preferred := []string{
		"cc", "in", "method", "lg", "lc", "fdid", "expid", "access_session_id",
		"id", "backup_token", "code", "auth_response", "context", "advertising_id",
		"login", "type", "token", "authkey", "e_ident", "e_keytype", "e_regid",
		"e_skey_id", "e_skey_val", "e_skey_sig",
		"mistyped", "reason", "hasav", "offline_ab", "client_metrics", "entered",
		"read_phone_permission_granted", "sim_state", "network_operator_name",
		"sim_operator_name", "device_name", "backup_token_error", "mcc", "mnc",
		"sim_mcc", "sim_mnc", "education_screen_displayed", "prefer_sms_over_flash",
		"network_radio_type", "simnum", "hasinrc", "pid", "rc", "device_ram", "gpia",
		"db", "recaptcha", "_ge", "_gi", "_gg", "_gp", "_ga", "aid",
		"feo2_query_status", "is_foa_fdid_app_installed", "language_selector_time_spent",
		"language_selector_clicked_count",
	}
	seen := map[string]struct{}{}
	out := []string{}
	for _, key := range preferred {
		if _, ok := params[key]; ok {
			out = append(out, key)
			seen[key] = struct{}{}
		}
	}
	rest := make([]string, 0)
	for key := range params {
		if _, ok := seen[key]; !ok {
			rest = append(rest, key)
		}
	}
	sort.Strings(rest)
	out = append(out, rest...)
	return out
}

func nativeUserAgent(appVersion string) string {
	if strings.TrimSpace(appVersion) == "" {
		appVersion = defaultWAAppVersion
	}
	return "WhatsApp/" + appVersion + " Android/7.0 Device/HUAWEI-TRT-AL00A"
}

func parseJSONMap(text string) map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal([]byte(text), &out)
	return out
}

func responseStatus(data map[string]any) string {
	if value, ok := data["status"].(string); ok {
		return strings.ToLower(value)
	}
	return ""
}

func fullPhoneKey(cc string, phone string) string {
	compact := regexp.MustCompile(`\D+`).ReplaceAllString(cc+phone, "")
	if compact == "" {
		return hex.EncodeToString(sha256.New().Sum(nil))
	}
	return compact
}
