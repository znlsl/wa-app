import type { ThreadMessageLike } from '@assistant-ui/react';
import type { WAContact as WAContactRecord } from '../proto/byte/v/forge/waapp/v1/contacts';
import { WAContactKind } from '../proto/byte/v/forge/waapp/v1/contacts';
import type { OtpMessage } from '../proto/byte/v/forge/waapp/v1/extraction';
import { WaOtpSource } from '../proto/byte/v/forge/waapp/v1/extraction';
import type { AccountMessage } from '../proto/byte/v/forge/waapp/v1/messaging';
import { AccountMessageDirection, AccountMessageSource, InboundMessageKind, MessageEncryptionState } from '../proto/byte/v/forge/waapp/v1/messaging';

export type WaChatEvent = {
  id: string;
  contactID: string;
  text: string;
  at?: Date;
  source: string;
  sender: string;
  copyText?: string;
  outgoing: boolean;
};

export type WaContact = {
  id: string;
  title: string;
  subtitle: string;
  kind: WAContactKind;
  count: number;
  lastAt?: Date;
  profilePictureURL?: string;
};

export type WaChatMeta = Pick<WaChatEvent, 'source' | 'sender' | 'copyText' | 'outgoing'> & { displayText: string };

export function buildWaChatEvents(messages: AccountMessage[], otps: OtpMessage[]) {
  const chatMessages = messages.filter((message) => message.kind === InboundMessageKind.INBOUND_MESSAGE_KIND_MESSAGE);
  const events = [...chatMessages].sort(byMessageTime).map(messageEvent).filter(isKnownChatEvent);
  const seenMessages = new Set(chatMessages.map((message) => message.account_message_id).filter(Boolean));
  for (const item of [...otps].sort(byOtpTime)) {
    if (!item.message_id || !seenMessages.has(item.message_id)) events.push(otpEvent(item));
  }
  return events.sort((a, b) => (a.at?.getTime() || 0) - (b.at?.getTime() || 0));
}

export function buildWaContacts(events: WaChatEvent[], records: WAContactRecord[] = []) {
  const contacts = new Map<string, WaContact>();
  for (const record of records) {
    const id = recordContactID(record);
    if (!id) continue;
    contacts.set(id, { id, title: recordTitle(record), subtitle: recordSubtitle(record), kind: record.kind || WAContactKind.WA_CONTACT_KIND_UNSPECIFIED, count: 0, lastAt: parseDate(record.audit?.updated_at), profilePictureURL: contactProfilePictureURL(record) });
  }
  for (const event of events) {
    const current = contacts.get(event.contactID);
    contacts.set(event.contactID, {
      id: event.contactID,
      title: current?.title || event.sender,
      subtitle: current?.subtitle || event.source,
      kind: current?.kind || WAContactKind.WA_CONTACT_KIND_UNSPECIFIED,
      count: (current?.count || 0) + 1,
      lastAt: newerDate(current?.lastAt, event.at),
      profilePictureURL: current?.profilePictureURL,
    });
  }
  return [...contacts.values()].sort(compareContacts);
}

export function filterWaEvents(events: WaChatEvent[], contactID: string) {
  return contactID ? events.filter((event) => event.contactID === contactID) : [];
}

export function toAssistantMessage(event: WaChatEvent): ThreadMessageLike {
  return {
    id: event.id,
    role: event.outgoing ? 'user' : 'assistant',
    content: event.text,
    createdAt: event.at,
    status: { type: 'complete', reason: 'stop' },
    metadata: { custom: { source: event.source, sender: event.sender, copyText: event.copyText, outgoing: event.outgoing, displayText: event.text } satisfies WaChatMeta },
  };
}

