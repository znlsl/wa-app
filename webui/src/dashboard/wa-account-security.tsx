import { type FormEvent, type ReactNode, useEffect, useMemo, useState } from 'react';
import { CheckCircle2, KeyRound, Mail, Pencil, RefreshCw, Send, ShieldCheck, X } from 'lucide-react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AccountSettingsOperationStatus } from '../proto/byte/v/forge/waapp/v1/account_settings';
import type { GetTwoFactorAuthStatusResponse } from '../proto/byte/v/forge/waapp/v1/account_settings';
import type { WaAccountProjection } from './wa-api';
import { getWaTwoFactorAuthStatus, requestWaAccountEmailOtp, setWaAccountEmail, setWaTwoFactorAuthSettings, verifyWaAccountEmailOtp, waAccountID, waKeys } from './wa-api';
import {
  emailBadgeVariant,
  emailStatusLabel,
  initialTwoFactorStatus,
  shouldCollectEmailOtpAfterSet,
  shouldShowEmailOtp,
  statusLabel,
  twoFactorBadgeVariant,
  twoFactorConfigured,
  twoFactorEmailConfigured,
  twoFactorStatusLabel,
} from './wa-account-security-model';
import { Badge, Button, Field, FieldGroup, FieldLabel, Input } from './ui';

type Props = { account: WaAccountProjection; onDone: (message: string) => void; onError: (message: string) => void };

