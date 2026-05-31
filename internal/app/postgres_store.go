package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/byte-v-forge/common-lib/pagex"
	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(ctx context.Context, dsn string) (*PostgresStore, error) {
	if dsn == "" {
		return nil, fmt.Errorf("WA_APP_PG_DSN or PG_DSN is required")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	store := &PostgresStore{pool: pool}
	if err := store.validate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (s *PostgresStore) Close() {
	if s != nil && s.pool != nil {
		s.pool.Close()
	}
}

func (s *PostgresStore) validate(ctx context.Context) error {
	for _, table := range []string{
		"wa_app_artifacts", "wa_protocol_profiles", "wa_accounts", "wa_client_profiles", "wa_account_probes",
		"wa_verification_requests", "wa_registrations", "wa_login_states", "wa_message_sessions", "wa_inbound_messages",
		"wa_decrypted_messages", "wa_extracted_candidates",
	} {
		var exists bool
		if err := s.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("required wa-app table %s is missing; apply migrations first", table)
		}
	}
	return nil
}

func (s *PostgresStore) SaveAppArtifact(ctx context.Context, artifact *waappv1.AppArtifact, workspaceID string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO wa_app_artifacts (artifact_id, workspace_id, label, version_label, sha256, observed_at)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (artifact_id) DO UPDATE SET label=EXCLUDED.label, version_label=EXCLUDED.version_label, sha256=EXCLUDED.sha256, observed_at=EXCLUDED.observed_at`,
		artifact.GetArtifactId(), workspaceID, artifact.GetLabel(), artifact.GetVersionLabel(), artifact.GetSha256(), timeFromProto(artifact.GetObservedAt()))
	return err
}

func (s *PostgresStore) GetAppArtifact(ctx context.Context, workspaceID string, artifactID string) (*waappv1.AppArtifact, error) {
	row := s.pool.QueryRow(ctx, `SELECT artifact_id,label,version_label,sha256,observed_at FROM wa_app_artifacts WHERE workspace_id=$1 AND artifact_id=$2`, workspaceID, artifactID)
	var id, label, version, hash string
	var observed time.Time
	if err := row.Scan(&id, &label, &version, &hash, &observed); err != nil {
		return nil, notFound(err, waappv1.WaErrorCode_WA_ERROR_CODE_PROTOCOL_PROFILE_NOT_FOUND, "app artifact not found")
	}
	return &waappv1.AppArtifact{ArtifactId: id, Label: label, VersionLabel: version, Sha256: hash, ObservedAt: timestamppb.New(observed)}, nil
}

func (s *PostgresStore) SaveProtocolProfile(ctx context.Context, profile *waappv1.ProtocolProfile, workspaceID string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO wa_protocol_profiles (protocol_profile_id, workspace_id, app_artifact_id, display_name, app_version, status, capabilities, registration_flows, message_transports, discovered_at, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (protocol_profile_id) DO UPDATE SET display_name=EXCLUDED.display_name, app_version=EXCLUDED.app_version, status=EXCLUDED.status, capabilities=EXCLUDED.capabilities, registration_flows=EXCLUDED.registration_flows, message_transports=EXCLUDED.message_transports, updated_at=EXCLUDED.updated_at`,
		profile.GetProtocolProfileId(), workspaceID, profile.GetAppArtifactId(), profile.GetDisplayName(), profile.GetAppVersion(), profile.GetStatus().String(), protocolCapabilities(profile.GetCapabilities()), registrationFlows(profile.GetRegistrationFlows()), messageTransports(profile.GetMessageTransports()), timeFromProto(profile.GetDiscoveredAt()), timeFromProto(profile.GetAudit().GetCreatedAt()), timeFromProto(profile.GetAudit().GetUpdatedAt()))
	return err
}

