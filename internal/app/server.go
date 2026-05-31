package app

import (
	"context"
	"strings"
	"time"

	"github.com/byte-v-forge/common-lib/accountevent"
	"github.com/byte-v-forge/common-lib/eventbus"
	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Server struct {
	waappv1.UnimplementedWaDiscoveryServiceServer
	waappv1.UnimplementedWaProfileServiceServer
	waappv1.UnimplementedWaRegistrationServiceServer
	waappv1.UnimplementedWaMessagingServiceServer
	waappv1.UnimplementedWaExtractionServiceServer
	waappv1.UnimplementedWaToolingServiceServer

	store   Store
	runtime RuntimeState
	runner  ProtocolEngine
	clock   Clock
	ids     IDGenerator

	proxyRuntime      *DynamicProxyRuntime
	platformPublisher eventbus.Publisher
	accountPublisher  *accountevent.Publisher
	longConnections   *LongConnectionManager
}

func NewServer(store Store, runtime RuntimeState, runner ProtocolEngine, clock Clock, ids IDGenerator) *Server {
	if clock == nil {
		clock = SystemClock{}
	}
	if ids == nil {
		ids = RandomIDGenerator{}
	}
	server := &Server{store: store, runtime: runtime, runner: runner, clock: clock, ids: ids}
	server.longConnections = NewLongConnectionManager(server)
	return server
}

func (s *Server) SetDynamicProxyRuntime(proxyRuntime *DynamicProxyRuntime) {
	s.proxyRuntime = proxyRuntime
}

func (s *Server) SetPlatformPublisher(publisher eventbus.Publisher) {
	s.platformPublisher = publisher
	s.accountPublisher = accountevent.NewPublisher(accountevent.Config{Publisher: publisher, Descriptor: waAccountDescriptor})
}

func (s *Server) RunLongConnections(ctx context.Context) error {
	if s == nil || s.longConnections == nil {
		return nil
	}
	return s.longConnections.Run(ctx)
}

func (s *Server) RegisterAppArtifact(ctx context.Context, req *waappv1.RegisterAppArtifactRequest) (*waappv1.RegisterAppArtifactResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.RegisterAppArtifactResponse{Error: ToProtoError(err)}, nil
	}
	if strings.TrimSpace(req.GetLabel()) == "" {
		return &waappv1.RegisterAppArtifactResponse{Error: ToProtoError(NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "artifact label is required", false))}, nil
	}
	now := s.clock.Now()
	artifact := &waappv1.AppArtifact{ArtifactId: s.ids.NewID("waart_"), Label: req.GetLabel(), VersionLabel: req.GetVersionLabel(), Sha256: req.GetSha256(), ObservedAt: timestamppb.New(now)}
	if err := s.store.SaveAppArtifact(ctx, artifact, req.GetContext().GetWorkspaceId()); err != nil {
		return &waappv1.RegisterAppArtifactResponse{Error: ToProtoError(err)}, nil
	}
	return &waappv1.RegisterAppArtifactResponse{Artifact: artifact}, nil
}

func (s *Server) RecordProtocolProfile(ctx context.Context, req *waappv1.RecordProtocolProfileRequest) (*waappv1.RecordProtocolProfileResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.RecordProtocolProfileResponse{Error: ToProtoError(err)}, nil
	}
	workspaceID := req.GetContext().GetWorkspaceId()
	if _, err := s.store.GetAppArtifact(ctx, workspaceID, req.GetAppArtifactId()); err != nil {
		return &waappv1.RecordProtocolProfileResponse{Error: ToProtoError(err)}, nil
	}
	now := s.clock.Now()
	profile := &waappv1.ProtocolProfile{
		ProtocolProfileId: s.ids.NewID("waproto_"),
		AppArtifactId:     req.GetAppArtifactId(),
		DisplayName:       firstNonEmpty(req.GetDisplayName(), "WA protocol profile"),
		AppVersion:        req.GetAppVersion(),
		Status:            waappv1.ProtocolProfileStatus_PROTOCOL_PROFILE_STATUS_ACTIVE,
		Capabilities:      req.GetCapabilities(),
		RegistrationFlows: req.GetRegistrationFlows(),
		MessageTransports: req.GetMessageTransports(),
		DiscoveredAt:      timestamppb.New(now),
		Audit:             &waappv1.AuditStamp{CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)},
	}
	if err := s.store.SaveProtocolProfile(ctx, profile, workspaceID); err != nil {
		return &waappv1.RecordProtocolProfileResponse{Error: ToProtoError(err)}, nil
	}
	return &waappv1.RecordProtocolProfileResponse{ProtocolProfile: profile}, nil
}

