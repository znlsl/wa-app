package app

import (
	"context"
	"time"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
)

type Store interface {
	Close()
	SaveAppArtifact(context.Context, *waappv1.AppArtifact) error
	GetAppArtifact(context.Context, string) (*waappv1.AppArtifact, error)
	SaveProtocolProfile(context.Context, *waappv1.ProtocolProfile) error
	GetProtocolProfile(context.Context, string) (*waappv1.ProtocolProfile, error)

	SaveWAAccount(context.Context, *waappv1.WAAccount) error
	GetWAAccount(context.Context, string) (*waappv1.WAAccount, error)
	FindWAAccountByPhone(context.Context, string) (*waappv1.WAAccount, error)
	ListWAAccounts(context.Context, string, int) ([]*waappv1.WAAccount, string, error)
	DeleteWAAccount(context.Context, string) error
	SaveClientProfile(context.Context, *waappv1.ClientProfile) error
	GetClientProfile(context.Context, string) (*waappv1.ClientProfile, error)
	ListClientProfiles(context.Context, string, string, int) ([]*waappv1.ClientProfile, string, error)
	SaveNativeState(context.Context, string, nativeState) error
	GetNativeState(context.Context, string) (nativeState, error)

	SaveAccountProbe(context.Context, *waappv1.AccountProbe) error
	SaveVerificationRequest(context.Context, *waappv1.VerificationCodeRequestRecord) error
	GetVerificationRequest(context.Context, string) (*waappv1.VerificationCodeRequestRecord, error)
	SaveRegistration(context.Context, *waappv1.RegistrationRecord) error
	GetRegistration(context.Context, string) (*waappv1.RegistrationRecord, error)
	SaveLoginState(context.Context, *waappv1.LoginState, string) error
	GetLoginState(context.Context, string) (*waappv1.LoginState, error)
	GetActiveLoginState(context.Context, string, string) (*waappv1.LoginState, error)
	ListActiveLoginStates(context.Context) ([]LoginStateRecord, error)
	GetLoginStateByRegistration(context.Context, string) (*waappv1.LoginState, error)
	GetLoginStateByRegisteredIdentity(context.Context, string) (*waappv1.LoginState, error)

	SaveMessageSession(context.Context, *waappv1.MessageSession) error
	GetMessageSession(context.Context, string) (*waappv1.MessageSession, error)
	CloseStaleOpenMessageSessions(context.Context, time.Time) (int64, error)
	SaveInboundMessages(context.Context, []*waappv1.InboundMessage) error
	GetInboundMessage(context.Context, string) (*waappv1.InboundMessage, error)
	ListPendingEncryptedInboundMessages(context.Context, string, string, int) ([]*waappv1.InboundMessage, error)
	ListAccountMessages(context.Context, string, string, string, int, bool) ([]*waappv1.AccountMessage, string, error)
	SaveDecryptedMessage(context.Context, *waappv1.DecryptedMessage) error
	GetDecryptedMessage(context.Context, string) (*waappv1.DecryptedMessage, error)
	SaveCandidates(context.Context, []*waappv1.ExtractedCandidate) error
	SaveOTPMessage(context.Context, *waappv1.OtpMessage) error
	ListAccountOTPMessages(context.Context, string, string, int, bool) ([]*waappv1.OtpMessage, string, error)
	SaveWAContacts(context.Context, []*waappv1.WAContact) error
	GetWAContact(context.Context, string) (*waappv1.WAContact, error)
	ListWAContacts(context.Context, string, string, int) ([]*waappv1.WAContact, string, error)
}

type RuntimeState interface {
	Close() error
	ClaimRequest(context.Context, string, time.Duration) (bool, error)
	SaveTransientState(context.Context, string, []byte, time.Duration) error
	GetTransientState(context.Context, string) ([]byte, error)
	DeleteTransientState(context.Context, string) error
	ClaimLease(context.Context, string, string, time.Duration) (bool, error)
	RenewLease(context.Context, string, string, time.Duration) (bool, error)
	ReleaseLease(context.Context, string, string) error
	OpenSessionLease(context.Context, string, time.Duration) error
	CloseSessionLease(context.Context, string) error
}

type NativeStateStore interface {
	SaveNativeState(context.Context, string, nativeState) error
	GetNativeState(context.Context, string) (nativeState, error)
}

type LoginStateRecord struct {
	LoginState *waappv1.LoginState
}

