"use client";

import { useEffect, useState } from "react";
import { Copy } from "lucide-react";
import { AppShell, MetricCard, PageHead } from "@/components/shell";
import { userApi } from "@/lib/api";
import type { Profile, UsageSummary } from "@/types/api";

function formatNumber(n: number): string {
  if (n >= 1_000_000_000) return (n / 1_000_000_000).toFixed(1) + "B";
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + "M";
  if (n >= 1_000) return (n / 1_000).toFixed(1) + "K";
  return String(n);
}

function formatQuota(quota: number): string {
  if (quota >= 1_000_000) return `${(quota / 1_000_000).toFixed(1)}M`;
  if (quota >= 1_000) return `${(quota / 1_000).toFixed(1)}K`;
  return String(quota);
}

export default function OverviewPage() {
  const [profile, setProfile] = useState<Profile | null>(null);
  const [usage, setUsage] = useState<UsageSummary | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.user.token");
    if (!token) { setLoading(false); return; }
    Promise.all([
      userApi.profile(token).catch(() => null),
      userApi.usage(token).catch(() => null),
    ]).then(([p, u]) => {
      setProfile(p);
      setUsage(u);
      setLoading(false);
    });
  }, []);

  const balance = profile?.balance ?? 0;
  const successRate = usage ? (usage.total_requests > 0 ? ((usage.total_requests - usage.failed_requests) / usage.total_requests * 100).toFixed(1) : "0") : "—";

  return (
    <AppShell title="用户控制台">
      <PageHead
        eyebrow="Overview"
        title="生产流量总览"
        description="查看余额、请求表现和最近调用。"
      />
      <div className="grid grid-4">
        <MetricCard label="可用余额" value={formatNumber(balance)} foot="Token credits" tone="green" />
        <MetricCard label="总请求" value={usage ? formatNumber(usage.total_requests) : "—"} foot="all time" tone="primary" />
        <MetricCard label="成功率" value={`${successRate}%`} foot={usage ? `失败 ${usage.failed_requests}` : ""} tone="green" />
        <MetricCard label="总 Token" value={usage ? formatNumber(usage.total_tokens) : "—"} foot={`${formatQuota(balance)} quota`} />
      </div>

      {usage && usage.by_model && usage.by_model.length > 0 && (
        <section className="card card-pad" style={{ marginTop: 16 }}>
          <h2>按模型统计</h2>
          <div className="table-wrap">
            <table>
              <thead><tr><th>模型</th><th>请求数</th><th>Token</th></tr></thead>
              <tbody>
                {usage.by_model.map((m) => (
                  <tr key={m.model}>
                    <td>{m.model}</td>
                    <td>{m.requests.toLocaleString()}</td>
                    <td>{formatNumber(m.total_tokens)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}

      <section className="card card-pad" style={{ marginTop: 16 }}>
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
          同一个 Key 可用于 OpenAI Chat Completions API、OpenAI Responses API、Anthropic Messages API 和 Gemini API 格式入口。
        </p>
      </section>
    </AppShell>
  );
}
