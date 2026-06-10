package app

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	waappv1 "github.com/byte-v-forge/wa-app/gen/go/byte/v/forge/waapp/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) saveInboundMessagesForSession(ctx context.Context, session *waappv1.MessageSession, messages []*waappv1.InboundMessage) error {
	if err := s.store.SaveInboundMessages(ctx, messages); err != nil {
		return err
	}
	contacts := contactsFromInboundMessages(session.GetWaAccountId(), messages, s.clock.Now())
	if len(contacts) == 0 {
		return nil
	}
	return s.store.SaveWAContacts(ctx, contacts)
}

func contactsFromInboundMessages(accountID string, messages []*waappv1.InboundMessage, now time.Time) []*waappv1.WAContact {
	contacts := map[string]*waappv1.WAContact{}
	for _, msg := range messages {
		if msg.GetKind() != waappv1.InboundMessageKind_INBOUND_MESSAGE_KIND_MESSAGE {
			continue
		}
		contactRef := contactRefForMessage(msg.GetContactRef(), msg.GetSenderRef())
		contact := contactFromRef(accountID, contactRef, timeFromProto(msg.GetReceivedAt()), now)
		if contact == nil {
			continue
		}
		current := contacts[contact.GetJid()]
		if current == nil || timeFromProto(contact.GetAudit().GetUpdatedAt()).After(timeFromProto(current.GetAudit().GetUpdatedAt())) {
			contacts[contact.GetJid()] = contact
		}
	}
	out := make([]*waappv1.WAContact, 0, len(contacts))
	for _, contact := range contacts {
		out = append(out, contact)
	}
	return out
}

func contactFromRef(accountID string, ref string, seenAt time.Time, now time.Time) *waappv1.WAContact {
	jid := normalizeWAJID(ref)
	if accountID == "" || jid == "" || jid == "unknown" {
		return nil
	}
	kind := contactKindForJID(jid)
	updatedAt := seenAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	return &waappv1.WAContact{
		ContactId:      "wact_" + stableID(accountID+":"+jid),
		WaAccountId:    accountID,
		Jid:            jid,
		Number:         contactNumberForJID(jid),
		DisplayName:    fallbackWAContactDisplayName(kind, jid, contactNumberForJID(jid)),
		Kind:           kind,
		IsWhatsappUser: kind == waappv1.WAContactKind_WA_CONTACT_KIND_USER || kind == waappv1.WAContactKind_WA_CONTACT_KIND_BUSINESS || kind == waappv1.WAContactKind_WA_CONTACT_KIND_GROUP,
		IsReachable:    kind == waappv1.WAContactKind_WA_CONTACT_KIND_USER || kind == waappv1.WAContactKind_WA_CONTACT_KIND_BUSINESS || kind == waappv1.WAContactKind_WA_CONTACT_KIND_GROUP,
		Audit:          &waappv1.AuditStamp{CreatedAt: timestamppb.New(updatedAt.UTC()), UpdatedAt: timestamppb.New(updatedAt.UTC())},
	}
}

func contactFromDecryptedMessage(accountID string, msg *waappv1.InboundMessage, text string, now time.Time) *waappv1.WAContact {
	if msg == nil || msg.GetKind() != waappv1.InboundMessageKind_INBOUND_MESSAGE_KIND_MESSAGE {
		return nil
	}
	contact := contactFromRef(accountID, contactRefForMessage(msg.GetContactRef(), msg.GetSenderRef()), timeFromProto(msg.GetReceivedAt()), now)
	if contact == nil {
		return nil
	}
	name, business := inferWAContactDisplayName(text, contact.GetJid())
	if name == "" {
		return nil
	}
	contact.DisplayName = name
	if business {
		contact.Kind = waappv1.WAContactKind_WA_CONTACT_KIND_BUSINESS
	}
	return contact
}

func contactsFromContactHints(accountID string, msg *waappv1.InboundMessage, hints []waContactHint, now time.Time) []*waappv1.WAContact {
	if accountID == "" || len(hints) == 0 {
		return nil
	}
	seenAt := now
	if msg != nil {
		if receivedAt := timeFromProto(msg.GetReceivedAt()); !receivedAt.IsZero() {
			seenAt = receivedAt
		}
	}
	contacts := []*waappv1.WAContact{}
	for _, hint := range hints {
		contact := contactFromContactHint(accountID, hint, seenAt, now)
		if contact == nil {
			continue
		}
		contacts = append(contacts, contact)
	}
	return dedupeWAContacts(contacts)
}

func contactFromContactHint(accountID string, hint waContactHint, seenAt time.Time, now time.Time) *waappv1.WAContact {
	hint = hint.normalized()
	if !hint.valid() {
		return nil
	}
	contact := contactFromRef(accountID, hint.LIDJID, seenAt, now)
	if contact == nil {
		return nil
	}
	contact.Number = contactNumberForJID(hint.PNJID)
	contact.DisplayName = firstNonEmpty(hint.DisplayName, hint.VerifiedName, hint.WAName, hint.Username, fallbackWAContactDisplayName(contact.GetKind(), contact.GetJid(), contact.GetNumber()))
	contact.WaName = firstNonEmpty(hint.WAName, hint.Username)
	contact.VerifiedName = hint.VerifiedName
	if hint.VerifiedName != "" {
		contact.Kind = waappv1.WAContactKind_WA_CONTACT_KIND_BUSINESS
	}
	normalizeWAContactNames(contact)
	return contact
}

