"use client";

import { useEffect, useState } from "react";
import { AppShell, PageHead, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import { formatTokens } from "@/lib/format";
import type { UsageLogItem } from "@/types/api";

function shortID(value?: string) {
  if (!value) return "-";
  return value.length > 12 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value;
}

function tokenTotal(row: UsageLogItem) {
  return row.total_tokens || row.prompt_tokens + row.completion_tokens;
}

export default function LogsPage() {
  const [logs, setLogs] = useState<UsageLogItem[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [filters, setFilters] = useState({ user: "", ip: "", model: "", start: "", end: "" });

  function load(p: number) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) { setLoading(false); return; }
    setLoading(true);
    adminApi.logs(token, p, 20, normalizedFilters())
      .then((data) => { setLogs(data.items ?? []); setTotal(data.total); setPage(p); })
      .catch(() => {})
      .finally(() => setLoading(false));
  }

  useEffect(() => { load(1); }, []);

  function normalizedFilters() {
    return {
      user: filters.user.trim(),
      ip: filters.ip.trim(),
      model: filters.model.trim(),
      start: filters.start ? new Date(filters.start).toISOString() : "",
      end: filters.end ? new Date(filters.end).toISOString() : "",
    };
  }

  function applyFilters() {
    load(1);
  }

  function clearFilters() {
    setFilters({ user: "", ip: "", model: "", start: "", end: "" });
    setTimeout(() => load(1), 0);
  }

  const totalPages = Math.ceil(total / 20);

  return (
    <AppShell title="调用日志" variant="admin">
      <PageHead
        title="调用日志"
        description="查看所有中转请求的模型、状态、Token 用量和延迟。"
      />
      <section className="card card-pad log-filter-panel" style={{ marginBottom: 12 }}>
        <div className="log-filter-grid">
          <div className="field"><label>用户</label><input className="input" value={filters.user} onChange={(e) => setFilters((cur) => ({ ...cur, user: e.target.value }))} placeholder="邮箱 / 用户名 / ID" /></div>
          <div className="field"><label>IP</label><input className="input" value={filters.ip} onChange={(e) => setFilters((cur) => ({ ...cur, ip: e.target.value }))} placeholder="127.0.0.1" /></div>
          <div className="field"><label>模型</label><input className="input" value={filters.model} onChange={(e) => setFilters((cur) => ({ ...cur, model: e.target.value }))} placeholder="model" /></div>
          <div className="field"><label>开始时间</label><input className="input" type="datetime-local" value={filters.start} onChange={(e) => setFilters((cur) => ({ ...cur, start: e.target.value }))} /></div>
          <div className="field"><label>结束时间</label><input className="input" type="datetime-local" value={filters.end} onChange={(e) => setFilters((cur) => ({ ...cur, end: e.target.value }))} /></div>
          <div className="row-actions log-filter-actions"><button className="btn primary" onClick={applyFilters} type="button">查询</button><button className="btn" onClick={clearFilters} type="button">重置</button></div>
        </div>
      </section>
      <section className="card">
        <div className="table-wrap">
          <table>
            <thead><tr><th>时间</th><th>用户</th><th>IP</th><th>账号</th><th>模型</th><th>格式</th><th>状态</th><th>Token</th><th>延迟</th></tr></thead>
            <tbody>
              {logs.map((row) => (
                <tr key={row.id}>
                  <td>{new Date(row.created_at).toLocaleTimeString()}</td>
                  <td>
                    <strong>{row.username || row.user_email || "-"}</strong>
                    {row.user_id ? <div className="muted" style={{ fontSize: 12 }}>{row.user_id}</div> : null}
                  </td>
                  <td className="muted" style={{ fontSize: 12 }}>{row.client_ip || "-"}</td>
                  <td>
                    <strong>{row.account_name || shortID(row.account_id)}</strong>
                    <div className="muted" style={{ fontSize: 12 }}>{row.channel_name || shortID(row.channel_id)} · {row.account_cred_type || "-"}</div>
                  </td>
                  <td>
                    <div style={{ whiteSpace: "nowrap" }}>
                      <span>{row.model || "-"}</span>
                      <span className="muted" style={{ fontSize: 12 }}> → {row.routed_model || "-"}</span>
                    </div>
                    {row.client_format || row.upstream_format ? (
                      <div className="muted" style={{ fontSize: 12, whiteSpace: "nowrap" }}>{row.client_format || "-"} → {row.upstream_format || "-"}</div>
                    ) : null}
                  </td>
                  <td>{row.is_stream ? "流式" : "普通"}</td>
                  <td><StatusBadge value={String(row.status_code)} /></td>
                  <td>
                    {tokenTotal(row) > 0 ? (
                      <>
                        <strong>{formatTokens(tokenTotal(row))}</strong>
                        <div className="muted" style={{ fontSize: 12 }}>入 {formatTokens(row.prompt_tokens)} / 出 {formatTokens(row.completion_tokens)}</div>
                      </>
                    ) : (
                      <>
                        <strong>{row.status_code >= 400 ? "未产生" : "未返回"}</strong>
                        <div className="muted" style={{ fontSize: 12 }}>{row.status_code >= 400 ? "失败请求" : "上游未提供 usage"}</div>
                      </>
                    )}
                  </td>
                  <td>{row.latency_ms}ms</td>
                </tr>
              ))}
              {logs.length === 0 && !loading && (
                <tr><td colSpan={9} className="muted" style={{ textAlign: "center", padding: 24 }}>
                  {loading ? "加载中…" : "暂无调用日志"}
                </td></tr>
              )}
            </tbody>
          </table>
        </div>
        {totalPages > 1 && (
          <div style={{ display: "flex", justifyContent: "center", gap: 8, padding: 16 }}>
            <button className="btn" disabled={page <= 1} onClick={() => load(page - 1)}>上一页</button>
            <span className="muted" style={{ lineHeight: "32px" }}>{page} / {totalPages}</span>
            <button className="btn" disabled={page >= totalPages} onClick={() => load(page + 1)}>下一页</button>
          </div>
        )}
      </section>
    </AppShell>
  );
}
