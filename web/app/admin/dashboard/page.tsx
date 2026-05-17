import { RefreshCw } from "lucide-react";
import { AppShell, MetricCard, PageHead, StatusBadge } from "@/components/shell";
import { channels, requests } from "@/lib/mock";

export default function AdminDashboardPage() {
  return (
    <AppShell title="管理员后台" variant="admin">
      <PageHead
        eyebrow="Admin"
        title="平台运营总览"
        description="集中查看中转流量、渠道健康、账号池和错误率。管理端和用户端共用同一视觉系统，但信息密度更高。"
        action={<button className="btn"><RefreshCw /> 刷新池</button>}
      />
      <div className="grid grid-4">
        <MetricCard label="总请求" value="12.8M" foot="all time" />
        <MetricCard label="总 Token" value="1.94B" foot="billable usage" tone="green" />
        <MetricCard label="活跃渠道" value="9" foot="3 providers" tone="green" />
        <MetricCard label="活跃凭证" value="42" foot="inside channels" />
      </div>
      <div className="grid grid-2" style={{ marginTop: 16 }}>
        <section className="card">
          <div className="card-pad">
            <h2>渠道健康</h2>
            <p className="muted" style={{ margin: 0 }}>按渠道类型和错误率观察当前可用性。</p>
          </div>
          <div className="table-wrap">
            <table>
              <thead><tr><th>渠道</th><th>类型</th><th>状态</th><th>账号</th><th>错误率</th></tr></thead>
              <tbody>{channels.map((row) => (
                <tr key={row.name}><td>{row.name}</td><td>{row.type}</td><td><StatusBadge value={row.status} /></td><td>{row.accounts}</td><td>{row.error}</td></tr>
              ))}</tbody>
            </table>
          </div>
        </section>
        <section className="card">
          <div className="card-pad">
            <h2>最近异常</h2>
            <p className="muted" style={{ margin: 0 }}>快速判断是用户限流、上游错误还是账号池冷却。</p>
          </div>
          <div className="table-wrap">
            <table>
              <thead><tr><th>时间</th><th>模型</th><th>状态</th><th>延迟</th></tr></thead>
              <tbody>{requests.map((row) => (
                <tr key={`${row.time}-admin`}><td>{row.time}</td><td>{row.model}</td><td><StatusBadge value={row.status} /></td><td>{row.latency}</td></tr>
              ))}</tbody>
            </table>
          </div>
        </section>
      </div>
    </AppShell>
  );
}
