CREATE TABLE IF NOT EXISTS wa_app_artifacts (
  artifact_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  label TEXT NOT NULL,
  version_label TEXT NOT NULL DEFAULT '',
  sha256 TEXT NOT NULL DEFAULT '',
  observed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS wa_protocol_profiles (
  protocol_profile_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  app_artifact_id TEXT NOT NULL REFERENCES wa_app_artifacts(artifact_id),
  display_name TEXT NOT NULL,
  app_version TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  capabilities TEXT[] NOT NULL DEFAULT '{}',
  registration_flows TEXT[] NOT NULL DEFAULT '{}',
  message_transports TEXT[] NOT NULL DEFAULT '{}',
  discovered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS wa_accounts (
  wa_account_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  e164_number TEXT NOT NULL,
  country_calling_code TEXT NOT NULL DEFAULT '',
  national_number TEXT NOT NULL DEFAULT '',
  country_iso2 TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, e164_number)
);

CREATE TABLE IF NOT EXISTS wa_client_profiles (
  client_profile_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  wa_account_id TEXT NOT NULL REFERENCES wa_accounts(wa_account_id),
  protocol_profile_id TEXT NOT NULL REFERENCES wa_protocol_profiles(protocol_profile_id),
  status TEXT NOT NULL,
  registration_key_state TEXT NOT NULL,
  messaging_key_state TEXT NOT NULL,
  state_ref TEXT NOT NULL DEFAULT '',
  last_error_code TEXT NOT NULL DEFAULT '',
  last_error_message TEXT NOT NULL DEFAULT '',
  last_error_retryable BOOLEAN NOT NULL DEFAULT false,
  last_used_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS wa_account_probes (
  account_probe_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  wa_account_id TEXT NOT NULL REFERENCES wa_accounts(wa_account_id),
  client_profile_id TEXT NOT NULL REFERENCES wa_client_profiles(client_profile_id),
  status TEXT NOT NULL,
  supported_methods TEXT[] NOT NULL DEFAULT '{}',
  last_error_code TEXT NOT NULL DEFAULT '',
  last_error_message TEXT NOT NULL DEFAULT '',
  last_error_retryable BOOLEAN NOT NULL DEFAULT false,
  probed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS wa_verification_requests (
  verification_request_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  wa_account_id TEXT NOT NULL REFERENCES wa_accounts(wa_account_id),
  client_profile_id TEXT NOT NULL REFERENCES wa_client_profiles(client_profile_id),
  delivery_method TEXT NOT NULL,
  status TEXT NOT NULL,
  expected_code_length INTEGER NOT NULL DEFAULT 0,
  last_error_code TEXT NOT NULL DEFAULT '',
  last_error_message TEXT NOT NULL DEFAULT '',
  last_error_retryable BOOLEAN NOT NULL DEFAULT false,
  requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS wa_registrations (
  registration_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  verification_request_id TEXT NOT NULL REFERENCES wa_verification_requests(verification_request_id),
  wa_account_id TEXT NOT NULL REFERENCES wa_accounts(wa_account_id),
  client_profile_id TEXT NOT NULL REFERENCES wa_client_profiles(client_profile_id),
  status TEXT NOT NULL,
  registered_identity_id TEXT NOT NULL DEFAULT '',
  service_account_id TEXT NOT NULL DEFAULT '',
  service_login_id TEXT NOT NULL DEFAULT '',
  last_error_code TEXT NOT NULL DEFAULT '',
  last_error_message TEXT NOT NULL DEFAULT '',
  last_error_retryable BOOLEAN NOT NULL DEFAULT false,
  submitted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS wa_login_states (
  login_state_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  registration_id TEXT NOT NULL REFERENCES wa_registrations(registration_id),
  wa_account_id TEXT NOT NULL REFERENCES wa_accounts(wa_account_id),
  client_profile_id TEXT NOT NULL REFERENCES wa_client_profiles(client_profile_id),
  registered_identity_id TEXT NOT NULL,
  service_account_id TEXT NOT NULL DEFAULT '',
  service_login_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  state_ref TEXT NOT NULL DEFAULT '',
  last_error_code TEXT NOT NULL DEFAULT '',
  last_error_message TEXT NOT NULL DEFAULT '',
  last_error_retryable BOOLEAN NOT NULL DEFAULT false,
  registered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_verified_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, registration_id),
  UNIQUE (workspace_id, registered_identity_id)
);

CREATE TABLE IF NOT EXISTS wa_message_sessions (
  message_session_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  wa_account_id TEXT NOT NULL REFERENCES wa_accounts(wa_account_id),
  client_profile_id TEXT NOT NULL REFERENCES wa_client_profiles(client_profile_id),
  registered_identity_id TEXT NOT NULL,
  protocol_profile_id TEXT NOT NULL REFERENCES wa_protocol_profiles(protocol_profile_id),
  status TEXT NOT NULL,
  last_error_code TEXT NOT NULL DEFAULT '',
  last_error_message TEXT NOT NULL DEFAULT '',
  last_error_retryable BOOLEAN NOT NULL DEFAULT false,
  opened_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ,
  closed_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS wa_inbound_messages (
  message_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  message_session_id TEXT NOT NULL REFERENCES wa_message_sessions(message_session_id),
  kind TEXT NOT NULL,
  encryption_state TEXT NOT NULL,
  ack_status TEXT NOT NULL,
  sender_ref TEXT NOT NULL DEFAULT '',
  payload_ref TEXT NOT NULL DEFAULT '',
  last_error_code TEXT NOT NULL DEFAULT '',
  last_error_message TEXT NOT NULL DEFAULT '',
  last_error_retryable BOOLEAN NOT NULL DEFAULT false,
  received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS wa_decrypted_messages (
  decrypted_message_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  message_id TEXT NOT NULL REFERENCES wa_inbound_messages(message_id),
  status TEXT NOT NULL,
  plaintext_ref TEXT NOT NULL DEFAULT '',
  plaintext_redacted TEXT NOT NULL DEFAULT '',
  plaintext_secret_ref TEXT NOT NULL DEFAULT '',
  last_error_code TEXT NOT NULL DEFAULT '',
  last_error_message TEXT NOT NULL DEFAULT '',
  last_error_retryable BOOLEAN NOT NULL DEFAULT false,
  decrypted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS wa_extracted_candidates (
  candidate_id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  message_id TEXT NOT NULL REFERENCES wa_inbound_messages(message_id),
  decrypted_message_id TEXT NOT NULL DEFAULT '',
  kind TEXT NOT NULL,
  redacted_value TEXT NOT NULL DEFAULT '',
  secret_ref TEXT NOT NULL DEFAULT '',
  confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
  extracted_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
DECLARE
  wa_table_name TEXT;
  wa_constraint_name TEXT;
BEGIN
  IF to_regclass('wa_targets') IS NOT NULL THEN
    INSERT INTO wa_accounts (
      wa_account_id,
      workspace_id,
      e164_number,
      country_calling_code,
      national_number,
      country_iso2,
      status,
      created_at,
      updated_at
    )
    SELECT
      target_id,
      workspace_id,
      e164_number,
      country_calling_code,
      national_number,
      country_iso2,
      status,
      created_at,
      updated_at
    FROM wa_targets
    ON CONFLICT (wa_account_id) DO UPDATE SET
      workspace_id = EXCLUDED.workspace_id,
      e164_number = EXCLUDED.e164_number,
      country_calling_code = EXCLUDED.country_calling_code,
      national_number = EXCLUDED.national_number,
      country_iso2 = EXCLUDED.country_iso2,
      status = EXCLUDED.status,
      updated_at = EXCLUDED.updated_at;
  END IF;

  FOR wa_table_name IN
    SELECT unnest(ARRAY[
      'wa_client_profiles',
      'wa_account_probes',
      'wa_verification_requests',
      'wa_registrations',
      'wa_login_states',
      'wa_message_sessions'
    ])
  LOOP
    IF EXISTS (
      SELECT 1
      FROM information_schema.columns
      WHERE table_schema = current_schema()
        AND table_name = wa_table_name
        AND column_name = 'target_id'
    ) AND NOT EXISTS (
      SELECT 1
      FROM information_schema.columns
      WHERE table_schema = current_schema()
        AND table_name = wa_table_name
        AND column_name = 'wa_account_id'
    ) THEN
      EXECUTE format('ALTER TABLE %I RENAME COLUMN target_id TO wa_account_id', wa_table_name);
    END IF;
  END LOOP;

  FOR wa_constraint_name, wa_table_name IN
    SELECT c.conname, rel.relname
    FROM pg_constraint c
    JOIN pg_class rel ON rel.oid = c.conrelid
    JOIN pg_class ref ON ref.oid = c.confrelid
    JOIN pg_namespace ns ON ns.oid = rel.relnamespace
    WHERE ns.nspname = current_schema()
      AND c.contype = 'f'
      AND ref.relname = 'wa_targets'
      AND rel.relname = ANY (ARRAY[
        'wa_client_profiles',
        'wa_account_probes',
        'wa_verification_requests',
        'wa_registrations',
        'wa_login_states',
        'wa_message_sessions'
      ])
  LOOP
    EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', wa_table_name, wa_constraint_name);
  END LOOP;

  FOR wa_table_name IN
    SELECT unnest(ARRAY[
      'wa_client_profiles',
      'wa_account_probes',
      'wa_verification_requests',
      'wa_registrations',
      'wa_login_states',
      'wa_message_sessions'
    ])
  LOOP
    IF EXISTS (
      SELECT 1
      FROM information_schema.columns
      WHERE table_schema = current_schema()
        AND table_name = wa_table_name
        AND column_name = 'wa_account_id'
    ) AND NOT EXISTS (
      SELECT 1
      FROM pg_constraint c
      JOIN pg_class rel ON rel.oid = c.conrelid
      JOIN pg_namespace ns ON ns.oid = rel.relnamespace
      WHERE ns.nspname = current_schema()
        AND rel.relname = wa_table_name
        AND c.contype = 'f'
        AND c.conname = wa_table_name || '_wa_account_id_fkey'
    ) THEN
      EXECUTE format(
        'ALTER TABLE %I ADD CONSTRAINT %I FOREIGN KEY (wa_account_id) REFERENCES wa_accounts(wa_account_id)',
        wa_table_name,
        wa_table_name || '_wa_account_id_fkey'
      );
    END IF;
  END LOOP;

  IF to_regclass('wa_targets') IS NOT NULL THEN
    DROP TABLE wa_targets;
  END IF;
END $$;