func (s *Server) GetProtocolProfile(ctx context.Context, req *waappv1.GetProtocolProfileRequest) (*waappv1.GetProtocolProfileResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.GetProtocolProfileResponse{Error: ToProtoError(err)}, nil
	}
	profile, err := s.store.GetProtocolProfile(ctx, req.GetContext().GetWorkspaceId(), req.GetProtocolProfileId())
	if err != nil {
		return &waappv1.GetProtocolProfileResponse{Error: ToProtoError(err)}, nil
	}
	return &waappv1.GetProtocolProfileResponse{ProtocolProfile: profile}, nil
}

func (s *Server) CreateWAAccount(ctx context.Context, req *waappv1.CreateWAAccountRequest) (*waappv1.CreateWAAccountResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.CreateWAAccountResponse{Error: ToProtoError(err)}, nil
	}
	phone := normalizePhone(req.GetPhone())
	if phone.GetE164Number() == "" {
		return &waappv1.CreateWAAccountResponse{Error: ToProtoError(NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, "phone is required", false))}, nil
	}
	workspaceID := req.GetContext().GetWorkspaceId()
	if existing, err := s.store.FindWAAccountByPhone(ctx, workspaceID, phone.GetE164Number()); err == nil {
		return &waappv1.CreateWAAccountResponse{Account: existing}, nil
	}
	now := s.clock.Now()
	account := newWAAccount(s.ids.NewID("waacc_"), workspaceID, phone, waappv1.WAAccountStatus_WA_ACCOUNT_STATUS_ACTIVE, &waappv1.AuditStamp{CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)})
	account, err := s.saveWAAccount(ctx, workspaceID, account)
	if err != nil {
		return &waappv1.CreateWAAccountResponse{Error: ToProtoError(err)}, nil
	}
	return &waappv1.CreateWAAccountResponse{Account: account}, nil
}

func (s *Server) GetWAAccount(ctx context.Context, req *waappv1.GetWAAccountRequest) (*waappv1.GetWAAccountResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.GetWAAccountResponse{Error: ToProtoError(err)}, nil
	}
	accountID, err := requireWAAccountID(req.GetWaAccountId())
	if err != nil {
		return &waappv1.GetWAAccountResponse{Error: ToProtoError(err)}, nil
	}
	account, err := s.getWAAccount(ctx, req.GetContext().GetWorkspaceId(), accountID)
	if err != nil {
		return &waappv1.GetWAAccountResponse{Error: ToProtoError(err)}, nil
	}
	return &waappv1.GetWAAccountResponse{Account: account}, nil
}

func (s *Server) ListWAAccounts(ctx context.Context, req *waappv1.ListWAAccountsRequest) (*waappv1.ListWAAccountsResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.ListWAAccountsResponse{Error: ToProtoError(err)}, nil
	}
	accounts, nextCursor, err := s.listWAAccounts(ctx, req.GetContext().GetWorkspaceId(), req.GetCursor(), int(req.GetLimit()))
	if err != nil {
		return &waappv1.ListWAAccountsResponse{Error: ToProtoError(err)}, nil
	}
	return &waappv1.ListWAAccountsResponse{Accounts: accounts, NextCursor: nextCursor}, nil
}

