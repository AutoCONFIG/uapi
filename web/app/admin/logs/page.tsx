import { Download } from "lucide-react";
import { AppShell, PageHead, StatusBadge } from "@/components/shell";
import { requests } from "@/lib/mock";

export default function LogsPage() {
  return (
    <AppShell title="请求日志" variant="admin">
      <PageHead
        eyebrow="Admin / Logs"
        title="请求日志和审计"
        description="第一版合并展示请求日志入口；后续可拆出 audit logs 页面和高级筛选。"
        action={<button className="btn"><Download /> Export</button>}
      />
      <section className="card">
        <div className="table-wrap">
          <table>
            <thead><tr><th>时间</th><th>用户</th><th>模型</th><th>格式</th><th>状态</th><th>Token</th><th>延迟</th></tr></thead>
            <tbody>{requests.map((row, index) => (
              <tr key={`${row.time}-logs`}><td>{row.time}</td><td>{index % 2 ? "ops@acme.io" : "team@northstar.dev"}</td><td>{row.model}</td><td>{row.format}</td><td><StatusBadge value={row.status} /></td><td>{row.tokens}</td><td>{row.latency}</td></tr>
            ))}</tbody>
          </table>
        </div>
      </section>
    </AppShell>
  );
}
