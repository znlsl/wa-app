import { ResultSummaryPanel, type ResultSummaryMetric, type ResultTone } from '@byte-v-forge/common-ui';
import type { WaWorkflowResponse } from './wa-api';
import { booleanLabel, methodStateLabel, registeredLabel, smsLabel } from './wa-result-labels';
import { metaItems, outcomeMeta, waProbeStatus, type WaProbeStatus } from './wa-result-model';

export function WaResultPanel({ title, phone, result, loading }: { title: string; phone?: string; result?: WaWorkflowResponse | null; loading?: boolean }) {
  const status = waProbeStatus(result);
  const outcome = outcomeMeta(status, result, loading);
  const meta = metaItems(status, result);
  return (
    <ResultSummaryPanel
      title={title}
      subject={phone}
      badge={outcome}
      metrics={waMetrics(status)}
      methods={status.requestFailed ? [] : status.methodStatuses.map((method) => ({
        key: method.key,
        label: method.label,
        state: methodStateLabel(method.available, method.cooldownSeconds),
      }))}
      meta={meta}
    />
  );
}

function waMetrics(status: WaProbeStatus): ResultSummaryMetric[] {
  if (status.requestFailed) return [{ label: '请求', value: '失败', tone: 'bad' }];
  return [
    {
      label: '注册',
      value: registeredLabel(status.registered, status.accountFlow),
      tone: registrationTone(status),
    },
    {
      label: 'SMS',
      value: smsLabel(status.smsAvailable, status.smsWaitSeconds),
      tone: smsTone(status),
    },
    {
      label: '封禁',
      value: booleanLabel(status.blocked),
      tone: booleanTone(status.blocked),
    },
  ];
}

function registrationTone(status: WaProbeStatus): ResultTone {
  if (status.registered === true) return 'warn';
  if (status.accountFlow === 'not_registered' || status.registered === false) return 'ok';
  return 'idle';
}

function smsTone(status: WaProbeStatus): ResultTone {
  if (status.smsAvailable === true && !status.smsWaitSeconds) return 'ok';
  if (status.smsAvailable === false || Boolean(status.smsWaitSeconds)) return 'warn';
  return 'idle';
}

function booleanTone(value?: boolean): ResultTone {
  if (value === true) return 'bad';
  if (value === false) return 'ok';
  return 'idle';
}
