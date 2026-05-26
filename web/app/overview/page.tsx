"use client";

import { useEffect, useMemo, useState } from "react";
import { Copy } from "lucide-react";
import { AppShell, MetricCard, PageHead } from "@/components/shell";
import { publicApi, userApi } from "@/lib/api";
import { formatNumber } from "@/lib/format";
import type { ApiKey, PublicSettings, Subscription, UsageSummary } from "@/types/api";

export default function OverviewPage() {
  const [usage, setUsage] = useState<UsageSummary | null>(null);
  const [subscription, setSubscription] = useState<Subscription | null>(null);
  const [loading, setLoading] = useState(true);
  const [keys, setKeys] = useState<ApiKey[]>([]);
  const [publicSettings, setPublicSettings] = useState<PublicSettings | null>(null);
  const [origin, setOrigin] = useState("");

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.user.token");
    if (!token) { setLoading(false); return; }
    setOrigin(window.location.origin);
    Promise.all([
      userApi.usage(token).catch(() => null),
      userApi.keys(token).catch(() => []),
      userApi.subscription(token).catch(() => null),
      publicApi.settings().catch(() => null),
    ]).then(([u, k, sub, settings]) => {
      setUsage(u);
      setKeys(k);
      setSubscription(sub);
      setPublicSettings(settings);
      setLoading(false);
    });
  }, []);

  const successRate = usage ? (usage.total_requests > 0 ? ((usage.total_requests - usage.failed_requests) / usage.total_requests * 100).toFixed(1) : "0") : "—";
  const activeKey = keys.find((item) => item.enabled) ?? keys[0];
  const endpoint = publicSettings?.public_base_url || origin || "http://localhost";
  const topModels = useMemo(() => (
    [...(usage?.by_model ?? [])]
      .sort((a, b) => b.requests - a.requests)
      .slice(0, 10)
  ), [usage?.by_model]);

  function copyValue(value: string) {
    if (!value) return;
    navigator.clipboard?.writeText(value);
  }

  return (
    <AppShell title="用户控制台">
      <PageHead title="生产流量总览" />
      <section className="card card-pad">
        <h2>快速接入</h2>
        <div className="quick-access-list">
          <div className="quick-access-row">
            <span>Base URL</span>
            <code>{endpoint}</code>
            <button className="btn" onClick={() => copyValue(endpoint)} title="复制 Base URL" type="button"><Copy /> 复制</button>
          </div>
          <div className="quick-access-row">
            <span>API Key</span>
            <code>{activeKey?.key ?? "请先创建密钥"}</code>
            <button className="btn" disabled={!activeKey} onClick={() => copyValue(activeKey?.key ?? "")} title="复制 API Key" type="button"><Copy /> 复制</button>
          </div>
        </div>
        <p className="muted" style={{ margin: "12px 0 0", fontSize: 13 }}>
          同一个密钥可用于 OpenAI 对话补全、OpenAI 响应接口、Anthropic 消息接口和 Gemini 接口格式入口。
        </p>
      </section>

      <div className="grid grid-4" style={{ marginTop: 16 }}>
        <MetricCard label="当前套餐" value={subscription?.plan_name ?? "未开通"} foot={subscription ? "已生效" : "需兑换或管理员分配"} tone={subscription ? "green" : "primary"} />
        <MetricCard label="总请求" value={usage ? formatNumber(usage.total_requests) : "—"} foot="累计" tone="primary" />
        <MetricCard label="成功率" value={`${successRate}%`} foot={usage ? `失败 ${usage.failed_requests}` : ""} tone="green" />
        <MetricCard label="总 Token" value={usage ? formatNumber(usage.total_tokens) : "—"} foot="累计用量" />
      </div>

      {topModels.length > 0 && (
        <section className="card card-pad" style={{ marginTop: 16 }}>
          <h2>按模型统计</h2>
          <div className="table-wrap">
            <table>
              <thead><tr><th>模型</th><th>请求数</th><th>Token</th></tr></thead>
              <tbody>
                {topModels.map((m) => (
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
    </AppShell>
  );
}
