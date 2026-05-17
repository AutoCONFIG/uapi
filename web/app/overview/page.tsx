import { Copy, KeyRound, Plus } from "lucide-react";
import { AppShell, MetricCard, PageHead, StatusBadge } from "@/components/shell";
import { metrics, requests, usageBars } from "@/lib/mock";

export default function OverviewPage() {
  return (
    <AppShell title="用户控制台">
      <PageHead
        eyebrow="Overview"
        title="生产流量总览"
        description="查看余额、请求表现和最近调用，直接复制 OpenAI-compatible 接入代码。"
        action={<button className="btn primary"><Plus /> 创建 API Key</button>}
      />
      <div className="grid grid-4">
        {metrics.map((metric) => <MetricCard key={metric.label} {...metric} />)}
      </div>
      <div className="split" style={{ marginTop: 16 }}>
        <section className="card card-pad">
          <div style={{ display: "flex", justifyContent: "space-between", gap: 12 }}>
            <div>
              <h2>用量趋势</h2>
              <p className="muted">过去 12 小时请求量</p>
            </div>
            <span className="badge">20M quota · 42% used</span>
          </div>
          <div className="chart-bars">
            {usageBars.map((height, index) => <span key={index} style={{ height: `${height}%` }} />)}
          </div>
        </section>
        <section className="card card-pad">
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", gap: 12 }}>
            <h2>快速接入</h2>
            <button className="btn" title="复制代码"><Copy /> Copy</button>
          </div>
          <pre className="code-block">{`curl https://api.example.com/v1/chat/completions \\
  -H "Authorization: Bearer sk-relay-..." \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "gpt-4.1",
    "messages": [{"role": "user", "content": "Ping"}]
  }'`}</pre>
          <p className="muted" style={{ margin: "12px 0 0", fontSize: 13 }}>
            同一个 Key 可用于 OpenAI Chat、Responses、Anthropic Messages 和 Gemini 格式入口。
          </p>
        </section>
      </div>
      <section className="card" style={{ marginTop: 16 }}>
        <div className="card-pad" style={{ display: "flex", justifyContent: "space-between", alignItems: "center" }}>
          <div>
            <h2>最近请求</h2>
            <p className="muted" style={{ margin: 0 }}>用于快速定位失败、限流和高延迟调用。</p>
          </div>
          <button className="btn"><KeyRound /> API Keys</button>
        </div>
        <div className="table-wrap">
          <table>
            <thead><tr><th>时间</th><th>模型</th><th>格式</th><th>状态</th><th>Token</th><th>延迟</th></tr></thead>
            <tbody>
              {requests.map((row) => (
                <tr key={`${row.time}-${row.model}`}>
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
