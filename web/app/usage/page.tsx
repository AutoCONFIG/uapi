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

function tokenTotal(log: UsageLogItem) {
  return log.total_tokens || log.prompt_tokens + log.completion_tokens;
}

function cacheHitTokens(log: UsageLogItem) {
  return Number(log.cache_read_tokens ?? 0);
}

export default function UsagePage() {
  const [summary, setSummary] = useState<UsageSummary>(emptySummary);
  const [logs, setLogs] = useState<UsageLogItem[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loadingLogs, setLoadingLogs] = useState(true);

  function loadLogs(nextPage: number) {
    const token = window.localStorage.getItem("uapi.user.token");
    if (!token) {
      setLoadingLogs(false);
      return;
    }
    setLoadingLogs(true);
    userApi.usageLogs(token, nextPage, 20)
      .then((data) => {
        setLogs(data.logs ?? []);
        setTotal(data.total ?? 0);
        setPage(nextPage);
      })
      .catch(() => undefined)
      .finally(() => setLoadingLogs(false));
  }

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.user.token");
    if (!token) {
      setLoadingLogs(false);
      return;
    }
    Promise.all([userApi.usage(token), userApi.usageLogs(token, 1, 20)])
      .then(([usage, usageLogs]) => {
        setSummary({
          ...emptySummary,
          ...usage,
          by_model: usage.by_model ?? [],
          daily: usage.daily ?? [],
        });
        setLogs(usageLogs.logs ?? []);
        setTotal(usageLogs.total ?? 0);
        setPage(1);
      })
      .catch(() => undefined)
      .finally(() => setLoadingLogs(false));
  }, []);

  const bars = useMemo(() => {
    const points = summary.daily ?? [];
    if (!points.length) return [];
    const max = Math.max(...points.map((point) => point.total_tokens), 1);
    return points.map((point) => Math.max(8, Math.round((point.total_tokens / max) * 100)));
  }, [summary.daily]);

  const topModels = useMemo(() => (
    [...(summary.by_model ?? [])]
      .sort((a, b) => b.requests - a.requests)
      .slice(0, 10)
  ), [summary.by_model]);
  const totalPages = Math.ceil(total / 20);

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
      {topModels.length > 0 ? (
        <section className="card card-pad" style={{ marginTop: 16 }}>
          <h2>模型排行</h2>
          <div className="table-wrap">
            <table>
              <thead><tr><th>模型</th><th>请求数</th><th>Token</th></tr></thead>
              <tbody>
                {topModels.map((model) => (
                  <tr key={model.model}>
                    <td>{model.model || "-"}</td>
                    <td>{model.requests.toLocaleString()}</td>
                    <td>{compactNumber(model.total_tokens)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}
      <section className="card" style={{ marginTop: 16 }}>
        <div className="card-pad" style={{ paddingBottom: 0 }}>
          <h2>调用记录</h2>
        </div>
        <div className="table-wrap">
          <table>
            <thead><tr><th>时间</th><th>IP</th><th>模型</th><th>格式</th><th>状态</th><th>Token</th><th>延迟</th></tr></thead>
            <tbody>
              {logs.length > 0 ? logs.map((log) => (
                <tr key={log.id}>
                  <td>{new Date(log.created_at).toLocaleString()}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{log.client_ip || "-"}</td>
                  <td>{log.model || "-"}</td>
                  <td>{log.is_stream ? "流式" : "普通"}</td>
                  <td><StatusBadge value={logStatus(log)} /></td>
                  <td>
                    {tokenTotal(log) > 0 ? (
                      <>
                        <strong>{compactNumber(tokenTotal(log))}</strong>
                        <div className="muted" style={{ fontSize: 12 }}>入 {compactNumber(log.prompt_tokens)} / 出 {compactNumber(log.completion_tokens)}</div>
                        {cacheHitTokens(log) > 0 ? (
                          <div className="muted" style={{ fontSize: 12 }}>缓存命中 {compactNumber(cacheHitTokens(log))}</div>
                        ) : null}
                      </>
                    ) : (
                      <>
                        <strong>{log.status_code >= 400 ? "未产生" : "未返回"}</strong>
                        <div className="muted" style={{ fontSize: 12 }}>{log.status_code >= 400 ? "失败请求" : "上游未提供 usage"}</div>
                      </>
                    )}
                  </td>
                  <td>{log.latency_ms}ms</td>
                </tr>
              )) : !loadingLogs ? (
                <tr><td colSpan={7} className="muted" style={{ textAlign: "center", padding: 24 }}>暂无调用记录</td></tr>
              ) : (
                <tr><td colSpan={7} className="muted" style={{ textAlign: "center", padding: 24 }}>加载中...</td></tr>
              )}
            </tbody>
          </table>
        </div>
        {totalPages > 1 ? (
          <div style={{ display: "flex", justifyContent: "center", gap: 8, padding: 16 }}>
            <button className="btn" disabled={page <= 1 || loadingLogs} onClick={() => loadLogs(page - 1)} type="button">上一页</button>
            <span className="muted" style={{ lineHeight: "32px" }}>{page} / {totalPages}</span>
            <button className="btn" disabled={page >= totalPages || loadingLogs} onClick={() => loadLogs(page + 1)} type="button">下一页</button>
          </div>
        ) : null}
      </section>
    </AppShell>
  );
}
