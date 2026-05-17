import { AppShell, PageHead, StatusBadge } from "@/components/shell";

const audits = [
  { time: "09:23:01", actor: "admin", action: "channel.update", target: "OpenAI Primary", result: "active" },
  { time: "09:10:44", actor: "admin", action: "user.balance.adjust", target: "ops@acme.io", result: "active" },
  { time: "08:55:19", actor: "system", action: "account.cooldown", target: "gemini-oauth-03", result: "paused" },
];

export default function AuditLogsPage() {
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
            <thead><tr><th>时间</th><th>操作者</th><th>动作</th><th>目标</th><th>结果</th></tr></thead>
            <tbody>{audits.map((row) => (
              <tr key={`${row.time}-${row.action}`}>
                <td>{row.time}</td><td>{row.actor}</td><td>{row.action}</td><td>{row.target}</td><td><StatusBadge value={row.result} /></td>
              </tr>
            ))}</tbody>
          </table>
        </div>
      </section>
    </AppShell>
  );
}
