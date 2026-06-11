import { AccountSettingsOperationStatus } from '../proto/byte/v/forge/waapp/v1/account_settings';
import type { GetTwoFactorAuthStatusResponse, TwoFactorAuthStatus } from '../proto/byte/v/forge/waapp/v1/account_settings';
import type { BadgeVariant } from './ui';

export type TwoFactorStatusView = { isFetching: boolean; isError: boolean; data?: { status?: { configured?: boolean; email_configured?: boolean } } };

export function initialTwoFactorStatus(status?: TwoFactorAuthStatus): GetTwoFactorAuthStatusResponse {
  return status ? { status, error: undefined } : { status: undefined, error: undefined };
}

export function shouldCollectEmailOtpAfterSet(status?: AccountSettingsOperationStatus) {
  return status !== AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_VERIFIED
    && status !== AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_REJECTED;
}

export function shouldShowEmailOtp(status?: AccountSettingsOperationStatus) {
  return status === AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_NEEDS_VERIFICATION
    || status === AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_WAITING
    || status === AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_CODE_MISMATCH;
}

export function twoFactorStatusLabel(query: TwoFactorStatusView) {
  if (query.isFetching) return '同步中';
  if (query.isError) return '同步失败';
  if (!query.data?.status) return '未同步';
  return query.data.status.configured ? '已配置' : '未配置';
}

export function emailStatusLabel(query: TwoFactorStatusView) {
  if (query.isFetching) return '同步中';
  if (query.isError) return '同步失败';
  if (!query.data?.status) return '未同步';
  return query.data.status.email_configured ? '已配置' : '未配置';
}

export function twoFactorBadgeVariant(query: TwoFactorStatusView): BadgeVariant {
  if (query.isError) return 'destructive';
  return query.data?.status?.configured ? 'default' : 'outline';
}

export function emailBadgeVariant(query: TwoFactorStatusView): BadgeVariant {
  if (query.isError) return 'destructive';
  return query.data?.status?.email_configured ? 'default' : 'outline';
}

export function twoFactorConfigured(query: TwoFactorStatusView) {
  return Boolean(query.data?.status?.configured);
}

export function twoFactorEmailConfigured(query: TwoFactorStatusView) {
  return Boolean(query.data?.status?.email_configured);
}

export function statusLabel(status?: AccountSettingsOperationStatus) {
  switch (status) {
    case AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_NEEDS_VERIFICATION: return '待邮箱验证';
    case AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_WAITING: return '等待 OTP';
    case AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_VERIFIED: return '已验证';
    case AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_CODE_MISMATCH: return '验证码不匹配';
    case AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_REJECTED: return '已拒绝';
    case AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_ACCEPTED: return '已受理';
    default: return '未执行';
  }
}
