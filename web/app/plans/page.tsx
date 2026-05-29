"use client";

import { useEffect, useState } from "react";
import { CalendarDays, Check, Gift, ShieldCheck, WalletCards } from "lucide-react";
import { AppShell, PageHead } from "@/components/shell";
import { userApi } from "@/lib/api";
import { formatQuota } from "@/lib/format";
import type { AvailableModels, PublicPlan, Subscription, SubscriptionWindow } from "@/types/api";

function quotaValues(subscription: Subscription) {
  const monthly = subscription.windows.find((item) => item.type === "month");
  return {
    total: monthly?.limit ?? 0,
    used: monthly?.used ?? 0,
    remaining: monthly?.remaining ?? 0,
  };
}

function windowLabel(type: SubscriptionWindow["type"]): string {
  return ({ hour: "5 小时", week: "本周", month: "本月" } as const)[type] || type;
}

function percent(used: number, limit: number): number {
  if (limit <= 0) return 100;
  return Math.min(100, Math.max(0, (used / limit) * 100));
}

function planWindow(plan: PublicPlan, type: SubscriptionWindow["type"]) {
  return plan.windows.find((item) => item.type === type)?.limit ?? 0;
}

export default function PlansPage() {
  const [subscription, setSubscription] = useState<Subscription | null>(null);
  const [plans, setPlans] = useState<PublicPlan[]>([]);
  const [availableModels, setAvailableModels] = useState<AvailableModels | null>(null);
  const [loading, setLoading] = useState(true);
  const [redeemCode, setRedeemCode] = useState("");
  const [redeemMsg, setRedeemMsg] = useState("");

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.user.token");
    if (!token) { setLoading(false); return; }
    Promise.all([
      userApi.subscription(token).catch(() => null),
      userApi.plans(token).catch(() => []),
      userApi.models(token).catch(() => null),
    ]).then(([sub, planItems, models]) => {
      setSubscription(sub);
      setPlans(planItems);
      setAvailableModels(models);
      setLoading(false);
    });
  }, []);

  async function redeem() {
    const token = window.localStorage.getItem("uapi.user.token");
    if (!token || !redeemCode.trim()) return;
    try {
      const result = await userApi.redeem(token, redeemCode.trim());
      setRedeemMsg(`兑换成功，当前套餐：${result.plan_name}`);
      setRedeemCode("");
      const sub = await userApi.subscription(token).catch(() => result);
      setSubscription(sub);
    } catch (err) {
      setRedeemMsg(err instanceof Error ? err.message : "兑换失败");
    }
  }

  const expiresAt = subscription?.expires_at ? new Date(subscription.expires_at) : null;
  const startsAt = subscription?.starts_at ? new Date(subscription.starts_at) : null;
  const quota = subscription ? quotaValues(subscription) : null;
  const modelRatios = availableModels?.model_ratios ?? [];

  return (
    <AppShell title="套餐">
      <PageHead
        title="套餐权益"
        description="查看公开套餐、当前额度和可用模型倍率；隐藏套餐仍可通过兑换码或管理员分配使用。"
      />
      <div className="grid grid-2">
        <section className="card card-pad">
          <div style={{ display: "flex", justifyContent: "space-between", gap: 12, alignItems: "flex-start" }}>
            <div>
              <p className="muted" style={{ margin: 0 }}>当前套餐</p>
              <h2 style={{ marginTop: 6 }}>{subscription?.plan_name || (loading ? "加载中..." : "暂无有效套餐")}</h2>
            </div>
            <span className={`badge ${subscription ? "green" : ""}`}>
              {subscription ? <Check size={14} /> : <WalletCards size={14} />}
              {subscription ? "已生效" : "待领取"}
            </span>
          </div>

          {subscription ? (
            <div className="grid grid-2" style={{ marginTop: 18 }}>
              <div className="metric-card">
                <span className="muted">计费类型</span>
                <strong>{subscription.plan_type === "count_based" ? "按次数" : "按 Token"}</strong>
              </div>
              <div className="metric-card">
                <span className="muted">到期时间</span>
                <strong>{expiresAt ? expiresAt.toLocaleDateString() : "-"}</strong>
              </div>
            </div>
          ) : (
            <div className="empty-state" style={{ marginTop: 18 }}>
              <strong>还没有可用套餐</strong>
              <p>使用管理员发放的兑换码领取套餐，或等待管理员直接分配。</p>
            </div>
          )}

          {startsAt && expiresAt && (
            <p className="muted" style={{ display: "flex", alignItems: "center", gap: 8, margin: "16px 0 0" }}>
              <CalendarDays size={15} />
              {startsAt.toLocaleDateString()} 至 {expiresAt.toLocaleDateString()}
            </p>
          )}
        </section>

        <section className="card card-pad">
          <div style={{ display: "flex", justifyContent: "space-between", gap: 12, alignItems: "center" }}>
            <div>
              <p className="muted" style={{ margin: 0 }}>兑换码</p>
              <h2 style={{ marginTop: 6 }}>领取套餐</h2>
            </div>
            <span className="badge"><Gift size={14} /> Code</span>
          </div>
          <div style={{ display: "grid", gridTemplateColumns: "minmax(0, 1fr) auto", gap: 10, alignItems: "end", marginTop: 18 }}>
            <div className="field" style={{ margin: 0 }}>
              <label>兑换码</label>
              <input className="input" value={redeemCode} onChange={(e) => setRedeemCode(e.target.value)} placeholder="输入兑换码" />
            </div>
            <button className="btn primary" onClick={redeem} disabled={!redeemCode.trim()} type="button"><Gift /> 兑换</button>
          </div>
          {redeemMsg && <p style={{ marginTop: 10, fontSize: 13 }} className={redeemMsg.includes("成功") ? "muted" : "form-error"}>{redeemMsg}</p>}
        </section>
      </div>

      <div className="grid grid-2" style={{ marginTop: 16 }}>
        <section className="card card-pad">
          <h2>本月额度</h2>
          {subscription ? (
            <>
              <p className="metric-value" style={{ marginBottom: 0 }}>{formatQuota(quota?.remaining ?? 0, subscription.plan_type)}</p>
              <div className="progress" style={{ marginTop: 12 }}>
                <span style={{ width: `${percent(quota?.used ?? 0, quota?.total ?? 0)}%` }} />
              </div>
              <p className="muted" style={{ margin: "8px 0 0" }}>
                已用 {formatQuota(quota?.used ?? 0, subscription.plan_type)} / 总额 {formatQuota(quota?.total ?? 0, subscription.plan_type)}
              </p>
            </>
          ) : (
            <>
              <p className="metric-value" style={{ marginBottom: 0 }}>未开通</p>
              <p className="muted" style={{ margin: "8px 0 0" }}>没有有效套餐时，API Key 不能调用模型接口。</p>
            </>
          )}
        </section>
        <section className="card card-pad">
          <h2>窗口限制</h2>
          {subscription?.windows?.length ? (
            <div style={{ display: "grid", gap: 12, marginTop: 12 }}>
              {subscription.windows.map((item) => (
                <div key={item.type}>
                  <div style={{ display: "flex", justifyContent: "space-between", gap: 12, fontSize: 13 }}>
                    <strong>{windowLabel(item.type)}</strong>
                    <span className="muted">剩余 {formatQuota(item.remaining, subscription.plan_type)} / {formatQuota(item.limit, subscription.plan_type)}</span>
                  </div>
                  <div className="progress" style={{ marginTop: 8 }}>
                    <span style={{ width: `${percent(item.used, item.limit)}%` }} />
                  </div>
                  <p className="muted" style={{ margin: "6px 0 0", fontSize: 12 }}>重置 {new Date(item.reset_at).toLocaleString()}</p>
                </div>
              ))}
            </div>
          ) : (
            <p className="muted" style={{ display: "flex", alignItems: "center", gap: 8, margin: "10px 0 0" }}>
              <ShieldCheck size={16} />
              {subscription ? "当前套餐没有配置小时、周、月使用窗口。" : "管理员分配或兑换码兑换后，套餐会绑定到你的账号。"}
            </p>
          )}
        </section>
      </div>

      <section className="card card-pad" style={{ marginTop: 16 }}>
        <h2>公开套餐</h2>
        {plans.length ? (
          <div className="grid grid-3" style={{ marginTop: 12 }}>
            {plans.map((plan) => (
              <article className="metric-card" key={plan.id}>
                <span className="muted">{plan.type === "count_based" ? "按次数" : "按 Token"} · {plan.duration_days || 30} 天</span>
                <strong>{plan.name}</strong>
                <p className="muted" style={{ margin: "8px 0 0" }}>
                  月额度 {formatQuota(planWindow(plan, "month"), plan.type)}
                  {plan.max_concurrency > 0 ? ` · 并发 ${plan.max_concurrency}` : ""}
                </p>
                <p className="muted" style={{ margin: "6px 0 0", fontSize: 12 }}>
                  5 小时 {formatQuota(planWindow(plan, "hour"), plan.type)} · 周 {formatQuota(planWindow(plan, "week"), plan.type)}
                </p>
                <p className="muted" style={{ margin: "6px 0 0", fontSize: 12 }}>
                  {plan.allowed_models ? `模型：${plan.allowed_models}` : "模型不限"}
                </p>
              </article>
            ))}
          </div>
        ) : (
          <p className="muted" style={{ margin: "10px 0 0" }}>{loading ? "加载中..." : "暂无公开套餐"}</p>
        )}
      </section>

      <section className="card card-pad" style={{ marginTop: 16 }}>
        <h2>模型倍率</h2>
        {modelRatios.length ? (
          <div className="grid grid-3" style={{ marginTop: 12 }}>
            {modelRatios.map((item) => (
              <div className="metric-card" key={item.model}>
                <span className="muted">{item.model}</span>
                <strong>{item.ratio}x</strong>
              </div>
            ))}
          </div>
        ) : (
          <p className="muted" style={{ margin: "10px 0 0" }}>当前没有单独设置模型倍率，默认按 1x 计费。</p>
        )}
      </section>
    </AppShell>
  );
}