func normalizeWAJID(ref string) string {
	value := strings.TrimSpace(ref)
	if value == "" || value == "s.whatsapp.net" {
		return ""
	}
	if strings.Contains(value, "@") {
		return value
	}
	digits := digitsOnly(value)
	if digits != "" && digits == value {
		return digits + "@s.whatsapp.net"
	}
	return value
}

func contactNumberForJID(jid string) string {
	local, domain, ok := strings.Cut(jid, "@")
	if !ok || domain != "s.whatsapp.net" {
		return ""
	}
	return digitsOnly(local)
}

func contactKindForJID(jid string) waappv1.WAContactKind {
	switch {
	case jid == "status@broadcast" || strings.HasSuffix(jid, "@broadcast"):
		return waappv1.WAContactKind_WA_CONTACT_KIND_SYSTEM
	case strings.HasSuffix(jid, "@g.us"):
		return waappv1.WAContactKind_WA_CONTACT_KIND_GROUP
	case strings.HasSuffix(jid, "@lid"), strings.HasSuffix(jid, "@s.whatsapp.net"):
		return waappv1.WAContactKind_WA_CONTACT_KIND_USER
	case strings.Contains(jid, "interop"):
		return waappv1.WAContactKind_WA_CONTACT_KIND_INTEROP
	default:
		return waappv1.WAContactKind_WA_CONTACT_KIND_SYSTEM
	}
}

func fallbackWAContactDisplayName(kind waappv1.WAContactKind, jid string, number string) string {
	if number != "" {
		return "+" + number
	}
	local, _, _ := strings.Cut(jid, "@")
	local = strings.TrimSpace(local)
	switch kind {
	case waappv1.WAContactKind_WA_CONTACT_KIND_GROUP:
		return fallbackContactName("群组", local)
	case waappv1.WAContactKind_WA_CONTACT_KIND_BUSINESS:
		return fallbackContactName("联系人", local)
	case waappv1.WAContactKind_WA_CONTACT_KIND_INTEROP:
		return fallbackContactName("互通联系人", local)
	case waappv1.WAContactKind_WA_CONTACT_KIND_SYSTEM:
		if jid == "status@broadcast" {
			return "状态"
		}
		return fallbackContactName("系统联系人", local)
	case waappv1.WAContactKind_WA_CONTACT_KIND_USER:
		if strings.HasSuffix(jid, "@lid") {
			return "未知联系人"
		}
		return fallbackContactName("联系人", local)
	default:
		return fallbackContactName("联系人", local)
	}
}

func storedWAContactDisplayName(value string, number string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if contactNameNeedsResolution(value, number) {
		return ""
	}
	return value
}

func enrichWAContactFallback(contact *waappv1.WAContact) {
	if contact == nil {
		return
	}
	normalizeWAContactNames(contact)
	contact.DisplayName = storedWAContactDisplayName(contact.GetDisplayName(), contact.GetNumber())
	if contact.GetDisplayName() != "" {
		return
	}
	contact.DisplayName = fallbackWAContactDisplayName(contact.GetKind(), contact.GetJid(), contact.GetNumber())
}

func fallbackContactName(prefix string, value string) string {
	value = shortContactToken(value)
	if value == "" {
		return prefix
	}
	return prefix + " " + value
}

func inferWAContactDisplayName(text string, jid string) (string, bool) {
	value := strings.ToLower(text)
	switch {
	case strings.Contains(value, "facebook.com") || strings.Contains(value, " facebook"):
		return "Facebook", true
	case strings.Contains(value, "instagram.com") || strings.Contains(value, " instagram"):
		return "Instagram", true
	case strings.HasSuffix(normalizeWAJID(jid), "@lid") && looksLikeVerificationCodeOnlyMessage(text):
		return "验证码服务", true
	default:
		return "", false
	}
}

func looksLikeVerificationCodeOnlyMessage(text string) bool {
	value := strings.TrimSpace(text)
	if value == "" || utf8.RuneCountInString(value) > 32 {
		return false
	}
	digits := digitsOnly(value)
	if len(digits) < 4 || len(digits) > 10 {
		return false
	}
	for _, r := range value {
		if unicode.IsDigit(r) || unicode.IsSpace(r) {
			continue
		}
		switch r {
		case '-', '–', '—', '.', ':':
			continue
		default:
			return false
		}
	}
	return true
}

func shortContactToken(value string) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= 12 {
		return value
	}
	runes := []rune(value)
	return string(runes[:8]) + "…" + string(runes[len(runes)-4:])
}
