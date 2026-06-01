import { useState } from 'react';
import { RefreshCw, Smartphone, Workflow } from 'lucide-react';
import {
  ACCOUNT_PAGE_SIZE,
  AccountManagementDrawerView,
  ToastMessage,
  WorkflowStatusPanel,
  WorkspaceTabbedPanel,
  accountId,
  accountSubject,
  deleteAccountCarrier,
  useAccountPages,
  useAsyncActionRunner,
  useQuery,
  useToastMessage,
  type AccountListPagination,
  type AccountRecord,
} from '@byte-v-forge/common-ui';
import type { ListWAAccountsResponse } from '../proto/byte/v/forge/waapp/v1/profile';
import { deleteWaAccount, getWaAccounts, getWaHealth, probeWaAccount, probeWaPhoneSMS, registerWaAccount, waKeys, type WaAccountProjection, type WaWorkflowResponse } from './wa-api';
import { WaAccountAdd } from './wa-account-add';
import { waAccountDetailTabs, type WaAccountActionResult } from './wa-account-detail';
import { WaPhoneSMSProbeForm } from './wa-phone-sms-probe-form';
import { WaResultPanel } from './wa-result-panel';
import type { WaResolvedPhone } from './wa-utils';

type WaTab = 'accounts' | 'toolbox' | 'workflows';

const ACCOUNT_WORKSPACE_ID = 'default';

export function WaPage() {
  const toast = useToastMessage();
  const health = useQuery({ queryKey: waKeys.health, queryFn: getWaHealth });
  const [checkedPhone, setCheckedPhone] = useState('');
  const [result, setResult] = useState<WaWorkflowResponse | null>(null);
  const runner = useAsyncActionRunner();
  const accounts = useAccountPages<WaAccountProjection, ListWAAccountsResponse>({
    queryKey: waKeys.accounts(ACCOUNT_WORKSPACE_ID),
    queryFn: (cursor) => getWaAccounts(ACCOUNT_WORKSPACE_ID, cursor),
    refetchInterval: 10000,
    pageSize: ACCOUNT_PAGE_SIZE
  });

  async function probePhoneSMS(target: WaResolvedPhone) {
    setCheckedPhone(target.e164);
    setResult(null);
    await runner.tryRun('wa-phone-sms-probe', async () => {
      const output = await probeWaPhoneSMS(target.input);
      setResult(output);
      toast.showOK('手机号/SMS 探测完成');
    }, { onError: toast.showError });
  }

  return <><ToastMessage toast={toast.toast} /><WorkspaceTabbedPanel<WaTab> defaultValue="accounts" title={<span className="inline-flex items-center gap-2"><Smartphone className="size-4" />WA 管理</span>} meta={`${accounts.accounts.length} 个账号 · ${health.data?.n8n_webhook_configured ? 'n8n 已接入' : '等待 n8n'}`} tabs={[
    { value: 'accounts', label: '账号', content: <WaAccountsTab accounts={accounts.accounts} loading={accounts.isLoading} pagination={accounts.pagination} onAccountsChanged={async () => { await accounts.refetch(); }} onAccountAdded={async () => { toast.showOK('WAAccount 已添加'); await accounts.refetch(); }} onActionDone={toast.showOK} onError={toast.showError} /> },
    { value: 'toolbox', label: '工具箱', content: <ToolboxTab result={result} phone={checkedPhone} busy={runner.busy} onCheck={probePhoneSMS} onError={toast.showError} /> },
    { value: 'workflows', label: '工作流', content: <WorkflowTab configured={Boolean(health.data?.n8n_webhook_configured)} workflows={health.data?.workflows || []} loading={health.isLoading} /> }
  ]} /></>;
}

function ToolboxTab(props: { result: WaWorkflowResponse | null; phone: string; busy: boolean; onCheck: (target: WaResolvedPhone) => void | Promise<void>; onError: (message: unknown) => void }) {
  const hasResult = props.busy || props.result || props.phone;
  return <div className="p-3"><WaPhoneSMSProbeForm disabled={props.busy} resultSlot={hasResult ? <WaResultPanel title="探测结果" phone={props.phone} result={props.result} loading={props.busy} /> : undefined} onCheck={props.onCheck} onError={props.onError} /></div>;
}

