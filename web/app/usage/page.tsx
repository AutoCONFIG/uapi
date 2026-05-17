import { Download, SlidersHorizontal } from "lucide-react";
import { AppShell, MetricCard, PageHead, StatusBadge } from "@/components/shell";
import { requests, usageBars } from "@/lib/mock";

export default function UsagePage() {
  return (
    <AppShell title="用量统计">
      <PageHead
        eyebrow="Usage"
        title="请求、Token 和费用"
        description="按时间、模型、格式和状态查看中转流量。第一版先对齐后端聚合和分页日志接口。"
        action={<button className="btn"><Download /> Export</button>}
      />
      <div className="grid grid-3">
        <MetricCard label="本月请求" value="428,190" foot="OpenAI + Anthropic + Gemini" />
        <MetricCard label="本月 Token" value="38.7M" foot="prompt + completion" tone="green" />
        <MetricCard label="失败请求" value="0.18%" foot="last 30 days" tone="amber" />
      </div>
      <section className="card card-pad" style={{ marginTop: 16 }}>
        <div style={{ display: "flex", justifyContent: "space-between", gap: 12 }}>
          <h2>趋势</h2>
          <button className="btn"><SlidersHorizontal /> 过滤</button>
        </div>
        <div className="chart-bars">
          {usageBars.map((height, index) => <span key={index} style={{ height: `${height}%` }} />)}
        </div>
      </section>
      <section className="card" style={{ marginTop: 16 }}>
        <div className="table-wrap">
          <table>
            <thead><tr><th>时间</th><th>模型</th><th>格式</th><th>状态</th><th>Token</th><th>延迟</th></tr></thead>
            <tbody>
              {requests.map((row) => (
                <tr key={`${row.time}-${row.format}`}>
                  <td>{row.time}</td><td>{row.model}</td><td>{row.format}</td><td><StatusBadge value={row.status} /></td><td>{row.tokens}</td><td>{row.latency}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </AppShell>
  );
}
