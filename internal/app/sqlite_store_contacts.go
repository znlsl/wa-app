package app

import (
	"context"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
)

func (s *SQLiteStore) SaveWAContacts(ctx context.Context, contacts []*waappv1.WAContact) error {
	if len(contacts) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, contact := range contacts {
		if contact == nil || contact.GetContactId() == "" || contact.GetWaAccountId() == "" {
			continue
		}
		payload, err := sqliteMarshal(contact)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO wa_sqlite_contacts (id,wa_account_id,updated_at,payload) VALUES (?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  wa_account_id=excluded.wa_account_id,
  updated_at=excluded.updated_at,
  payload=CASE
    WHEN COALESCE(json_extract(wa_sqlite_contacts.payload, '$.display_name'), '') IN ('', '未知联系人') THEN excluded.payload
    WHEN COALESCE(json_extract(wa_sqlite_contacts.payload, '$.display_name'), '') LIKE '联系人 %' THEN excluded.payload
    WHEN COALESCE(json_extract(wa_sqlite_contacts.payload, '$.display_name'), '') LIKE 'LID %' THEN excluded.payload
    WHEN COALESCE(NULLIF(json_extract(wa_sqlite_contacts.payload, '$.number'), ''), NULLIF(json_extract(excluded.payload, '$.number'), ''), '') <> ''
      AND COALESCE(json_extract(wa_sqlite_contacts.payload, '$.display_name'), '') = '+' || COALESCE(NULLIF(json_extract(wa_sqlite_contacts.payload, '$.number'), ''), NULLIF(json_extract(excluded.payload, '$.number'), ''), '') THEN excluded.payload
    ELSE wa_sqlite_contacts.payload
  END`, contact.GetContactId(), contact.GetWaAccountId(), sqliteTimeValue(timeFromProto(contact.GetAudit().GetUpdatedAt())), payload); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetWAContact(ctx context.Context, contactID string) (*waappv1.WAContact, error) {
	contact := &waappv1.WAContact{}
	if err := s.loadPayload(ctx, "wa_sqlite_contacts", contactID, contact, waappv1.WaErrorCode_WA_ERROR_CODE_MESSAGE_NOT_FOUND, "WA contact not found"); err != nil {
		return nil, err
	}
	enrichWAContactFallback(contact)
	return contact, nil
}

func (s *SQLiteStore) ListWAContacts(ctx context.Context, waAccountIDValue string, cursorValue string, limit int) ([]*waappv1.WAContact, string, error) {
	cursor, err := decodeKeysetCursor(cursorValue)
	if err != nil {
		return nil, "", NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, err.Error(), false)
	}
	limit = normalizePageLimit(limit)
	lookahead := keysetLookaheadLimit(limit)
	query := `SELECT payload FROM wa_sqlite_contacts WHERE wa_account_id=?`
	args := []any{waAccountIDValue}
	if hasKeysetCursor(cursor) {
		value := sqliteTimeValue(cursor.UpdatedAt)
		query += ` AND (updated_at < ? OR (updated_at = ? AND id < ?))`
		args = append(args, value, value, cursor.ID)
	}
	query += ` ORDER BY updated_at DESC, id DESC LIMIT ?`
	args = append(args, lookahead)
	items, err := sqliteListPayloads(ctx, s.db, func() *waappv1.WAContact { return &waappv1.WAContact{} }, query, args...)
	if err != nil {
		return nil, "", err
	}
	for _, item := range items {
		enrichWAContactFallback(item)
	}
	items, nextCursor := newKeysetPage(items, limit, func(contact *waappv1.WAContact) keysetCursor {
		return keysetCursorValue(timeFromProto(contact.GetAudit().GetUpdatedAt()), contact.GetContactId())
	})
	return items, nextCursor, nil
}
