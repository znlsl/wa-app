import type { ResultTone } from '@byte-v-forge/common-ui';
import type { WaWorkflowResponse } from './wa-api';
import { methodLabel, methodLabels } from './wa-result-labels';
import { compactJoin, extraValues, firstBool, firstNumber, firstText, record, statusIn } from './wa-result-normalize';
export type BadgeVariant = 'default' | 'secondary' | 'destructive' | 'outline';
export type VerificationMethodStatus = { key: string; label: string; available?: boolean; cooldownSeconds: number | null };
export type WaProbeStatus = {
  requestFailed: boolean;
  failureReason: string;
  registered?: boolean;
  blocked?: boolean;
  accountReachable?: boolean;
  smsAvailable?: boolean;
  smsWaitSeconds: number | null;
  smsWaitUntil: string;
  canRegister?: boolean;
  accountFlow: string;
  accountStatus: string;
  accountRawStatus: string;
  accountRawReason: string;
  accountError: string;
  smsStatus: string;
  methodStatuses: VerificationMethodStatus[];
  proxyText: string;
  rejectReason: string;
};
export type MetaItem = { label: string; value: string; tone?: ResultTone };
export function waProbeStatus(result?: WaWorkflowResponse | null): WaProbeStatus {
  const phoneStatus = record(result?.phone_status);
  const accountProbe = record(result?.account_probe);
  const smsProbe = record(result?.sms_probe);
  const proxy = record(result?.proxy);
  const registered = firstBool(phoneStatus.registered, accountProbe.registered) ?? registeredSignal(phoneStatus.account_raw_status, accountProbe.raw_status, accountProbe.status);
  const blocked = firstBool(phoneStatus.blocked, accountProbe.blocked) ?? statusIn(['blocked'], phoneStatus.account_raw_status, accountProbe.raw_status, accountProbe.status, phoneStatus.account_raw_reason, accountProbe.raw_reason);
  const accountReachable = firstBool(phoneStatus.account_reachable, accountProbe.success) ?? statusIn(['reachable', 'account_probe_status_reachable', 'ok', 'sent', 'valid', 'exists', 'incorrect'], phoneStatus.account_status, accountProbe.account_status, accountProbe.status, accountProbe.raw_status, accountProbe.raw_reason);
  const smsAvailable = firstBool(phoneStatus.sms_available, phoneStatus.can_receive_sms, smsProbe.sms_available, smsProbe.can_send_sms, smsProbe.can_receive_sms, accountProbe.can_send_sms) ?? statusIn(['available', 'sms_available', 'sent', 'waiting', 'ok'], phoneStatus.sms_status, smsProbe.sms_status, smsProbe.status);
  const smsWaitSeconds = firstNumber(phoneStatus.sms_wait_seconds, smsProbe.sms_wait_seconds, smsProbe.wait_seconds, smsProbe.retry_after_seconds, smsProbe.cooldown_seconds, smsProbe.remaining_seconds, accountProbe.sms_wait_seconds);
  const smsWaitUntil = firstText(phoneStatus.sms_wait_until, smsProbe.sms_wait_until, smsProbe.wait_until, smsProbe.retry_after_at, smsProbe.cooldown_until);
  const canRegister = firstBool(phoneStatus.can_register);
  const accountStatus = firstText(phoneStatus.account_status, accountProbe.account_status, accountProbe.status);
  const accountFlow = firstText(phoneStatus.account_flow, accountProbe.account_flow) || deriveAccountFlow({ registered, blocked, smsAvailable, accountStatus, rawReason: firstText(phoneStatus.account_raw_reason, accountProbe.raw_reason) });
  const accountRawStatus = firstText(phoneStatus.account_raw_status, accountProbe.raw_status);
  const accountRawReason = firstText(phoneStatus.account_raw_reason, accountProbe.raw_reason, phoneStatus.account_error, accountProbe.error_message);
  const accountError = firstText(phoneStatus.account_error, accountProbe.error_message);
  const rejectReason = firstText(phoneStatus.reject_reason, result?.error_message, result?.status);
  const explicitRequestFailed = firstBool(phoneStatus.request_failed, result?.request_failed);
  const requestFailed = explicitRequestFailed ?? (accountFlow !== 'registered' && (accountRejected(accountStatus, accountRawReason, accountError) || requestFailure(rejectReason, result?.error_message, result?.status)));
  const methodStatuses = verificationMethodStatuses(phoneStatus.method_statuses, accountProbe.method_statuses, accountProbe.supported_methods);
  return {
    requestFailed, failureReason: reasonLabel(rejectReason || accountRawReason || accountError),
    registered, blocked, accountReachable, smsAvailable, smsWaitSeconds, smsWaitUntil, canRegister, accountFlow,
    accountStatus, accountRawStatus, accountRawReason, accountError,
    smsStatus: firstText(phoneStatus.sms_status, smsProbe.sms_status, smsProbe.status),
    methodStatuses,
    proxyText: compactJoin([firstText(proxy.proxy_mode), firstText(proxy.country_code)], ' · '),
    rejectReason
  };
}
export function outcomeMeta(status: WaProbeStatus, result?: WaWorkflowResponse | null, loading?: boolean): { label: string; variant: BadgeVariant } {
  if (loading) return { label: '执行中', variant: 'secondary' };
  if (!result) return { label: '等待', variant: 'outline' };
  if (status.requestFailed) return { label: '探测失败', variant: 'destructive' };
  if (status.blocked === true) return { label: '已封禁', variant: 'destructive' };
  if (status.accountFlow === 'invalid_number') return { label: '号码异常', variant: 'secondary' };
  if (status.accountFlow === 'rate_limited') return { label: '限流', variant: 'secondary' };
  if (status.registered === true || status.accountFlow === 'registered') return { label: '已注册', variant: 'secondary' };
  if (status.canRegister === true || (status.accountFlow === 'not_registered' && status.smsAvailable === true)) return { label: 'SMS 可发', variant: 'default' };
  if (status.smsAvailable === false) return { label: 'SMS 不可发', variant: 'secondary' };
  if (status.accountFlow === 'not_registered') return { label: '未检测到已注册', variant: 'secondary' };
  return { label: '完成', variant: 'secondary' };
}
export function metaItems(status: WaProbeStatus, result?: WaWorkflowResponse | null): MetaItem[] {
  const entries: MetaItem[] = [];
  if (status.requestFailed) {
    addItem(entries, '原因', status.failureReason || reasonLabel(result?.error_message || '') || '本次请求失败', 'bad');
    addItem(entries, '代理', status.proxyText);
    return entries;
  }
  const account = accountFeedback(status);
  addItem(entries, '账号反馈', account, account ? 'warn' : 'idle');
  addItem(entries, 'SMS补充', smsExtra(status));
  addItem(entries, '代理', status.proxyText);
  return entries;
}
function accountFeedback(status: WaProbeStatus) {
  if (['registered', 'not_registered', 'blocked', 'invalid_number', 'rate_limited'].includes(status.accountFlow)) return '';
  const raw = compactJoin([status.accountStatus, status.accountRawStatus, status.accountRawReason, status.accountError], ' / ');
  const normalized = raw.toLowerCase();
  if (!raw) return '';
  if (normalized.includes('account_probe_status_rejected') || normalized.includes('invalid_skey') || normalized.includes('bad_token') || normalized.includes('incorrect')) return '请求未确认';
  if (normalized.includes('incorrect')) return '';
  return extraValues(status.accountStatus, status.accountRawStatus, status.accountRawReason, status.accountError).join(' / ');
}
function accountRejected(...values: string[]) {
  const normalized = compactJoin(values, ' ').toLowerCase();
  return normalized.includes('account_probe_status_rejected') || normalized.includes('incorrect') || normalized.includes('invalid_skey') || normalized.includes('bad_token') || normalized.includes('missing_param') || normalized.includes('bad_param') || normalized.includes('old_version');
}
function requestFailure(...values: unknown[]) {
  const normalized = values.map(firstText).join(' ').toLowerCase();
  if (!normalized || normalized.includes('already registered') || normalized.includes('number is blocked') || normalized.includes('cooling down') || normalized.includes('sms route unavailable')) return false;
  return normalized.startsWith('account probe rejected') || normalized.startsWith('account probe request') || normalized.includes('network') || normalized.includes('unreachable') || normalized.includes('dynamic ip') || normalized.includes('proxy') || normalized.includes(' eof') || normalized.includes('invalid_skey') || normalized.includes('bad_token') || normalized.includes('missing_param') || normalized.includes('bad_param');
}
function smsExtra(status: WaProbeStatus) {
  if (status.smsWaitUntil) return `冷却到 ${status.smsWaitUntil}`;
  return extraValues(status.smsStatus).join(' / ');
}
function verificationMethodStatuses(...values: unknown[]) {
  const seen = new Map<string, VerificationMethodStatus>();
  for (const value of values) {
    if (Array.isArray(value)) {
      for (const item of value) addMethodStatus(seen, item);
      continue;
    }
    addMethodStatus(seen, value);
  }
  return [...seen.values()];
}
function addMethodStatus(seen: Map<string, VerificationMethodStatus>, value: unknown) {
  if (typeof value === 'string') {
    for (const label of methodLabels(value)) upsertMethodStatus(seen, label, undefined, null);
    return;
  }
  const item = record(value);
  if (!Object.keys(item).length) return;
  const label = methodLabel(firstText(item.method, item.delivery_method, item.name, item.type));
  if (!label) return;
  upsertMethodStatus(seen, label, firstBool(item.available, item.eligible, item.enabled), firstNumber(item.cooldown_seconds, item.wait_seconds, item.retry_after_seconds));
}
function upsertMethodStatus(seen: Map<string, VerificationMethodStatus>, label: string, available?: boolean, cooldownSeconds: number | null = null) {
  const key = label.toLowerCase();
  const previous = seen.get(key);
  seen.set(key, {
    key,
    label,
    available: previous?.available ?? available,
    cooldownSeconds: firstNumber(cooldownSeconds, previous?.cooldownSeconds)
  });
}
function reasonLabel(value: string) {
  const normalized = value.trim().toLowerCase();
  if (!normalized) return '';
  if (normalized.includes('incorrect')) return '探测请求未被 WA 确认';
  if (normalized.startsWith('account probe rejected') || normalized.startsWith('account probe request rejected')) return '账号请求被拒';
  if (normalized.startsWith('account probe request failed')) return '账号请求失败';
  if (normalized.includes('already registered')) return '号码已注册';
  if (normalized.includes('number is blocked')) return '号码被封禁';
  if (normalized.includes('sms route is cooling down')) return 'SMS 冷却中';
  if (normalized.startsWith('sms route unavailable')) return 'SMS 不可用';
  if (normalized.includes('dynamic ip lease unavailable')) return '动态 IP 不可用';
  if (normalized.includes('length_short') || normalized.includes('length_long') || normalized.includes('format_wrong')) return '号码格式异常';
  if (normalized.includes('too_recent') || normalized.includes('too_many') || normalized.includes('temporarily_unavailable')) return '限流';
  return value.trim();
}
function addItem(entries: MetaItem[], label: string, value?: string, tone: ResultTone = 'idle') {
  const text = value?.trim();
  if (text) entries.push({ label, value: text, tone });
}
function registeredSignal(...values: unknown[]) {
  return statusIn(['registered', 'exists', 'account_exists'], ...values) ? true : undefined;
}

function deriveAccountFlow(input: { registered?: boolean; blocked?: boolean; smsAvailable?: boolean; accountStatus: string; rawReason: string }) {
  const raw = compactJoin([input.accountStatus, input.rawReason], ' ').toLowerCase();
  if (input.registered === true || raw.includes('exists') || raw.includes('registered')) return 'registered';
  if (input.blocked === true || raw.includes('blocked')) return 'blocked';
  if (raw.includes('length_short') || raw.includes('length_long') || raw.includes('format_wrong')) return 'invalid_number';
  if (raw.includes('too_recent') || raw.includes('too_many') || raw.includes('temporarily_unavailable')) return 'rate_limited';
  if (raw.includes('incorrect')) return 'probe_failed';
  return 'unknown';
}