export function WaAccountSecurityPanel({ account, onDone, onError }: Props) {
  const queryClient = useQueryClient();
  const [pin, setPin] = useState('');
  const [email, setEmail] = useState('');
  const [emailOtp, setEmailOtp] = useState('');
  const [emailOtpVisible, setEmailOtpVisible] = useState(false);
  const [pinEditing, setPinEditing] = useState(false);
  const [emailEditing, setEmailEditing] = useState(false);
  const [lastStatus, setLastStatus] = useState<AccountSettingsOperationStatus | undefined>();
  const handleError = (error: unknown) => onError(error instanceof Error ? error.message : String(error));
  const handleSuccess = (message: string, status?: AccountSettingsOperationStatus) => { setLastStatus(status); onDone(message); };
  const accountID = waAccountID(account);
  const statusKey = useMemo(() => waKeys.twoFactorStatus(accountID), [accountID]);
  const patchStatus = (patch: Partial<NonNullable<GetTwoFactorAuthStatusResponse['status']>>) =>
    queryClient.setQueryData<GetTwoFactorAuthStatusResponse>(statusKey, (previous) => ({
      error: previous?.error,
      status: {
        configured: previous?.status?.configured || false,
        email_configured: previous?.status?.email_configured || false,
        email_verified: previous?.status?.email_verified || false,
        email_confirmed: previous?.status?.email_confirmed || false,
        email_address: previous?.status?.email_address || '',
        ...patch,
      },
    }));
  const twoFactorStatus = useQuery({
    queryKey: statusKey,
    queryFn: () => getWaTwoFactorAuthStatus(account, { remoteRefresh: true }),
    enabled: false,
    gcTime: 30 * 60_000,
    initialData: () => initialTwoFactorStatus(account.two_factor_auth),
    staleTime: Infinity,
  });
  const pinConfigured = twoFactorConfigured(twoFactorStatus);
  const emailConfigured = twoFactorEmailConfigured(twoFactorStatus);
  const pinAction = pinConfigured ? '修改 2FA PIN' : '设置 2FA PIN';
  const emailAction = emailConfigured ? '修改账户邮箱' : '设置账户邮箱';
  const twoFactor = useMutation({
    mutationFn: () => setWaTwoFactorAuthSettings(account, pin),
    onSuccess: (resp) => {
      setPin('');
      setPinEditing(false);
      if (resp.operation?.status !== AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_REJECTED) patchStatus({ configured: true });
      handleSuccess(pinConfigured ? '2FA PIN 修改请求已提交' : '2FA PIN 设置请求已提交', resp.operation?.status);
    },
    onError: handleError,
  });
  const emailSet = useMutation({
    mutationFn: () => setWaAccountEmail(account, { email_address: email }),
    onSuccess: (resp) => {
      const setStatus = resp.operation?.status;
      setEmailOtpVisible(shouldCollectEmailOtpAfterSet(setStatus));
      setEmailEditing(false);
      if (setStatus !== AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_REJECTED) {
        patchStatus({
          email_address: email,
          email_configured: true,
          email_verified: setStatus === AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_VERIFIED,
          email_confirmed: setStatus === AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_VERIFIED,
        });
      }
      if (setStatus === AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_VERIFIED) {
        setEmailOtp('');
      }
      handleSuccess(emailConfigured ? '账户邮箱修改请求已提交' : '账户邮箱设置请求已提交', setStatus);
    },
    onError: handleError,
  });
  const otpRequest = useMutation({
    mutationFn: () => requestWaAccountEmailOtp(account),
    onSuccess: (resp) => {
      setEmailOtpVisible(true);
      handleSuccess('邮箱 OTP 已请求', resp.operation?.status);
    },
    onError: handleError,
  });
  const otpVerify = useMutation({
    mutationFn: () => verifyWaAccountEmailOtp(account, emailOtp),
    onSuccess: (resp) => {
      const status = resp.operation?.status;
      setEmailOtp('');
      setEmailOtpVisible(shouldShowEmailOtp(status));
      if (status === AccountSettingsOperationStatus.ACCOUNT_SETTINGS_OPERATION_STATUS_VERIFIED) patchStatus({ email_configured: true, email_verified: true });
      handleSuccess('邮箱 OTP 校验请求已提交', status);
    },
    onError: handleError,
  });
  const busy = twoFactor.isPending || emailSet.isPending || otpRequest.isPending || otpVerify.isPending;
  const handleEmailChange = (value: string) => { setEmail(value); setEmailOtp(''); setEmailOtpVisible(false); };
  const pinFormVisible = !pinConfigured || pinEditing;
  const emailFormVisible = (!emailConfigured && !emailOtpVisible) || emailEditing;
  useEffect(() => {
    if (account.two_factor_auth) queryClient.setQueryData(statusKey, initialTwoFactorStatus(account.two_factor_auth));
  }, [account.two_factor_auth, queryClient, statusKey]);
  useEffect(() => {
    setPin('');
    setEmail('');
    setEmailOtp('');
    setEmailOtpVisible(false);
    setPinEditing(false);
    setEmailEditing(false);
  }, [accountID]);
  return (
    <section className="grid gap-4">
      <div className="flex items-center justify-end gap-2">
        {lastStatus !== undefined ? <Badge variant="outline">{statusLabel(lastStatus)}</Badge> : null}
        <Button size="icon" variant="ghost" type="button" disabled={busy || twoFactorStatus.isFetching} title="同步状态" aria-label="同步状态" onClick={() => { void twoFactorStatus.refetch(); }}><RefreshCw size={16} className={twoFactorStatus.isFetching ? 'animate-spin' : ''} /></Button>
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <section className="grid gap-3">
          <SettingHeader icon={<ShieldCheck size={15} />} title={pinAction} badge={<Badge variant={twoFactorBadgeVariant(twoFactorStatus)}>{twoFactorStatusLabel(twoFactorStatus)}</Badge>} canEdit={pinConfigured && !pinEditing && !busy} onEdit={() => setPinEditing(true)} />
          {pinFormVisible ? <PinForm pin={pin} busy={busy} configured={pinConfigured} onPinChange={setPin} onCancel={() => { setPin(''); setPinEditing(false); }} onSubmit={(event) => submit(event, twoFactor.mutate)} /> : null}
        </section>
        <section className="grid gap-3">
          <SettingHeader icon={<Mail size={15} />} title={emailAction} badge={<Badge variant={emailBadgeVariant(twoFactorStatus)}>{emailStatusLabel(twoFactorStatus)}</Badge>} canEdit={emailConfigured && !emailEditing && !busy} onEdit={() => setEmailEditing(true)} />
          {emailConfigured && !emailEditing ? <EmailProjection status={twoFactorStatus.data?.status} /> : null}
          {emailFormVisible ? <EmailForm email={email} busy={busy} configured={emailConfigured} onEmailChange={handleEmailChange} onCancel={() => { setEmail(''); setEmailEditing(false); }} onSubmit={(event) => submit(event, emailSet.mutate)} /> : null}
        </section>
        {emailOtpVisible && (
          <div className="grid gap-3 border-t border-border pt-5 lg:col-span-2">
            <div className="flex items-center gap-2 text-sm font-medium"><Send size={15} />邮箱 OTP</div>
            <div className="grid gap-3 sm:grid-cols-[auto_1fr_auto]">
              <Button type="button" variant="outline" disabled={busy} onClick={() => otpRequest.mutate()}><Send size={14} />请求 OTP</Button>
              <Input value={emailOtp} onChange={(event) => setEmailOtp(event.target.value)} inputMode="numeric" autoComplete="one-time-code" type="password" maxLength={6} disabled={busy} placeholder="6 位验证码" />
              <Button type="button" disabled={busy || emailOtp.length !== 6} onClick={() => otpVerify.mutate()}><CheckCircle2 size={14} />校验 OTP</Button>
            </div>
          </div>
        )}
      </div>
    </section>
  );
}

