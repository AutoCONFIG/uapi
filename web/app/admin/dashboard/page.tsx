"use client";

import { useEffect, useState } from "react";
import { KeyRound, Network, Package, Route, Users } from "lucide-react";
import { AppShell, MetricCard, PageHead, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { Channel, Dashboard } from "@/types/api";

export default function AdminDashboardPage() {
  const [dashboard, setDashboard] = useState<Dashboard | null>(null);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) { setLoading(false); return; }
    Promise.all([
      adminApi.dashboard(token).catch(() => null),
      adminApi.channels(token).then(r => r.items).catch(() => []),
    ]).then(([d, ch]) => {
      if (d) setDashboard(d);
      setChannels(ch);
      setLoading(false);
    });
  }, []);

  function formatNumber(n: number): string {
    if (n >= 1_000_000_000) return (n / 1_000_000_000).toFixed(1) + "B";
    if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + "M";
    if (n >= 1_000) return (n / 1_000).toFixed(1) + "K";
    return String(n);
  }

  return (
    <AppShell title="总览" variant="admin">
      <PageHead
        title="平台运营总览"
        description="集中查看中转流量、渠道健康、账号池和错误率。"
      />
      <div className="grid grid-4">
        <MetricCard label="总请求" value={dashboard ? formatNumber(dashboard.total_requests) : "—"} foot="累计" />
        <MetricCard label="总 Token" value={dashboard ? formatNumber(dashboard.total_tokens) : "—"} foot="计费用量" tone="green" />
        <MetricCard label="活跃渠道" value={dashboard ? String(dashboard.active_channels) : "—"} foot="providers" tone="green" />
        <MetricCard label="活跃凭证" value={dashboard ? String(dashboard.active_accounts) : "—"} foot="inside channels" />
      </div>
      <div className="grid grid-2" style={{ marginTop: 16 }}>
        <section className="card">
          <div className="card-pad">
            <h2>渠道健康</h2>
            <p className="muted" style={{ margin: 0 }}>按渠道类型和可用性观察当前状态。</p>
          </div>
          <div className="table-wrap">
            <table>
              <thead><tr><th>渠道</th><th>类型</th><th>状态</th><th>权重</th></tr></thead>
              <tbody>
                {channels.length > 0 ? channels.map((ch) => (
                  <tr key={ch.id}>
                    <td>{ch.name}</td>
                    <td>{ch.type}</td>
                    <td><StatusBadge value={ch.enabled ? "enabled" : "disabled"} /></td>
                    <td>{ch.priority}</td>
                  </tr>
                )) : (
                  <tr><td colSpan={4} className="muted" style={{ textAlign: "center", padding: 24 }}>
                    {loading ? "加载中…" : "暂无渠道数据"}
                  </td></tr>
                )}
              </tbody>
            </table>
          </div>
        </section>
        <section className="card">
          <div className="card-pad">
            <h2>快捷操作</h2>
            <p className="muted" style={{ margin: 0 }}>按常用配置顺序进入控制台。</p>
          </div>
          <div className="quick-grid">
            <a className="quick-card" href="/admin/plans">
              <Package />
              <span><strong>套餐策略</strong>配置额度、模型、并发和请求窗口</span>
            </a>
            <a className="quick-card" href="/admin/channels">
              <Route />
              <span><strong>渠道账号</strong>维护上游和凭证池</span>
            </a>
            <a className="quick-card" href="/admin/relay-nodes">
              <Network />
              <span><strong>节点</strong>配置节点权重和账号绑定</span>
            </a>
            <a className="quick-card" href="/admin/tokens">
              <KeyRound />
              <span><strong>平台令牌</strong>创建和绑定策略</span>
            </a>
            <a className="quick-card" href="/admin/users">
              <Users />
              <span><strong>用户管理</strong>处理状态和密码</span>
            </a>
          </div>
        </section>
      </div>
    </AppShell>
  );
}
