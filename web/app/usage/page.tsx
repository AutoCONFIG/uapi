"use client";

import { useEffect, useMemo, useState } from "react";
import { AppShell, MetricCard, PageHead, StatusBadge } from "@/components/shell";
import { userApi } from "@/lib/api";
import type { UsageLogItem, UsageSummary } from "@/types/api";

const emptySummary: UsageSummary = {
  total_requests: 0,
  failed_requests: 0,
  success_rate: 1,
  total_tokens: 0,
  prompt_tokens: 0,
  completion_tokens: 0,
  by_model: [],
  daily: [],
};

function compactNumber(value?: number) {
  return new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 }).format(Number(value ?? 0));
}

function logStatus(log: UsageLogItem) {
  return String(log.status_code);
}

export default function UsagePage() {
  const [summary, setSummary] = useState<UsageSummary>(emptySummary);
  const [logs, setLogs] = useState<UsageLogItem[]>([]);

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.user.token");
    if (!token) return;
    Promise.all([userApi.usage(token), userApi.usageLogs(token, 1, 20)])
      .then(([usage, usageLogs]) => {
        setSummary({
          ...emptySummary,
          ...usage,
          by_model: usage.by_model ?? [],
          daily: usage.daily ?? [],
        });
        setLogs(usageLogs.logs ?? []);
      })
      .catch(() => undefined);
  }, []);

  const bars = useMemo(() => {
    const points = summary.daily ?? [];
    if (!points.length) return [];
    const max = Math.max(...points.map((point) => point.total_tokens), 1);
    return points.map((point) => Math.max(8, Math.round((point.total_tokens / max) * 100)));
  }, [summary.daily]);

  return (
    <AppShell title="用量统计">
      <PageHead title="用量统计" />
      <div className="grid grid-3">
        <MetricCard label="总请求" value={compactNumber(summary.total_requests)} foot="当前账号" />
        <MetricCard label="总 Token" value={compactNumber(summary.total_tokens)} foot="输入 + 输出" tone="green" />
        <MetricCard label="成功率" value={`${(summary.success_rate * 100).toFixed(2)}%`} foot={`失败 ${compactNumber(summary.failed_requests)}`} tone="amber" />
      </div>
      {bars.length > 0 && (
        <section className="card card-pad" style={{ marginTop: 16 }}>
          <div style={{ display: "flex", justifyContent: "space-between", gap: 12 }}>
            <h2>趋势</h2>
          </div>
          <div className="chart-bars">
            {bars.map((height, index) => <span key={index} style={{ height: `${height}%` }} />)}
          </div>
        </section>
      )}
      <section className="card" style={{ marginTop: 16 }}>
        <div className="table-wrap">
          <table>
            <thead><tr><th>时间</th><th>IP</th><th>模型</th><th>格式</th><th>状态</th><th>Token</th><th>延迟</th></tr></thead>
            <tbody>
              {logs.length > 0 ? logs.map((log) => (
                <tr key={log.id}>
                  <td>{new Date(log.created_at).toLocaleTimeString()}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{log.client_ip || "-"}</td>
                  <td>{log.model}</td>
                  <td>{log.is_stream ? "流式" : "普通"}</td>
                  <td><StatusBadge value={logStatus(log)} /></td>
                  <td>
                    <strong>{compactNumber(log.total_tokens || log.prompt_tokens + log.completion_tokens)}</strong>
                    <div className="muted" style={{ fontSize: 12 }}>入 {compactNumber(log.prompt_tokens)} / 出 {compactNumber(log.completion_tokens)}</div>
                  </td>
                  <td>{log.latency_ms}ms</td>
                </tr>
              )) : (
                <tr><td colSpan={7} className="muted" style={{ textAlign: "center", padding: 24 }}>暂无用量数据</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </section>
    </AppShell>
  );
}