function SettingHeader({ icon, title, badge, canEdit, onEdit }: { icon: ReactNode; title: string; badge: ReactNode; canEdit: boolean; onEdit: () => void }) {
  return <div className="flex items-center justify-between gap-2"><div className="inline-flex items-center gap-2 text-sm font-medium">{icon}{title}{badge}</div>{canEdit ? <Button size="icon" variant="ghost" type="button" title={title} aria-label={title} onClick={onEdit}><Pencil size={16} /></Button> : null}</div>;
}

function EmailProjection({ status }: { status?: NonNullable<GetTwoFactorAuthStatusResponse['status']> }) {
  return status?.email_address ? <div className="truncate text-sm text-muted-foreground">{status.email_address}</div> : null;
}

function PinForm({ pin, busy, configured, onPinChange, onCancel, onSubmit }: { pin: string; busy: boolean; configured: boolean; onPinChange: (value: string) => void; onCancel: () => void; onSubmit: (event: FormEvent<HTMLFormElement>) => void }) {
  return <form className="grid gap-3" onSubmit={onSubmit}><FieldGroup><Field><FieldLabel>{configured ? '新 6 位 PIN' : '6 位 PIN'}</FieldLabel><Input value={pin} onChange={(event) => onPinChange(event.target.value)} inputMode="numeric" autoComplete="one-time-code" type="password" maxLength={6} disabled={busy} /></Field>{configured ? <Button type="button" variant="ghost" size="icon" disabled={busy} title="取消修改" aria-label="取消修改" onClick={onCancel}><X size={16} /></Button> : null}<Button type="submit" disabled={busy || pin.length !== 6}><KeyRound size={14} />{configured ? '修改 PIN' : '设置 PIN'}</Button></FieldGroup></form>;
}

function EmailForm({ email, busy, configured, onEmailChange, onCancel, onSubmit }: { email: string; busy: boolean; configured: boolean; onEmailChange: (value: string) => void; onCancel: () => void; onSubmit: (event: FormEvent<HTMLFormElement>) => void }) {
  return <form className="grid gap-3" onSubmit={onSubmit}><FieldGroup><Field><FieldLabel>{configured ? '新邮箱地址' : '邮箱地址'}</FieldLabel><Input value={email} onChange={(event) => onEmailChange(event.target.value)} type="email" disabled={busy} placeholder={configured ? '新邮箱地址' : '邮箱地址'} /></Field>{configured ? <Button type="button" variant="ghost" size="icon" disabled={busy} title="取消修改" aria-label="取消修改" onClick={onCancel}><X size={16} /></Button> : null}<Button type="submit" disabled={busy || !email}><Mail size={14} />{configured ? '修改邮箱' : '设置邮箱'}</Button></FieldGroup></form>;
}

function submit(event: FormEvent<HTMLFormElement>, run: () => void) { event.preventDefault(); run(); }
