"use client";

import { useEffect, useState } from "react";
import { AppShell, PageHead, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import { formatAdminDateTime } from "@/lib/datetime";

type AuditEntry = {
  id: number;
  user: string;
  action: string;
  resource: string;
  resource_id: string;
  old_value?: string;
  new_value?: string;
  ip_address?: string;
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
    relay_node: "节点",
    node_channel: "节点绑定",
    access_policy: "限制策略",
    redeem_code: "兑换码",
    settings: "系统设置",
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
      .then((data) => { setLogs(data.items ?? []); setTotal(data.total); setPage(p); })
      .catch(() => {})
      .finally(() => setLoading(false));
  }

  useEffect(() => { load(1); }, []);

  const totalPages = Math.ceil(total / 20);

  return (
    <AppShell title="系统审计" variant="admin">
      <PageHead
        title="系统审计"
        description="记录管理员和系统对渠道、用户套餐、账号池的关键变更。"
      />
      <section className="card">
        <div className="table-wrap">
          <table>
            <thead><tr><th>时间</th><th>操作者</th><th>IP</th><th>动作</th><th>目标</th><th>详情</th></tr></thead>
            <tbody>
              {logs.map((row) => (
                <tr key={row.id}>
                  <td>{formatAdminDateTime(row.created_at)}</td>
                  <td>{row.user || "-"}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{row.ip_address || "-"}</td>
                  <td><StatusBadge value={actionLabel(row.action)} /></td>
                  <td>{targetTypeLabel(row.resource)}<div className="muted" style={{ fontSize: 12 }}>{row.resource_id}</div></td>
                  <td className="muted" style={{ fontSize: 12, maxWidth: 360, whiteSpace: "normal" }}>{row.new_value || row.old_value || "-"}</td>
                </tr>
              ))}
              {logs.length === 0 && !loading && (
                <tr><td colSpan={6} className="muted" style={{ textAlign: "center", padding: 24 }}>
                  {loading ? "加载中…" : "暂无系统审计"}
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
