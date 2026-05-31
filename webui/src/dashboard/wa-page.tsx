import { useMemo, useState, type ReactNode } from 'react';
import { Activity, ListChecks, Radio, RefreshCw, Smartphone, Workflow } from 'lucide-react';
import { ACCOUNT_PAGE_SIZE, AccountCarrierPanel, Alert, AlertDescription, AlertTitle, Badge, Button, Card, CardContent, ToastMessage, ToolbarActionButtons, WorkspaceTabbedPanel, accountSubject, actionTargetStateKey, activeActionTarget, useAccountPages, useAsyncActionRunner, useQuery, useToastMessage, type AccountListPagination } from '@byte-v-forge/common-ui';
import type { ListWAAccountsResponse } from '../proto/byte/v/forge/waapp/v1/profile';
import { getWaAccounts, getWaConnections, getWaHealth, probeWaNumber, registerWaNumber, waKeys, type WaAccountProjection, type WaConnectionState } from './wa-api';
import { WaNumberImport } from './wa-number-import';
import { WaNumberTable } from './wa-number-table';
import { WaResultPanel } from './wa-result-panel';
import { connectionHealthy, mergeNumbers, withResult, type WaManagedNumber } from './wa-utils';

export function WaPage() {
  const toast = useToastMessage();
  const health = useQuery({ queryKey: waKeys.health, queryFn: getWaHealth });
  const [rows, setRows] = useState<WaManagedNumber[]>([]);
  const [selectedId, setSelectedId] = useState('');
  const runner = useAsyncActionRunner();
  const busyId = activeActionTarget(runner.activeKey, 'wa-number');
  const selected = useMemo(() => rows.find((row) => row.id === selectedId) || rows[0] || null, [rows, selectedId]);
  const workspaceId = selected?.input.workspace_id || 'default';
  const accounts = useAccountPages<WaAccountProjection, ListWAAccountsResponse>({
    queryKey: waKeys.accounts(workspaceId),
    queryFn: (cursor) => getWaAccounts(workspaceId, cursor),
    enabled: Boolean(workspaceId),
    refetchInterval: 10000,
    pageSize: ACCOUNT_PAGE_SIZE
  });
  const connections = useQuery({ queryKey: waKeys.connections(workspaceId), queryFn: () => getWaConnections(workspaceId), enabled: Boolean(workspaceId), refetchInterval: 5000 });
  const connectionForRow = (row: WaManagedNumber) => findConnection(connections.data?.connections || [], row);
  const selectedConnection = selected ? connectionForRow(selected) : null;

  async function run(row: WaManagedNumber, action: 'probe' | 'register') {
    setRows((items) => items.map((item) => item.id === row.id ? { ...item, status: action === 'probe' ? 'probing' : 'registering' } : item));
    await runner.tryRun(actionTargetStateKey('wa-number', row.id), async () => {
      const result = action === 'probe' ? await probeWaNumber(row.input) : await registerWaNumber(row.input);
      setRows((items) => items.map((item) => item.id === row.id ? withResult(item, result, action) : item));
      setSelectedId(row.id);
      void accounts.refetch();
      toast.showOK(action === 'probe' ? '检测完成' : '注册流程完成');
    }, { onError: (error) => {
      setRows((items) => items.map((item) => item.id === row.id ? { ...item, status: 'failed' } : item));
      toast.showError(error);
    } });
  }

  return <><ToastMessage toast={toast.toast} /><WorkspaceTabbedPanel defaultValue="numbers" title={<span className="inline-flex items-center gap-2"><Smartphone className="size-4" />WA 管理</span>} meta={`${rows.length} 个号码 · ${health.data?.n8n_webhook_configured ? 'n8n 已接入' : '等待 n8n'}`} tabs={[
    { value: 'numbers', label: '号码池', content: <NumbersTab rows={rows} accounts={accounts.accounts} accountsLoading={accounts.isLoading} accountsPagination={accounts.pagination} connections={connections.data?.connections || []} connectionForRow={connectionForRow} selected={selected} connection={selectedConnection} busyId={busyId} onAdd={(items) => setRows((current) => mergeNumbers(current, items))} onSelect={(row) => setSelectedId(row.id)} onProbe={(row) => run(row, 'probe')} onRegister={(row) => run(row, 'register')} onRemove={(row) => setRows((items) => items.filter((item) => item.id !== row.id))} onProbeAll={() => rows.reduce((p, row) => p.then(() => run(row, 'probe')), Promise.resolve())} /> },
    { value: 'workflows', label: '工作流', content: <WorkflowTab configured={Boolean(health.data?.n8n_webhook_configured)} workflows={health.data?.workflows || []} loading={health.isLoading} /> }
  ]} /></>;
}

