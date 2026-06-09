package app

import (
	"context"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) SaveWAContacts(ctx context.Context, contacts []*waappv1.WAContact) error {
	if len(contacts) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	queued := 0
	for _, contact := range contacts {
		if contact == nil || contact.GetContactId() == "" || contact.GetWaAccountId() == "" {
			continue
		}
		createdAt := timeFromProto(contact.GetAudit().GetCreatedAt())
		updatedAt := timeFromProto(contact.GetAudit().GetUpdatedAt())
		batch.Queue(`INSERT INTO wa_contacts (contact_id, wa_account_id, jid, number, display_name, wa_name, verified_name, profile_picture_id, kind, is_whatsapp_user, is_reachable, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (contact_id) DO UPDATE SET
  jid=EXCLUDED.jid,
  number=COALESCE(NULLIF(EXCLUDED.number,''), wa_contacts.number),
  display_name=CASE
    WHEN NULLIF(EXCLUDED.display_name,'') IS NULL THEN wa_contacts.display_name
    WHEN wa_contacts.display_name='' OR wa_contacts.display_name='未知联系人' OR wa_contacts.display_name LIKE '联系人 %' OR wa_contacts.display_name LIKE 'LID %' THEN EXCLUDED.display_name
    WHEN COALESCE(NULLIF(wa_contacts.number,''), NULLIF(EXCLUDED.number,''), '') <> '' AND wa_contacts.display_name='+' || COALESCE(NULLIF(wa_contacts.number,''), NULLIF(EXCLUDED.number,''), '') THEN EXCLUDED.display_name
    ELSE wa_contacts.display_name
  END,
  wa_name=COALESCE(NULLIF(EXCLUDED.wa_name,''), wa_contacts.wa_name),
  verified_name=COALESCE(NULLIF(EXCLUDED.verified_name,''), wa_contacts.verified_name),
  profile_picture_id=COALESCE(NULLIF(EXCLUDED.profile_picture_id,''), wa_contacts.profile_picture_id),
  kind=CASE WHEN EXCLUDED.kind IN ('WA_CONTACT_KIND_UNSPECIFIED','WA_CONTACT_KIND_USER') AND wa_contacts.kind <> '' THEN wa_contacts.kind ELSE EXCLUDED.kind END,
  is_whatsapp_user=wa_contacts.is_whatsapp_user OR EXCLUDED.is_whatsapp_user,
  is_reachable=wa_contacts.is_reachable OR EXCLUDED.is_reachable,
  updated_at=GREATEST(wa_contacts.updated_at, EXCLUDED.updated_at)`, contact.GetContactId(), contact.GetWaAccountId(), contact.GetJid(), contact.GetNumber(), contact.GetDisplayName(), contact.GetWaName(), contact.GetVerifiedName(), contact.GetProfilePictureId(), contactKindStorageValue(contact), contact.GetIsWhatsappUser(), contact.GetIsReachable(), createdAt, updatedAt)
		queued++
	}
	if queued == 0 {
		return nil
	}
	br := s.pool.SendBatch(ctx, batch)
	return br.Close()
}

func (s *PostgresStore) GetWAContact(ctx context.Context, contactID string) (*waappv1.WAContact, error) {
	var r contactRow
	row := s.pool.QueryRow(ctx, contactSelectSQL+` WHERE contact_id=$1`, contactID)
	if err := scanContactRow(row, &r); err != nil {
		return nil, notFound(err, waappv1.WaErrorCode_WA_ERROR_CODE_MESSAGE_NOT_FOUND, "WA contact not found")
	}
	return r.toProto(), nil
}

func (s *PostgresStore) ListWAContacts(ctx context.Context, waAccountIDValue string, cursorValue string, limit int) ([]*waappv1.WAContact, string, error) {
	cursor, err := decodeKeysetCursor(cursorValue)
	if err != nil {
		return nil, "", NewError(waappv1.WaErrorCode_WA_ERROR_CODE_VALIDATION_FAILED, err.Error(), false)
	}
	limit = normalizePageLimit(limit)
	rows, err := s.queryContactPage(ctx, waAccountIDValue, cursor, keysetLookaheadLimit(limit))
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := []*waappv1.WAContact{}
	for rows.Next() {
		var r contactRow
		if err := scanContactRow(rows, &r); err != nil {
			return nil, "", err
		}
		items = append(items, r.toProto())
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	items, nextCursor := newKeysetPage(items, limit, func(contact *waappv1.WAContact) keysetCursor {
		return keysetCursorValue(timeFromProto(contact.GetAudit().GetUpdatedAt()), contact.GetContactId())
	})
	return items, nextCursor, nil
}

func (s *PostgresStore) queryContactPage(ctx context.Context, waAccountIDValue string, cursor keysetCursor, limit int) (pgx.Rows, error) {
	if !hasKeysetCursor(cursor) {
		return s.pool.Query(ctx, contactSelectSQL+` WHERE wa_account_id=$1 ORDER BY updated_at DESC, contact_id DESC LIMIT $2`, waAccountIDValue, limit)
	}
	return s.pool.Query(ctx, contactSelectSQL+` WHERE wa_account_id=$1 AND (updated_at, contact_id) < ($2, $3) ORDER BY updated_at DESC, contact_id DESC LIMIT $4`, waAccountIDValue, cursor.UpdatedAt, cursor.ID, limit)
}

const contactSelectSQL = `SELECT contact_id,wa_account_id,jid,number,display_name,wa_name,verified_name,profile_picture_id,kind,is_whatsapp_user,is_reachable,created_at,updated_at FROM wa_contacts`

type contactScanner interface {
	Scan(...any) error
}

func scanContactRow(scanner contactScanner, r *contactRow) error {
	return scanner.Scan(&r.id, &r.waAccountIDValue, &r.jid, &r.number, &r.displayName, &r.waName, &r.verifiedName, &r.profilePictureID, &r.kind, &r.isWhatsAppUser, &r.isReachable, &r.createdAt, &r.updatedAt)
}
