"use client";

import { useEffect, useState } from "react";
import { Download } from "lucide-react";
import { AppShell, PageHead, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { UsageLogItem } from "@/types/api";

function formatTokens(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + "M";
  if (n >= 1_000) return (n / 1_000).toFixed(1) + "K";
  return String(n);
}

export default function LogsPage() {
  const [logs, setLogs] = useState<UsageLogItem[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);

  function load(p: number) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) { setLoading(false); return; }
    setLoading(true);
    adminApi.logs(token, p, 20)
      .then((data) => { setLogs(data.items); setTotal(data.total); setPage(p); })
      .catch(() => {})
      .finally(() => setLoading(false));
  }

  useEffect(() => { load(1); }, []);

  const totalPages = Math.ceil(total / 20);

  return (
    <AppShell title="调用日志" variant="admin">
      <PageHead
        title="调用日志"
        description="查看所有中转请求的模型、状态、Token 用量和延迟。"
      />
      <section className="card">
        <div className="table-wrap">
          <table>
            <thead><tr><th>时间</th><th>模型</th><th>流式</th><th>状态</th><th>Token</th><th>延迟</th></tr></thead>
            <tbody>
              {logs.map((row) => (
                <tr key={row.id}>
                  <td>{new Date(row.created_at).toLocaleTimeString()}</td>
                  <td>{row.model}</td>
                  <td>{row.is_stream ? "是" : "否"}</td>
                  <td><StatusBadge value={String(row.status_code)} /></td>
                  <td>{formatTokens(row.total_tokens)}</td>
                  <td>{row.latency_ms}ms</td>
                </tr>
              ))}
              {logs.length === 0 && !loading && (
                <tr><td colSpan={6} className="muted" style={{ textAlign: "center", padding: 24 }}>
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