function NumbersTab(props: { rows: WaManagedNumber[]; accounts: WaAccountProjection[]; accountsLoading?: boolean; accountsPagination?: AccountListPagination; connections: WaConnectionState[]; selected: WaManagedNumber | null; connection?: WaConnectionState | null; connectionForRow: (row: WaManagedNumber) => WaConnectionState | null; busyId: string; onAdd: (rows: WaManagedNumber[]) => void; onSelect: (row: WaManagedNumber) => void; onProbe: (row: WaManagedNumber) => void; onRegister: (row: WaManagedNumber) => void; onRemove: (row: WaManagedNumber) => void; onProbeAll: () => void }) {
  const registered = props.rows.filter((row) => row.status === 'registered').length;
  const connected = props.connections.filter(connectionHealthy).length;
  return <div className="grid gap-4 p-4 xl:grid-cols-[360px_minmax(0,1fr)_440px]"><WaNumberImport onAdd={props.onAdd} /><div className="grid content-start gap-3"><StatusCards total={props.rows.length} accounts={props.accounts.length} registered={registered} connected={connected} /><AccountCarrierPanel title="WAAccount" carriers={props.accounts} loading={props.accountsLoading} loadingText="加载 WAAccount..." emptyText="暂无已持久化 WAAccount" pagination={props.accountsPagination} config={{ icon: () => <Smartphone size={15} />, title: (record) => <span className="font-mono">{accountSubject(record) || record.key?.account_id}</span>, meta: (record) => <span className="text-xs text-muted-foreground">{record.key?.account_id}</span> }} /><div className="flex items-center justify-between gap-3"><div className="text-sm font-medium">号码池</div><ToolbarActionButtons actions={[{ label: '检测全部', icon: <ListChecks size={15} />, disabled: Boolean(props.busyId) || props.rows.length === 0, onClick: props.onProbeAll }]} /></div><WaNumberTable rows={props.rows} selectedId={props.selected?.id} busyId={props.busyId} connectionForRow={props.connectionForRow} onSelect={props.onSelect} onProbe={props.onProbe} onRegister={props.onRegister} onRemove={props.onRemove} /></div><WaResultPanel title="号码详情" result={props.selected?.result} connection={props.connection} loading={Boolean(props.busyId && props.selected?.id === props.busyId)} /></div>;
}

function StatusCards({ total, accounts, registered, connected }: { total: number; accounts: number; registered: number; connected: number }) {
  return <div className="grid gap-3 md:grid-cols-4"><Summary icon={<Smartphone size={16} />} label="号码" value={total} /><Summary icon={<Smartphone size={16} />} label="账号" value={accounts} /><Summary icon={<Activity size={16} />} label="已注册" value={registered} /><Summary icon={<Radio size={16} />} label="长连接" value={connected} /></div>;
}

function Summary({ icon, label, value }: { icon: ReactNode; label: string; value: number }) {
  return <Card><CardContent className="flex items-center gap-3 p-3"><div className="rounded-lg bg-primary/10 p-2 text-primary">{icon}</div><div><div className="text-xs text-muted-foreground">{label}</div><div className="text-lg font-semibold leading-none">{value}</div></div></CardContent></Card>;
}

function WorkflowTab({ configured, workflows, loading }: { configured: boolean; loading?: boolean; workflows: Array<{ key: string; label: string; webhook_path: string }> }) {
  return <div className="grid gap-4 p-4"><Alert><AlertTitle>{configured ? 'WA n8n 编排已接入' : 'WA n8n webhook 未配置'}</AlertTitle><AlertDescription>{loading ? '加载中...' : '注册仍走 workflow；登录态检测、长连接恢复和 OTP MQ 投放由 wa-app 直连服务完成。'}</AlertDescription></Alert><div className="grid gap-3 md:grid-cols-2"><InfoCard icon={<Workflow size={16} />} title="注册 workflow" badge="n8n" text="号码检测、SMS 可发性、发起 OTP、等待 resume 回调和提交验证码。" /><InfoCard icon={<RefreshCw size={16} />} title="登录态 / 长连接" badge="直连" text="登录态检测、服务重启恢复长连接、chatd ping 心跳和 OTP 投放 MQ 不进入 workflow。" /></div><div className="grid gap-2">{workflows.map((item) => <div key={item.key} className="flex items-center justify-between rounded-xl border bg-card p-3 text-sm"><span>{item.label}</span><code className="text-xs text-muted-foreground">{item.webhook_path}</code></div>)}</div><Button variant="outline" asChild><a href="/workflow" target="_blank" rel="noreferrer">打开 Workflow 状态页</a></Button></div>;
}

function InfoCard({ icon, title, badge, text }: { icon: ReactNode; title: string; badge: string; text: string }) {
  return <Card><CardContent className="grid gap-2 p-4"><div className="flex items-center justify-between"><div className="flex items-center gap-2 font-medium">{icon}{title}</div><Badge variant="outline">{badge}</Badge></div><p className="text-sm text-muted-foreground">{text}</p></CardContent></Card>;
}

function findConnection(connections: WaConnectionState[], row: WaManagedNumber | null) {
  const loginState = row?.result?.login_state || {};
  const loginStateId = String(loginState.login_state_id || '');
  const registeredIdentityId = String(loginState.registered_identity_id || '');
  if (!row || (!loginStateId && !registeredIdentityId)) return null;
  return connections.find((item) => (loginStateId && item.login_state_id === loginStateId) || (registeredIdentityId && item.registered_identity_id === registeredIdentityId)) || null;
}
