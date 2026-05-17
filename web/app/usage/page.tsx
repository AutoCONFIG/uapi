"use client";

import { useEffect, useMemo, useState } from "react";
import { Download, SlidersHorizontal } from "lucide-react";
import { AppShell, MetricCard, PageHead, StatusBadge } from "@/components/shell";
import { userApi } from "@/lib/api";
import { requests, usageBars } from "@/lib/mock";
import type { UsageLogItem, UsageSummary } from "@/types/api";

const emptySummary: UsageSummary = {
  total_requests: 428190,
  failed_requests: 771,
  success_rate: 0.9982,
  total_tokens: 38700000,
  prompt_tokens: 15480000,
  completion_tokens: 23220000,
  by_model: [],
  daily: usageBars.map((height, index) => ({ date: String(index + 1), requests: height, total_tokens: height })),
};

function compactNumber(value: number) {
  return new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 }).format(value);
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
        setSummary(usage);
        setLogs(usageLogs.logs);
      })
      .catch(() => undefined);
  }, []);

  const bars = useMemo(() => {
    const points = summary.daily.length ? summary.daily : emptySummary.daily;
    const max = Math.max(...points.map((point) => point.total_tokens), 1);
    return points.map((point) => Math.max(8, Math.round((point.total_tokens / max) * 100)));
  }, [summary.daily]);

  const rows = logs.length ? logs.map((log) => ({
    key: String(log.id),
    time: new Date(log.created_at).toLocaleTimeString(),
    model: log.model,
    format: log.is_stream ? "Stream" : "JSON",
    status: logStatus(log),
    tokens: compactNumber(log.total_tokens),
    latency: `${log.latency_ms}ms`,
  })) : requests.map((row) => ({ key: `${row.time}-${row.format}`, ...row }));

  return (
    <AppShell title="用量统计">
      <PageHead
        eyebrow="Usage"
        title="请求、Token 和费用"
        description="按时间、模型、格式和状态查看中转流量。"
        action={<button className="btn"><Download /> Export</button>}
      />
      <div className="grid grid-3">
        <MetricCard label="总请求" value={compactNumber(summary.total_requests)} foot="All user keys" />
        <MetricCard label="总 Token" value={compactNumber(summary.total_tokens)} foot="prompt + completion" tone="green" />
        <MetricCard label="成功率" value={`${(summary.success_rate * 100).toFixed(2)}%`} foot={`${compactNumber(summary.failed_requests)} failed`} tone="amber" />
      </div>
      <section className="card card-pad" style={{ marginTop: 16 }}>
        <div style={{ display: "flex", justifyContent: "space-between", gap: 12 }}>
          <h2>趋势</h2>
          <button className="btn"><SlidersHorizontal /> 过滤</button>
        </div>
        <div className="chart-bars">
          {bars.map((height, index) => <span key={index} style={{ height: `${height}%` }} />)}
        </div>
      </section>
      <section className="card" style={{ marginTop: 16 }}>
        <div className="table-wrap">
          <table>
            <thead><tr><th>时间</th><th>模型</th><th>格式</th><th>状态</th><th>Token</th><th>延迟</th></tr></thead>
            <tbody>
              {rows.map((row) => (
                <tr key={row.key}>
                  <td>{row.time}</td><td>{row.model}</td><td>{row.format}</td><td><StatusBadge value={row.status} /></td><td>{row.tokens}</td><td>{row.latency}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </AppShell>
  );
}
