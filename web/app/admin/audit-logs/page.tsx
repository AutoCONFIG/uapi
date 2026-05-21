"use client";

import { useEffect, useState } from "react";
import { AppShell, PageHead, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";

type AuditEntry = {
  id: number;
  action: string;
  target_type: string;
  target_id: string;
  actor: string;
  created_at: string;
};

function actionLabel(action: string): string {
  const map: Record<string, string> = {
    create: "创建",
    update: "更新",
    delete: "删除",
  };
  return map[action] || action;
}

function targetTypeLabel(t: string): string {
  const map: Record<string, string> = {
    channel: "渠道",
    account: "凭证",
    token: "令牌",
    plan: "套餐",
    user: "用户",
  };
  return map[t] || t;
}

export default function AuditLogsPage() {
  const [logs, setLogs] = useState<AuditEntry[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);

  function load(p: number) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) { setLoading(false); return; }
    setLoading(true);
    adminApi.auditLogs(token, p, 20)
      .then((data) => { setLogs(data.items); setTotal(data.total); setPage(p); })
      .catch(() => {})
      .finally(() => setLoading(false));
  }

  useEffect(() => { load(1); }, []);

  const totalPages = Math.ceil(total / 20);

  return (
    <AppShell title="审计日志" variant="admin">
      <PageHead
        eyebrow="Admin / Audit"
        title="后台操作审计"
        description="记录管理员和系统对渠道、用户余额、账号池的关键变更。"
      />
      <section className="card">
        <div className="table-wrap">
          <table>
            <thead><tr><th>时间</th><th>操作者</th><th>动作</th><th>目标类型</th><th>目标ID</th></tr></thead>
            <tbody>
              {logs.map((row) => (
                <tr key={row.id}>
                  <td>{new Date(row.created_at).toLocaleTimeString()}</td>
                  <td>{row.actor}</td>
                  <td><StatusBadge value={row.action} /></td>
                  <td>{targetTypeLabel(row.target_type)}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{row.target_id}</td>
                </tr>
              ))}
              {logs.length === 0 && !loading && (
                <tr><td colSpan={5} className="muted" style={{ textAlign: "center", padding: 24 }}>
                  {loading ? "加载中…" : "暂无审计日志"}
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