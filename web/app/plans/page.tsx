"use client";

import { useEffect, useState } from "react";
import { Check, Gift } from "lucide-react";
import { AppShell, PageHead } from "@/components/shell";
import { userApi } from "@/lib/api";
import type { Plan, Profile } from "@/types/api";

function formatQuota(quota: number): string {
  if (quota >= 1_000_000) return `${(quota / 1_000_000).toFixed(1)}M tokens`;
  if (quota >= 1_000) return `${(quota / 1_000).toFixed(1)}K tokens`;
  return `${quota} tokens`;
}

export default function PlansPage() {
  const [plans, setPlans] = useState<Plan[]>([]);
  const [profile, setProfile] = useState<Profile | null>(null);
  const [loading, setLoading] = useState(true);
  const [subscribing, setSubscribing] = useState<string | null>(null);
  const [redeemCode, setRedeemCode] = useState("");
  const [redeemMsg, setRedeemMsg] = useState("");
  const [showRedeem, setShowRedeem] = useState(false);

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.user.token");
    if (!token) { setLoading(false); return; }
    Promise.all([
      userApi.plans(token).catch(() => []),
      userApi.profile(token).catch(() => null),
    ]).then(([p, pr]) => {
      setPlans(p);
      setProfile(pr);
      setLoading(false);
    });
  }, []);

  async function subscribe(planID: string) {
    const token = window.localStorage.getItem("uapi.user.token");
    if (!token) return;
    setSubscribing(planID);
    try {
      await userApi.subscribe(token, planID);
      const pr = await userApi.profile(token);
      setProfile(pr);
    } catch { /* ignore */ }
    setSubscribing(null);
  }

  async function redeem() {
    const token = window.localStorage.getItem("uapi.user.token");
    if (!token || !redeemCode.trim()) return;
    try {
      const result = await userApi.redeem(token, redeemCode.trim());
      setRedeemMsg(`兑换成功，当前套餐：${result.plan_name}`);
      setRedeemCode("");
      const pr = await userApi.profile(token);
      setProfile(pr);
    } catch (err) {
      setRedeemMsg(err instanceof Error ? err.message : "兑换失败");
    }
  }

  const balance = profile?.balance ?? 0;
  const usedPercent = balance > 0 ? Math.min(100, 42) : 0;

  return (
    <AppShell title="套餐">
      <PageHead
        title="套餐和充值"
        description="查看当前套餐、额度和可用升级项。兑换码用于兑换指定套餐。"
        action={
          <button className="btn" onClick={() => setShowRedeem(!showRedeem)}><Gift /> 兑换码</button>
        }
      />
      {showRedeem && (
        <section className="card card-pad" style={{ marginBottom: 16 }}>
          <div style={{ display: "flex", gap: 8, alignItems: "flex-end" }}>
            <div className="field" style={{ flex: 1, margin: 0 }}>
              <label>兑换码</label>
              <input className="input" value={redeemCode} onChange={(e) => setRedeemCode(e.target.value)} placeholder="输入兑换码" />
            </div>
            <button className="btn primary" onClick={redeem}>兑换</button>
          </div>
          {redeemMsg && <p style={{ marginTop: 8, fontSize: 13 }} className={redeemMsg.includes("成功") ? "muted" : "form-error"}>{redeemMsg}</p>}
        </section>
      )}
      <div className="grid grid-3">
        {plans.map((plan) => (
          <section className="card card-pad" key={plan.id}>
            <div style={{ display: "flex", justifyContent: "space-between", gap: 12 }}>
              <h2>{plan.name}</h2>
              {plan.enabled && <span className="badge green"><Check size={14} /> 可用</span>}
            </div>
            <p className="metric-value">{plan.type === "count_based" ? "按次数" : "按 Token"}</p>
            <p className="muted">{formatQuota(plan.token_quota)}</p>
            <button
              className={`btn ${plan.enabled ? "primary" : ""}`}
              style={{ width: "100%", marginTop: 12 }}
              type="button"
              disabled={!plan.enabled || subscribing === plan.id}
              onClick={() => subscribe(plan.id)}
            >
              {subscribing === plan.id ? "订阅中…" : plan.enabled ? "选择套餐" : "暂不可用"}
            </button>
          </section>
        ))}
        {plans.length === 0 && !loading && (
          <section className="card card-pad">
            <p className="muted" style={{ textAlign: "center" }}>{loading ? "加载中…" : "暂无可用套餐"}</p>
          </section>
        )}
      </div>
      <section className="card card-pad" style={{ marginTop: 16 }}>
        <h2>额度使用</h2>
        <div className="progress"><span style={{ width: `${usedPercent}%` }} /></div>
        <p className="muted" style={{ margin: "10px 0 0" }}>余额 {formatQuota(balance)}</p>
      </section>
    </AppShell>
  );
}