func (s *PostgresStore) GetProtocolProfile(ctx context.Context, workspaceID string, id string) (*waappv1.ProtocolProfile, error) {
	row := s.pool.QueryRow(ctx, `SELECT protocol_profile_id,app_artifact_id,display_name,app_version,status,capabilities,registration_flows,message_transports,discovered_at,created_at,updated_at FROM wa_protocol_profiles WHERE workspace_id=$1 AND protocol_profile_id=$2`, workspaceID, id)
	var r protocolProfileRow
	if err := row.Scan(&r.id, &r.artifactID, &r.displayName, &r.appVersion, &r.status, &r.capabilities, &r.registrationFlows, &r.messageTransports, &r.discoveredAt, &r.createdAt, &r.updatedAt); err != nil {
		return nil, notFound(err, waappv1.WaErrorCode_WA_ERROR_CODE_PROTOCOL_PROFILE_NOT_FOUND, "protocol profile not found")
	}
	return r.toProto(), nil
}

func (s *PostgresStore) SaveWAAccount(ctx context.Context, account *waappv1.WAAccount) error {
	createdAt := waAccountCreatedAt(account)
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := waAccountUpdatedAt(account)
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO wa_accounts (wa_account_id, workspace_id, e164_number, country_calling_code, national_number, country_iso2, status, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (workspace_id, e164_number) DO UPDATE SET country_calling_code=EXCLUDED.country_calling_code, national_number=EXCLUDED.national_number, country_iso2=EXCLUDED.country_iso2, status=EXCLUDED.status, updated_at=EXCLUDED.updated_at`,
		waAccountID(account), account.GetWorkspaceId(), account.GetPhone().GetE164Number(), account.GetPhone().GetCountryCallingCode(), account.GetPhone().GetNationalNumber(), account.GetPhone().GetCountryIso2(), waAccountStatusStorageValue(account), createdAt, updatedAt)
	return err
}

func (s *PostgresStore) GetWAAccount(ctx context.Context, workspaceID string, waAccountIDValue string) (*waappv1.WAAccount, error) {
	return s.getWAAccount(ctx, `workspace_id=$1 AND wa_account_id=$2`, workspaceID, waAccountIDValue)
}

func (s *PostgresStore) FindWAAccountByPhone(ctx context.Context, workspaceID string, e164 string) (*waappv1.WAAccount, error) {
	return s.getWAAccount(ctx, `workspace_id=$1 AND e164_number=$2`, workspaceID, e164)
}

func (s *PostgresStore) ListWAAccounts(ctx context.Context, workspaceID string, cursorValue string, limit int) ([]*waappv1.WAAccount, string, error) {
	cursor, err := pagex.DecodeKeysetCursor(cursorValue)
	if err != nil {
		return nil, "", NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, err.Error(), false)
	}
	limit = pagex.NormalizePageLimit(limit)
	rows, err := s.queryWAAccountPage(ctx, workspaceID, cursor, pagex.KeysetLookaheadLimit(limit))
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	accounts := []*waappv1.WAAccount{}
	for rows.Next() {
		var r waAccountRow
		if err := rows.Scan(&r.id, &r.workspaceID, &r.e164, &r.cc, &r.national, &r.iso2, &r.status, &r.createdAt, &r.updatedAt); err != nil {
			return nil, "", err
		}
		accounts = append(accounts, r.toProto())
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	page := pagex.NewKeysetPage(accounts, limit, func(account *waappv1.WAAccount) pagex.KeysetCursor {
		return pagex.KeysetCursorValue(waAccountUpdatedAt(account), waAccountID(account))
	})
	return page.Items, page.NextCursor, nil
}

func (s *PostgresStore) queryWAAccountPage(ctx context.Context, workspaceID string, cursor pagex.KeysetCursor, limit int) (pgx.Rows, error) {
	const base = `SELECT wa_account_id,workspace_id,e164_number,country_calling_code,national_number,country_iso2,status,created_at,updated_at FROM wa_accounts WHERE workspace_id=$1`
	if !pagex.HasKeysetCursor(cursor) {
		return s.pool.Query(ctx, base+` ORDER BY updated_at DESC, wa_account_id DESC LIMIT $2`, workspaceID, limit)
	}
	return s.pool.Query(ctx, base+` AND (updated_at, wa_account_id) < ($2, $3) ORDER BY updated_at DESC, wa_account_id DESC LIMIT $4`, workspaceID, cursor.UpdatedAt, cursor.ID, limit)
}

func (s *PostgresStore) getWAAccount(ctx context.Context, where string, args ...any) (*waappv1.WAAccount, error) {
	row := s.pool.QueryRow(ctx, `SELECT wa_account_id,workspace_id,e164_number,country_calling_code,national_number,country_iso2,status,created_at,updated_at FROM wa_accounts WHERE `+where, args...)
	var r waAccountRow
	if err := row.Scan(&r.id, &r.workspaceID, &r.e164, &r.cc, &r.national, &r.iso2, &r.status, &r.createdAt, &r.updatedAt); err != nil {
		return nil, notFound(err, waappv1.WaErrorCode_WA_ERROR_CODE_WA_ACCOUNT_NOT_FOUND, "WA account not found")
	}
	return r.toProto(), nil
}

func (s *PostgresStore) SaveClientProfile(ctx context.Context, profile *waappv1.ClientProfile, workspaceID string) error {
	appErr := errorFromProto(profile.GetLastError())
	_, err := s.pool.Exec(ctx, `INSERT INTO wa_client_profiles (client_profile_id, workspace_id, wa_account_id, protocol_profile_id, status, registration_key_state, messaging_key_state, last_error_code, last_error_message, last_error_retryable, last_used_at, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (client_profile_id) DO UPDATE SET status=EXCLUDED.status, registration_key_state=EXCLUDED.registration_key_state, messaging_key_state=EXCLUDED.messaging_key_state, last_error_code=EXCLUDED.last_error_code, last_error_message=EXCLUDED.last_error_message, last_error_retryable=EXCLUDED.last_error_retryable, last_used_at=EXCLUDED.last_used_at, updated_at=EXCLUDED.updated_at`,
		profile.GetClientProfileId(), workspaceID, profile.GetWaAccountId(), profile.GetProtocolProfileId(), profile.GetStatus().String(), profile.GetRegistrationKeyState().String(), profile.GetMessagingKeyState().String(), errCode(appErr), errMessage(appErr), errRetryable(appErr), nullableProtoTime(profile.GetLastUsedAt()), timeFromProto(profile.GetAudit().GetCreatedAt()), timeFromProto(profile.GetAudit().GetUpdatedAt()))
	return err
}

func (s *PostgresStore) GetClientProfile(ctx context.Context, workspaceID string, id string) (*waappv1.ClientProfile, error) {
	row := s.pool.QueryRow(ctx, `SELECT client_profile_id,wa_account_id,protocol_profile_id,status,registration_key_state,messaging_key_state,last_error_code,last_error_message,last_error_retryable,last_used_at,created_at,updated_at FROM wa_client_profiles WHERE workspace_id=$1 AND client_profile_id=$2`, workspaceID, id)
	var r clientProfileRow
	if err := row.Scan(&r.id, &r.waAccountIDValue, &r.protocolProfileID, &r.status, &r.registrationKeyState, &r.messagingKeyState, &r.errCode, &r.errMessage, &r.errRetryable, &r.lastUsedAt, &r.createdAt, &r.updatedAt); err != nil {
		return nil, notFound(err, waappv1.WaErrorCode_WA_ERROR_CODE_PROFILE_NOT_FOUND, "client profile not found")
	}
	return r.toProto(), nil
}

func (s *PostgresStore) SaveAccountProbe(ctx context.Context, probe *waappv1.AccountProbe, workspaceID string) error {
	appErr := errorFromProto(probe.GetLastError())
	_, err := s.pool.Exec(ctx, `INSERT INTO wa_account_probes (account_probe_id, workspace_id, wa_account_id, client_profile_id, status, supported_methods, last_error_code, last_error_message, last_error_retryable, probed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (account_probe_id) DO UPDATE SET status=EXCLUDED.status, supported_methods=EXCLUDED.supported_methods, last_error_code=EXCLUDED.last_error_code, last_error_message=EXCLUDED.last_error_message, last_error_retryable=EXCLUDED.last_error_retryable, probed_at=EXCLUDED.probed_at`,
		probe.GetAccountProbeId(), workspaceID, probe.GetWaAccountId(), probe.GetClientProfileId(), probe.GetStatus().String(), deliveryMethods(probe.GetSupportedMethods()), errCode(appErr), errMessage(appErr), errRetryable(appErr), timeFromProto(probe.GetProbedAt()))
	return err
}

func (s *PostgresStore) SaveVerificationRequest(ctx context.Context, record *waappv1.VerificationCodeRequestRecord, workspaceID string) error {
	appErr := errorFromProto(record.GetLastError())
	_, err := s.pool.Exec(ctx, `INSERT INTO wa_verification_requests (verification_request_id, workspace_id, wa_account_id, client_profile_id, delivery_method, status, expected_code_length, last_error_code, last_error_message, last_error_retryable, requested_at, expires_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (verification_request_id) DO UPDATE SET status=EXCLUDED.status, expected_code_length=EXCLUDED.expected_code_length, last_error_code=EXCLUDED.last_error_code, last_error_message=EXCLUDED.last_error_message, last_error_retryable=EXCLUDED.last_error_retryable, expires_at=EXCLUDED.expires_at`,
		record.GetVerificationRequestId(), workspaceID, record.GetWaAccountId(), record.GetClientProfileId(), record.GetDeliveryMethod().String(), record.GetStatus().String(), record.GetExpectedCodeLength(), errCode(appErr), errMessage(appErr), errRetryable(appErr), timeFromProto(record.GetRequestedAt()), nullableProtoTime(record.GetExpiresAt()))
	return err
}

func (s *PostgresStore) GetVerificationRequest(ctx context.Context, workspaceID string, id string) (*waappv1.VerificationCodeRequestRecord, error) {
	row := s.pool.QueryRow(ctx, `SELECT verification_request_id,wa_account_id,client_profile_id,delivery_method,status,expected_code_length,last_error_code,last_error_message,last_error_retryable,requested_at,expires_at FROM wa_verification_requests WHERE workspace_id=$1 AND verification_request_id=$2`, workspaceID, id)
	var r verificationRow
	if err := row.Scan(&r.id, &r.waAccountIDValue, &r.clientProfileID, &r.method, &r.status, &r.length, &r.errCode, &r.errMessage, &r.errRetryable, &r.requestedAt, &r.expiresAt); err != nil {
		return nil, notFound(err, waappv1.WaErrorCode_WA_ERROR_CODE_REGISTRATION_NOT_FOUND, "verification request not found")
	}
	return r.toProto(), nil
}

func (s *PostgresStore) SaveRegistration(ctx context.Context, record *waappv1.RegistrationRecord, workspaceID string) error {
	appErr := errorFromProto(record.GetLastError())
	identity := record.GetIdentity()
	_, err := s.pool.Exec(ctx, `INSERT INTO wa_registrations (registration_id, workspace_id, verification_request_id, wa_account_id, client_profile_id, status, registered_identity_id, service_account_id, service_login_id, last_error_code, last_error_message, last_error_retryable, submitted_at, completed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
ON CONFLICT (registration_id) DO UPDATE SET status=EXCLUDED.status, registered_identity_id=EXCLUDED.registered_identity_id, service_account_id=EXCLUDED.service_account_id, service_login_id=EXCLUDED.service_login_id, last_error_code=EXCLUDED.last_error_code, last_error_message=EXCLUDED.last_error_message, last_error_retryable=EXCLUDED.last_error_retryable, completed_at=EXCLUDED.completed_at`,
		record.GetRegistrationId(), workspaceID, record.GetVerificationRequestId(), record.GetWaAccountId(), record.GetClientProfileId(), record.GetStatus().String(), identity.GetRegisteredIdentityId(), identity.GetServiceAccountId(), identity.GetServiceLoginId(), errCode(appErr), errMessage(appErr), errRetryable(appErr), timeFromProto(record.GetSubmittedAt()), nullableProtoTime(record.GetCompletedAt()))
	return err
}

func (s *PostgresStore) GetRegistration(ctx context.Context, workspaceID string, id string) (*waappv1.RegistrationRecord, error) {
	row := s.pool.QueryRow(ctx, `SELECT registration_id,verification_request_id,wa_account_id,client_profile_id,status,registered_identity_id,service_account_id,service_login_id,last_error_code,last_error_message,last_error_retryable,submitted_at,completed_at FROM wa_registrations WHERE workspace_id=$1 AND registration_id=$2`, workspaceID, id)
	var r registrationRow
	if err := row.Scan(&r.id, &r.verificationRequestID, &r.waAccountIDValue, &r.clientProfileID, &r.status, &r.identityID, &r.serviceAccountID, &r.serviceLoginID, &r.errCode, &r.errMessage, &r.errRetryable, &r.submittedAt, &r.completedAt); err != nil {
		return nil, notFound(err, waappv1.WaErrorCode_WA_ERROR_CODE_REGISTRATION_NOT_FOUND, "registration not found")
	}
	return r.toProto(), nil
}

func (s *PostgresStore) SaveLoginState(ctx context.Context, state *waappv1.LoginState, workspaceID string, stateRef string) error {
	appErr := errorFromProto(state.GetLastError())
	_, err := s.pool.Exec(ctx, `INSERT INTO wa_login_states (login_state_id, workspace_id, registration_id, wa_account_id, client_profile_id, registered_identity_id, service_account_id, service_login_id, status, state_ref, last_error_code, last_error_message, last_error_retryable, registered_at, last_verified_at, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (login_state_id) DO UPDATE SET status=EXCLUDED.status, service_account_id=EXCLUDED.service_account_id, service_login_id=EXCLUDED.service_login_id, state_ref=EXCLUDED.state_ref, last_error_code=EXCLUDED.last_error_code, last_error_message=EXCLUDED.last_error_message, last_error_retryable=EXCLUDED.last_error_retryable, last_verified_at=EXCLUDED.last_verified_at, updated_at=EXCLUDED.updated_at`,
		state.GetLoginStateId(), workspaceID, state.GetRegistrationId(), state.GetWaAccountId(), state.GetClientProfileId(), state.GetRegisteredIdentityId(), state.GetServiceAccountId(), state.GetServiceLoginId(), state.GetStatus().String(), stateRef, errCode(appErr), errMessage(appErr), errRetryable(appErr), timeFromProto(state.GetRegisteredAt()), nullableProtoTime(state.GetLastVerifiedAt()), timeFromProto(state.GetAudit().GetCreatedAt()), timeFromProto(state.GetAudit().GetUpdatedAt()))
	return err
}

func (s *PostgresStore) GetLoginState(ctx context.Context, workspaceID string, id string) (*waappv1.LoginState, error) {
	return s.getLoginState(ctx, `workspace_id=$1 AND login_state_id=$2`, workspaceID, id)
}

func (s *PostgresStore) GetActiveLoginState(ctx context.Context, workspaceID string, waAccountIDValue string, clientProfileID string) (*waappv1.LoginState, error) {
	return s.getLoginState(ctx, `workspace_id=$1 AND wa_account_id=$2 AND client_profile_id=$3 AND status=$4`, workspaceID, waAccountIDValue, clientProfileID, waappv1.LoginStateStatus_LOGIN_STATE_STATUS_ACTIVE.String())
}

func (s *PostgresStore) ListActiveLoginStates(ctx context.Context) ([]LoginStateRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT workspace_id,login_state_id,registration_id,wa_account_id,client_profile_id,registered_identity_id,service_account_id,service_login_id,status,last_error_code,last_error_message,last_error_retryable,registered_at,last_verified_at,created_at,updated_at
FROM wa_login_states
WHERE status=$1
ORDER BY updated_at DESC`, waappv1.LoginStateStatus_LOGIN_STATE_STATUS_ACTIVE.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []LoginStateRecord{}
	for rows.Next() {
		var workspaceID string
		var r loginStateRow
		if err := rows.Scan(&workspaceID, &r.id, &r.registrationID, &r.waAccountIDValue, &r.clientProfileID, &r.registeredIdentityID, &r.serviceAccountID, &r.serviceLoginID, &r.status, &r.errCode, &r.errMessage, &r.errRetryable, &r.registeredAt, &r.lastVerifiedAt, &r.createdAt, &r.updatedAt); err != nil {
			return nil, err
		}
		records = append(records, LoginStateRecord{WorkspaceID: workspaceID, LoginState: r.toProto()})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func (s *PostgresStore) GetLoginStateByRegistration(ctx context.Context, workspaceID string, registrationID string) (*waappv1.LoginState, error) {
	return s.getLoginState(ctx, `workspace_id=$1 AND registration_id=$2`, workspaceID, registrationID)
}

func (s *PostgresStore) GetLoginStateByRegisteredIdentity(ctx context.Context, workspaceID string, registeredIdentityID string) (*waappv1.LoginState, error) {
	return s.getLoginState(ctx, `workspace_id=$1 AND registered_identity_id=$2`, workspaceID, registeredIdentityID)
}

func (s *PostgresStore) getLoginState(ctx context.Context, where string, args ...any) (*waappv1.LoginState, error) {
	row := s.pool.QueryRow(ctx, `SELECT login_state_id,registration_id,wa_account_id,client_profile_id,registered_identity_id,service_account_id,service_login_id,status,last_error_code,last_error_message,last_error_retryable,registered_at,last_verified_at,created_at,updated_at FROM wa_login_states WHERE `+where, args...)
	var r loginStateRow
	if err := row.Scan(&r.id, &r.registrationID, &r.waAccountIDValue, &r.clientProfileID, &r.registeredIdentityID, &r.serviceAccountID, &r.serviceLoginID, &r.status, &r.errCode, &r.errMessage, &r.errRetryable, &r.registeredAt, &r.lastVerifiedAt, &r.createdAt, &r.updatedAt); err != nil {
		return nil, notFound(err, waappv1.WaErrorCode_WA_ERROR_CODE_REGISTRATION_NOT_FOUND, "login state not found")
	}
	return r.toProto(), nil
}

func (s *PostgresStore) SaveMessageSession(ctx context.Context, session *waappv1.MessageSession, workspaceID string) error {
	appErr := errorFromProto(session.GetLastError())
	_, err := s.pool.Exec(ctx, `INSERT INTO wa_message_sessions (message_session_id, workspace_id, wa_account_id, client_profile_id, registered_identity_id, protocol_profile_id, status, last_error_code, last_error_message, last_error_retryable, opened_at, last_seen_at, closed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (message_session_id) DO UPDATE SET status=EXCLUDED.status, last_error_code=EXCLUDED.last_error_code, last_error_message=EXCLUDED.last_error_message, last_error_retryable=EXCLUDED.last_error_retryable, last_seen_at=EXCLUDED.last_seen_at, closed_at=EXCLUDED.closed_at`,
		session.GetMessageSessionId(), workspaceID, session.GetWaAccountId(), session.GetClientProfileId(), session.GetRegisteredIdentityId(), session.GetProtocolProfileId(), session.GetStatus().String(), errCode(appErr), errMessage(appErr), errRetryable(appErr), timeFromProto(session.GetOpenedAt()), nullableProtoTime(session.GetLastSeenAt()), nullableProtoTime(session.GetClosedAt()))
	return err
}

func (s *PostgresStore) GetMessageSession(ctx context.Context, workspaceID string, id string) (*waappv1.MessageSession, error) {
	row := s.pool.QueryRow(ctx, `SELECT message_session_id,wa_account_id,client_profile_id,registered_identity_id,protocol_profile_id,status,last_error_code,last_error_message,last_error_retryable,opened_at,last_seen_at,closed_at FROM wa_message_sessions WHERE workspace_id=$1 AND message_session_id=$2`, workspaceID, id)
	var r sessionRow
	if err := row.Scan(&r.id, &r.waAccountIDValue, &r.clientProfileID, &r.identityID, &r.protocolProfileID, &r.status, &r.errCode, &r.errMessage, &r.errRetryable, &r.openedAt, &r.lastSeenAt, &r.closedAt); err != nil {
		return nil, notFound(err, waappv1.WaErrorCode_WA_ERROR_CODE_MESSAGE_SESSION_NOT_FOUND, "message session not found")
	}
	return r.toProto(), nil
}

func (s *PostgresStore) SaveInboundMessages(ctx context.Context, workspaceID string, messages []*waappv1.InboundMessage) error {
	if len(messages) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, msg := range messages {
		appErr := errorFromProto(msg.GetLastError())
		batch.Queue(`INSERT INTO wa_inbound_messages (message_id, workspace_id, message_session_id, kind, encryption_state, ack_status, sender_ref, payload_ref, last_error_code, last_error_message, last_error_retryable, received_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
ON CONFLICT (message_id) DO UPDATE SET encryption_state=EXCLUDED.encryption_state, ack_status=EXCLUDED.ack_status, last_error_code=EXCLUDED.last_error_code, last_error_message=EXCLUDED.last_error_message, last_error_retryable=EXCLUDED.last_error_retryable`,
			msg.GetMessageId(), workspaceID, msg.GetMessageSessionId(), msg.GetKind().String(), msg.GetEncryptionState().String(), msg.GetAckStatus().String(), msg.GetSenderRef(), msg.GetPayloadRef(), errCode(appErr), errMessage(appErr), errRetryable(appErr), timeFromProto(msg.GetReceivedAt()))
	}
	return s.pool.SendBatch(ctx, batch).Close()
}

func (s *PostgresStore) GetInboundMessage(ctx context.Context, workspaceID string, id string) (*waappv1.InboundMessage, error) {
	row := s.pool.QueryRow(ctx, `SELECT message_id,message_session_id,kind,encryption_state,ack_status,sender_ref,payload_ref,last_error_code,last_error_message,last_error_retryable,received_at FROM wa_inbound_messages WHERE workspace_id=$1 AND message_id=$2`, workspaceID, id)
	var r messageRow
	if err := row.Scan(&r.id, &r.sessionID, &r.kind, &r.encryptionState, &r.ackStatus, &r.senderRef, &r.payloadRef, &r.errCode, &r.errMessage, &r.errRetryable, &r.receivedAt); err != nil {
		return nil, notFound(err, waappv1.WaErrorCode_WA_ERROR_CODE_MESSAGE_NOT_FOUND, "message not found")
	}
	return r.toProto(), nil
}

func (s *PostgresStore) SaveDecryptedMessage(ctx context.Context, msg *waappv1.DecryptedMessage, workspaceID string) error {
	appErr := errorFromProto(msg.GetLastError())
	_, err := s.pool.Exec(ctx, `INSERT INTO wa_decrypted_messages (decrypted_message_id, workspace_id, message_id, status, plaintext_ref, plaintext_redacted, plaintext_secret_ref, last_error_code, last_error_message, last_error_retryable, decrypted_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
ON CONFLICT (decrypted_message_id) DO UPDATE SET status=EXCLUDED.status, plaintext_ref=EXCLUDED.plaintext_ref, plaintext_redacted=EXCLUDED.plaintext_redacted, plaintext_secret_ref=EXCLUDED.plaintext_secret_ref, last_error_code=EXCLUDED.last_error_code, last_error_message=EXCLUDED.last_error_message, last_error_retryable=EXCLUDED.last_error_retryable, decrypted_at=EXCLUDED.decrypted_at`,
		msg.GetDecryptedMessageId(), workspaceID, msg.GetMessageId(), msg.GetStatus().String(), msg.GetPlaintextRef(), msg.GetPlaintextText().GetRedactedValue(), msg.GetPlaintextText().GetSecretRef(), errCode(appErr), errMessage(appErr), errRetryable(appErr), timeFromProto(msg.GetDecryptedAt()))
	return err
}

func (s *PostgresStore) GetDecryptedMessage(ctx context.Context, workspaceID string, id string) (*waappv1.DecryptedMessage, error) {
	row := s.pool.QueryRow(ctx, `SELECT decrypted_message_id,message_id,status,plaintext_ref,plaintext_redacted,plaintext_secret_ref,last_error_code,last_error_message,last_error_retryable,decrypted_at FROM wa_decrypted_messages WHERE workspace_id=$1 AND decrypted_message_id=$2`, workspaceID, id)
	var r decryptedRow
	if err := row.Scan(&r.id, &r.messageID, &r.status, &r.plaintextRef, &r.redacted, &r.secretRef, &r.errCode, &r.errMessage, &r.errRetryable, &r.decryptedAt); err != nil {
		return nil, notFound(err, waappv1.WaErrorCode_WA_ERROR_CODE_MESSAGE_NOT_FOUND, "decrypted message not found")
	}
	return r.toProto(), nil
}

func (s *PostgresStore) SaveCandidates(ctx context.Context, workspaceID string, candidates []*waappv1.ExtractedCandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, candidate := range candidates {
		batch.Queue(`INSERT INTO wa_extracted_candidates (candidate_id, workspace_id, message_id, decrypted_message_id, kind, redacted_value, secret_ref, confidence, extracted_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (candidate_id) DO UPDATE SET confidence=EXCLUDED.confidence, redacted_value=EXCLUDED.redacted_value, secret_ref=EXCLUDED.secret_ref`,
			candidate.GetCandidateId(), workspaceID, candidate.GetMessageId(), candidate.GetDecryptedMessageId(), candidate.GetKind().String(), candidate.GetText().GetRedactedValue(), candidate.GetText().GetSecretRef(), candidate.GetConfidence(), timeFromProto(candidate.GetExtractedAt()))
	}
	return s.pool.SendBatch(ctx, batch).Close()
}

func notFound(err error, code waappv1.WaErrorCode, message string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return NewError(code, message, false)
	}
	return err
}

func timeFromProto(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Now().UTC()
	}
	return ts.AsTime().UTC()
}

func nullableProtoTime(ts *timestamppb.Timestamp) any {
	if ts == nil {
		return nil
	}
	return ts.AsTime().UTC()
}

func sqlTime(ns sql.NullTime) *timestamppb.Timestamp {
	if !ns.Valid {
		return nil
	}
	return timestamppb.New(ns.Time.UTC())
}

func audit(createdAt time.Time, updatedAt time.Time) *waappv1.AuditStamp {
	return &waappv1.AuditStamp{CreatedAt: timestamppb.New(createdAt.UTC()), UpdatedAt: timestamppb.New(updatedAt.UTC())}
}

func errCode(err *AppError) string {
	if err == nil {
		return ""
	}
	return err.Code.String()
}

func errMessage(err *AppError) string {
	if err == nil {
		return ""
	}
	return err.Message
}

func errRetryable(err *AppError) bool {
	return err != nil && err.Retryable
}

func protoError(code string, message string, retryable bool) *waappv1.WaError {
	if code == "" {
		return nil
	}
	return &waappv1.WaError{Code: waappv1.WaErrorCode(waappv1.WaErrorCode_value[code]), Message: message, Retryable: retryable}
}