func (s *Server) PrepareClientProfile(ctx context.Context, req *waappv1.PrepareClientProfileRequest) (*waappv1.PrepareClientProfileResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.PrepareClientProfileResponse{Error: ToProtoError(err)}, nil
	}
	workspaceID := req.GetContext().GetWorkspaceId()
	accountID, err := requireWAAccountID(req.GetWaAccountId())
	if err != nil {
		return &waappv1.PrepareClientProfileResponse{Error: ToProtoError(err)}, nil
	}
	account, err := s.getWAAccount(ctx, workspaceID, accountID)
	if err != nil {
		return &waappv1.PrepareClientProfileResponse{Error: ToProtoError(err)}, nil
	}
	if _, err := s.store.GetProtocolProfile(ctx, workspaceID, req.GetProtocolProfileId()); err != nil {
		return &waappv1.PrepareClientProfileResponse{Error: ToProtoError(err)}, nil
	}
	now := s.clock.Now()
	profile := &waappv1.ClientProfile{ClientProfileId: s.ids.NewID("wacp_"), WaAccountId: waAccountID(account), ProtocolProfileId: req.GetProtocolProfileId(), Status: waappv1.ClientProfileStatus_CLIENT_PROFILE_STATUS_PREPARING, RegistrationKeyState: waappv1.KeyMaterialStatus_KEY_MATERIAL_STATUS_PENDING, MessagingKeyState: waappv1.KeyMaterialStatus_KEY_MATERIAL_STATUS_PENDING, Audit: &waappv1.AuditStamp{CreatedAt: timestamppb.New(now), UpdatedAt: timestamppb.New(now)}}
	if err := s.store.SaveClientProfile(ctx, profile, workspaceID); err != nil {
		return &waappv1.PrepareClientProfileResponse{Error: ToProtoError(err)}, nil
	}
	runErr := s.runner.PrepareClientProfile(ctx, EngineProfileInput{WorkspaceID: workspaceID, WAAccountID: waAccountID(account), ClientProfileID: profile.GetClientProfileId(), ProtocolProfileID: req.GetProtocolProfileId(), Phone: account.GetPhone()})
	profile.Audit.UpdatedAt = timestamppb.New(s.clock.Now())
	if runErr != nil {
		profile.Status = waappv1.ClientProfileStatus_CLIENT_PROFILE_STATUS_REJECTED
		profile.RegistrationKeyState = waappv1.KeyMaterialStatus_KEY_MATERIAL_STATUS_INVALID
		profile.MessagingKeyState = waappv1.KeyMaterialStatus_KEY_MATERIAL_STATUS_INVALID
		profile.LastError = ToProtoError(runErr)
	} else {
		profile.Status = waappv1.ClientProfileStatus_CLIENT_PROFILE_STATUS_READY
		profile.RegistrationKeyState = waappv1.KeyMaterialStatus_KEY_MATERIAL_STATUS_READY
		profile.MessagingKeyState = waappv1.KeyMaterialStatus_KEY_MATERIAL_STATUS_READY
	}
	if err := s.store.SaveClientProfile(ctx, profile, workspaceID); err != nil {
		return &waappv1.PrepareClientProfileResponse{Error: ToProtoError(err)}, nil
	}
	return &waappv1.PrepareClientProfileResponse{ClientProfile: profile, Error: profile.GetLastError()}, nil
}

func (s *Server) GetClientProfile(ctx context.Context, req *waappv1.GetClientProfileRequest) (*waappv1.GetClientProfileResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.GetClientProfileResponse{Error: ToProtoError(err)}, nil
	}
	profile, err := s.store.GetClientProfile(ctx, req.GetContext().GetWorkspaceId(), req.GetClientProfileId())
	if err != nil {
		return &waappv1.GetClientProfileResponse{Error: ToProtoError(err)}, nil
	}
	return &waappv1.GetClientProfileResponse{ClientProfile: profile}, nil
}

func (s *Server) RetireClientProfile(ctx context.Context, req *waappv1.RetireClientProfileRequest) (*waappv1.RetireClientProfileResponse, error) {
	if err := validateContext(req.GetContext()); err != nil {
		return &waappv1.RetireClientProfileResponse{Error: ToProtoError(err)}, nil
	}
	workspaceID := req.GetContext().GetWorkspaceId()
	profile, err := s.store.GetClientProfile(ctx, workspaceID, req.GetClientProfileId())
	if err != nil {
		return &waappv1.RetireClientProfileResponse{Error: ToProtoError(err)}, nil
	}
	profile.Status = waappv1.ClientProfileStatus_CLIENT_PROFILE_STATUS_RETIRED
	profile.Audit.UpdatedAt = timestamppb.New(s.clock.Now())
	if err := s.store.SaveClientProfile(ctx, profile, workspaceID); err != nil {
		return &waappv1.RetireClientProfileResponse{Error: ToProtoError(err)}, nil
	}
	return &waappv1.RetireClientProfileResponse{ClientProfile: profile}, nil
}

func normalizePhone(phone *waappv1.PhoneTarget) *waappv1.PhoneTarget {
	if phone == nil {
		return &waappv1.PhoneTarget{}
	}
	cc := strings.TrimPrefix(strings.TrimSpace(phone.GetCountryCallingCode()), "+")
	national := strings.TrimSpace(phone.GetNationalNumber())
	e164 := strings.TrimSpace(phone.GetE164Number())
	if e164 == "" && cc != "" && national != "" {
		e164 = "+" + cc + national
	}
	if e164 != "" && !strings.HasPrefix(e164, "+") {
		e164 = "+" + e164
	}
	return &waappv1.PhoneTarget{E164Number: e164, CountryCallingCode: cc, NationalNumber: national, CountryIso2: strings.ToUpper(strings.TrimSpace(phone.GetCountryIso2()))}
}

func protoDurationSeconds(d interface{ GetSeconds() int64 }) time.Duration {
	if d == nil {
		return 0
	}
	return time.Duration(d.GetSeconds()) * time.Second
}