function WaAccountsTab(props: { accounts: WaAccountProjection[]; loading?: boolean; pagination?: AccountListPagination; onAccountsChanged: () => void | Promise<void>; onAccountAdded: () => void | Promise<void>; onActionDone: (message: string) => void; onError: (message: unknown) => void }) {
  const [selected, setSelected] = useState<WaAccountProjection | null>(null);
  const [actionResult, setActionResult] = useState<WaAccountActionResult | null>(null);
  const runner = useAsyncActionRunner();
  const selectedAccount = selected?.account || null;
  const renderConfig = { icon: () => <Smartphone size={15} />, title: (record: AccountRecord) => <span className="font-mono">{accountSubject(record) || record.key?.account_id}</span>, subtitle: (record: AccountRecord) => record.key?.account_id || '', meta: (record: AccountRecord) => <span className="text-xs text-muted-foreground">{record.status?.label || record.status?.value || '-'}</span> };
  async function deleteAccount(account: WaAccountProjection) {
    const accountID = account.account?.key?.account_id || '';
    await runner.tryRun(`wa-delete:${accountID}`, async () => {
      const deleted = await deleteAccountCarrier(account, {
        deleteByID: () => deleteWaAccount(account, ACCOUNT_WORKSPACE_ID),
        confirmMessage: () => `删除 WAAccount ${accountID}？`,
        invalidate: async () => {
          setSelected(null);
          await props.onAccountsChanged();
        },
      });
      if (deleted) props.onActionDone('WAAccount 已删除');
    }, { onError: props.onError });
  }
  return <AccountManagementDrawerView title="WAAccount" icon={<Smartphone size={16} />} actions={<WaAccountAdd disabled={props.loading} onCreated={props.onAccountAdded} onError={props.onError} />} carriers={props.accounts} selectedCarrier={selected} selectedID={selectedAccount ? accountId(selectedAccount) : undefined} onSelectCarrier={setSelected} loading={props.loading} loadingText="加载 WAAccount..." emptyText="暂无已持久化 WAAccount" pagination={props.pagination} config={renderConfig} drawerDescription="WA 账号详情" detailTabs={waAccountDetailTabs({ actionResult, busy: runner.busy, onRegister: (account) => runWAAccountAction('register', account, runner, setActionResult, props), onProbe: (account) => runWAAccountAction('probe', account, runner, setActionResult, props), onDelete: deleteAccount, onManualOTPDone: props.onActionDone, onError: props.onError })} onCloseDetails={() => setSelected(null)} />;
}

type WaAccountActionKind = WaAccountActionResult['kind'];
type WaAccountRunner = ReturnType<typeof useAsyncActionRunner>;
type WaAccountActionCallbacks = {
  onAccountsChanged: () => void | Promise<void>;
  onActionDone: (message: string) => void;
  onError: (message: unknown) => void;
};

async function runWAAccountAction(kind: WaAccountActionKind, account: WaAccountProjection, runner: WaAccountRunner, setActionResult: (value: WaAccountActionResult | null) => void, callbacks: WaAccountActionCallbacks) {
  const accountID = account.account?.key?.account_id || '';
  const phone = account.phone?.e164_number || '';
  await runner.tryRun(`wa-${kind}:${accountID}`, async () => {
    const result = kind === 'register' ? await registerWaAccount(account) : await probeWaAccount(account);
    setActionResult({ accountID, kind, phone, result });
    const error = waAccountActionError(kind, result);
    if (error) throw new Error(error);
    callbacks.onActionDone(kind === 'register' ? 'WA 注册流程已触发' : '手机号/SMS 探测完成');
    if (kind === 'register') await callbacks.onAccountsChanged();
  }, { onError: callbacks.onError });
}

function waAccountActionError(kind: WaAccountActionKind, result: WaWorkflowResponse) {
  const errorText = textOf(result.error_message) || textOf((result as Record<string, unknown>).error);
  if (result.request_failed) return errorText || 'WA 请求失败';
  if (kind === 'register' && result.success === false) return errorText || textOf(result.status) || 'WA 注册流程失败';
  return '';
}

function textOf(value: unknown) {
  return typeof value === 'string' ? value.trim() : '';
}

function WorkflowTab({ configured, workflows, loading }: { configured: boolean; loading?: boolean; workflows: Array<{ key: string; label: string; webhook_path: string }> }) {
  return (
    <WorkflowStatusPanel
      configured={configured}
      loading={loading}
      configuredTitle="WA n8n 编排已接入"
      unconfiguredTitle="WA n8n webhook 未配置"
      description="注册流程走 workflow；工具箱号码/SMS 探测、登录态检测、长连接恢复和 OTP MQ 投放由 wa-app 直连服务完成。"
      cards={[
        {
          id: 'register',
          icon: <Workflow size={16} />,
          title: '注册流程',
          badge: 'n8n',
          text: '跨步骤注册和等待 OTP 仍由 n8n 编排。',
        },
        {
          id: 'direct',
          icon: <RefreshCw size={16} />,
          title: '探测 / 登录态 / 长连接',
          badge: '直连',
          text: '号码/SMS 探测使用 1 分钟动态 IP 短租约，用完释放；登录态和长连接不进入 workflow。',
        },
      ]}
      workflows={workflows}
    />
  );
}
