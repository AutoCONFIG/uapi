"use client";

import { useEffect, useState } from "react";
import { AppShell, PageHead, StatusBadge } from "@/components/shell";
import { userApi } from "@/lib/api";
import type { UsageLogItem } from "@/types/api";

function compactNumber(value?: number) {
  return new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 }).format(Number(value ?? 0));
}

export default function UserLogsPage() {
  const [logs, setLogs] = useState<UsageLogItem[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);

  function load(nextPage: number) {
    const token = window.localStorage.getItem("uapi.user.token");
    if (!token) { setLoading(false); return; }
    setLoading(true);
    userApi.usageLogs(token, nextPage, 20)
      .then((data) => {
        setLogs(data.logs ?? []);
        setTotal(data.total);
        setPage(nextPage);
      })
      .catch(() => undefined)
      .finally(() => setLoading(false));
  }

  useEffect(() => { load(1); }, []);

  const totalPages = Math.ceil(total / 20);

  return (
    <AppShell title="调用日志">
      <PageHead title="调用日志" description="查看当前账号下 API Key 产生的调用记录。" />
      <section className="card">
        <div className="table-wrap">
          <table>
            <thead><tr><th>时间</th><th>IP</th><th>模型</th><th>格式</th><th>状态</th><th>Token</th><th>延迟</th></tr></thead>
            <tbody>
              {logs.map((log) => (
                <tr key={log.id}>
                  <td>{new Date(log.created_at).toLocaleString()}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{log.client_ip || "-"}</td>
                  <td>{log.model || "-"}</td>
                  <td>{log.is_stream ? "流式" : "普通"}</td>
                  <td><StatusBadge value={String(log.status_code)} /></td>
                  <td>
                    <strong>{compactNumber(log.total_tokens || log.prompt_tokens + log.completion_tokens)}</strong>
                    <div className="muted" style={{ fontSize: 12 }}>入 {compactNumber(log.prompt_tokens)} / 出 {compactNumber(log.completion_tokens)}</div>
                  </td>
                  <td>{log.latency_ms}ms</td>
                </tr>
              ))}
              {logs.length === 0 && !loading ? (
                <tr><td colSpan={7} className="muted" style={{ textAlign: "center", padding: 24 }}>暂无调用日志</td></tr>
              ) : null}
            </tbody>
          </table>
        </div>
        {totalPages > 1 ? (
          <div style={{ display: "flex", justifyContent: "center", gap: 8, padding: 16 }}>
            <button className="btn" disabled={page <= 1} onClick={() => load(page - 1)} type="button">上一页</button>
            <span className="muted" style={{ lineHeight: "32px" }}>{page} / {totalPages}</span>
            <button className="btn" disabled={page >= totalPages} onClick={() => load(page + 1)} type="button">下一页</button>
          </div>
        ) : null}
      </section>
    </AppShell>
  );
}