export function formatChatTime(value?: Date) {
  if (!value) return '';
  return value.toLocaleString([], { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' });
}

function otpEvent(item: OtpMessage): WaChatEvent {
  const value = item.otp?.value || item.otp?.redacted_value || '-';
  return {
    id: item.otp_message_id || item.message_id || `${item.wa_account_id}-${item.source_party}-${item.received_at}`,
    contactID: contactID(item),
    text: value,
    at: parseDate(item.received_at),
    source: otpSourceLabel(item.source),
    sender: sourcePartyLabel(item.source_party),
    copyText: item.otp?.value || '',
    outgoing: false,
  };
}

function messageEvent(item: AccountMessage): WaChatEvent {
  const text = item.text?.value || item.text?.redacted_value || messageStateLabel(item.encryption_state);
  return {
    id: item.account_message_id || item.message_session_id || `${item.wa_account_id}-${item.received_at}`,
    contactID: (item.contact_ref || item.sender_ref || '').trim() || 'unknown',
    text,
    at: parseDate(item.received_at),
    source: messageSourceLabel(item),
    sender: sourcePartyLabel(item.sender_ref || item.contact_ref),
    copyText: item.text?.value || '',
    outgoing: item.direction === AccountMessageDirection.ACCOUNT_MESSAGE_DIRECTION_OUTBOUND,
  };
}

function contactID(item: OtpMessage) { return (item.source_party || '').trim() || 'unknown'; }

function newerDate(a?: Date, b?: Date) {
  if (!a) return b;
  if (!b) return a;
  return a.getTime() > b.getTime() ? a : b;
}

function byOtpTime(a: OtpMessage, b: OtpMessage) {
  return (parseDate(a.received_at)?.getTime() || 0) - (parseDate(b.received_at)?.getTime() || 0);
}

function byMessageTime(a: AccountMessage, b: AccountMessage) {
  return (parseDate(a.received_at)?.getTime() || 0) - (parseDate(b.received_at)?.getTime() || 0);
}

function sourcePartyLabel(value?: string) {
  const raw = (value || '').trim();
  if (!raw) return '未知联系人';
  if (raw === 's.whatsapp.net') return '系统';
  if (raw.endsWith('@s.whatsapp.net')) return `+${raw.replace(/@s\.whatsapp\.net$/, '')}`;
  if (raw.endsWith('@lid')) return '未知联系人';
  return raw;
}

function recordContactID(record: WAContactRecord) { return (record.jid || record.number || record.contact_id || '').trim(); }

function contactProfilePictureURL(record: WAContactRecord) {
  return record.profile_picture_id && record.contact_id ? `/api/wa/contacts/${encodeURIComponent(record.contact_id)}/profile-picture` : undefined;
}

function recordTitle(record: WAContactRecord) {
  return safeContactName(record.display_name) || record.verified_name || record.wa_name || phoneTitle(record.number) || sourcePartyLabel(record.jid) || '未知联系人';
}

function recordSubtitle(record: WAContactRecord) {
  if (record.kind === WAContactKind.WA_CONTACT_KIND_GROUP) return '群组';
  if (record.kind === WAContactKind.WA_CONTACT_KIND_BUSINESS || record.verified_name) return '企业账号';
  if (record.number) return `+${record.number}`;
  if (record.jid?.endsWith('@lid')) return 'WA 联系人';
  return sourcePartyLabel(record.jid);
}

function compareContacts(a: WaContact, b: WaContact) {
  const delta = (b.lastAt?.getTime() || 0) - (a.lastAt?.getTime() || 0);
  return delta || a.title.localeCompare(b.title);
}

function isKnownChatEvent(event: WaChatEvent) {
  return event.contactID.trim() !== '' && event.contactID !== 'unknown';
}

function otpSourceLabel(source?: WaOtpSource) {
  if (source === WaOtpSource.WA_OTP_SOURCE_LONG_CONNECTION) return '长连接';
  if (source === WaOtpSource.WA_OTP_SOURCE_IMPORTED_HISTORY) return '导入历史';
  if (source === WaOtpSource.WA_OTP_SOURCE_AUTO_EXTRACTION) return '自动解析';
  return '消息';
}

function messageSourceLabel(item: AccountMessage) {
  if (item.source === AccountMessageSource.ACCOUNT_MESSAGE_SOURCE_IMPORTED_HISTORY) return '导入历史';
  if (item.encryption_state === MessageEncryptionState.MESSAGE_ENCRYPTION_STATE_DECRYPTION_FAILED) return '解密失败';
  if (item.encryption_state === MessageEncryptionState.MESSAGE_ENCRYPTION_STATE_ENCRYPTED) return '待解密';
  return 'WA 消息';
}

function messageStateLabel(state?: MessageEncryptionState) {
  if (state === MessageEncryptionState.MESSAGE_ENCRYPTION_STATE_ENCRYPTED) return '消息待解密';
  if (state === MessageEncryptionState.MESSAGE_ENCRYPTION_STATE_DECRYPTION_FAILED) return '消息解密失败';
  return '空消息';
}

function safeContactName(value?: string) {
  const name = (value || '').trim();
  if (!name || name === '未知联系人' || name.startsWith('LID ')) return '';
  return name;
}

function phoneTitle(number?: string) {
  const value = (number || '').trim();
  return value ? `+${value}` : '';
}

function parseDate(value?: string) {
  if (!value) return undefined;
  const time = new Date(value);
  return Number.isNaN(time.getTime()) ? undefined : time;
}