type ProtocolEngine interface {
	PrepareClientProfile(context.Context, EngineProfileInput) error
	ProbeAccount(context.Context, EngineRegistrationInput) EngineProbeResult
	RequestVerificationCode(context.Context, EngineRegistrationInput) EngineCodeResult
	SubmitVerificationCode(context.Context, EngineSubmitInput) EngineRegisterResult
	CheckLoginState(context.Context, EngineLoginCheckInput) EngineLoginCheckResult
	ReceiveMessageBatch(context.Context, EngineMessageInput) EngineMessageBatchResult
	DecryptMessage(context.Context, EngineDecryptInput) EngineDecryptResult
	ApplyAccountSettings(context.Context, EngineAccountSettingsInput) EngineAccountSettingsResult
}

type EngineProfileInput struct {
	WAAccountID       string
	ClientProfileID   string
	ProtocolProfileID string
	Phone             *waappv1.PhoneTarget
}

type EngineRegistrationInput struct {
	WAAccountID       string
	ClientProfileID   string
	ProtocolProfileID string
	Phone             *waappv1.PhoneTarget
}

type EngineSubmitInput struct {
	EngineRegistrationInput
	VerificationRequestID string
	Code                  string
	CodeSecretRef         string
}

type EngineLoginCheckInput struct {
	WAAccountID          string
	ClientProfileID      string
	RegisteredIdentityID string
	RemoteTimeout        time.Duration
}

type EngineMessageInput struct {
	WAAccountID          string
	ClientProfileID      string
	RegisteredIdentityID string
	ProtocolProfileID    string
	MessageSessionID     string
	WaitTimeout          time.Duration
	MaxMessages          int
}

type EngineDecryptInput struct {
	MessageID            string
	MessageSessionID     string
	ClientProfileID      string
	PayloadRef           string
	SessionCommitPolicy  waappv1.SessionCommitPolicy
	IncludePlaintextText bool
}

type EngineAccountSettingsInput struct {
	WAAccountID          string
	ClientProfileID      string
	RegisteredIdentityID string
	LoginStateID         string
	Kind                 waappv1.AccountSettingsOperationKind
	Pin                  string
	RecoveryEmail        string
	EmailAddress         string
	GoogleIDToken        string
	LocaleLanguage       string
	LocaleCountry        string
	Code                 string
}

type EngineContactResolveInput struct {
	WAAccountID          string
	ClientProfileID      string
	RegisteredIdentityID string
	JIDs                 []string
	RemoteTimeout        time.Duration
}

type EngineContactProfilePictureInput struct {
	WAAccountID          string
	ClientProfileID      string
	RegisteredIdentityID string
	ContactJID           string
	RemoteTimeout        time.Duration
}

type EngineProbeResult struct {
	Status           waappv1.AccountProbeStatus
	AccountFlow      string
	RawStatus        string
	RawReason        string
	RegisteredKnown  bool
	Registered       bool
	Blocked          bool
	SMSWaitSeconds   int64
	CanSendSMS       bool
	SupportedMethods []waappv1.VerificationDeliveryMethod
	MethodStatuses   []VerificationMethodStatus
	Err              error
}

type VerificationMethodStatus struct {
	Method          waappv1.VerificationDeliveryMethod
	Code            string
	Available       bool
	CooldownSeconds int64
}

const (
	accountProbeFlowUnknown       = "unknown"
	accountProbeFlowProbeFailed   = "probe_failed"
	accountProbeFlowRegistered    = "registered"
	accountProbeFlowNotRegistered = "not_registered"
	accountProbeFlowBlocked       = "blocked"
	accountProbeFlowInvalidNumber = "invalid_number"
	accountProbeFlowRateLimited   = "rate_limited"
)

type EngineCodeResult struct {
	Status             waappv1.VerificationRequestStatus
	ExpectedCodeLength int32
	ExpiresAt          time.Time
	Err                error
}

type EngineRegisterResult struct {
	Status           waappv1.RegistrationStatus
	RegisteredID     string
	ServiceAccountID string
	ServiceLoginID   string
	CompletedAt      time.Time
	Err              error
}

type EngineLoginCheckResult struct {
	Status waappv1.LoginStateCheckStatus
	Err    error
}

type EngineMessageBatchResult struct {
	Messages []*waappv1.InboundMessage
	Contacts []*waappv1.WAContact
	Err      error
}

type EngineDecryptResult struct {
	DecryptedMessage *waappv1.DecryptedMessage
	Candidates       []*waappv1.ExtractedCandidate
	ContactHints     []waContactHint
	Err              error
}

type EngineAccountSettingsResult struct {
	Status   waappv1.AccountSettingsOperationStatus
	WaitTime time.Duration
	Err      error
}

type EngineContactResolveResult struct {
	Contacts []*waappv1.WAContact
	Queried  int
	Resolved int
	Err      error
}

type EngineContactProfilePictureResult struct {
	ProfilePictureID string
	ContentType      string
	Data             []byte
	Err              error
}
