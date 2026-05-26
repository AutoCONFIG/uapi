"use client";

import { useEffect, useMemo, useState } from "react";
import { Copy, Plus, Save, Trash2 } from "lucide-react";
import { AppShell, PageHead, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { AccessPolicy, Plan, RedeemCode } from "@/types/api";

type PolicyDraft = {
  allowed_models: string;
  max_concurrency: string;
  hourly_limit: string;
  weekly_limit: string;
  monthly_limit: string;
};

type PlanDraft = {
  type: string;
  count_quota: string;
  token_quota: string;
  duration_days: string;
};

const defaultQuotaForType = (type: string) => type === "count_based" ? 1000 : 100000;

const emptyPolicyDraft = (): PolicyDraft => ({
  allowed_models: "",
  max_concurrency: "0",
  hourly_limit: "0",
  weekly_limit: "0",
  monthly_limit: "0",
});

function draftFromPolicy(policy?: AccessPolicy): PolicyDraft {
  if (!policy) return emptyPolicyDraft();
  return {
    allowed_models: policy.allowed_models || "",
    max_concurrency: String(policy.max_concurrency || 0),
    hourly_limit: String(policy.hourly_limit || 0),
    weekly_limit: String(policy.weekly_limit || 0),
    monthly_limit: String(policy.monthly_limit || 0),
  };
}

function policyBody(draft: PolicyDraft) {
  return {
    allowed_models: draft.allowed_models.trim(),
    max_concurrency: Number(draft.max_concurrency || 0),
    hourly_limit: Number(draft.hourly_limit || 0),
    weekly_limit: Number(draft.weekly_limit || 0),
    monthly_limit: Number(draft.monthly_limit || 0),
    enabled: true,
  };
}

function formatQuota(value: number) {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return String(value);
}

function quotaLabel(type: string) {
  return type === "count_based" ? "次数额度" : "Token 额度";
}

function planQuotaValue(draft: PlanDraft) {
  return draft.type === "count_based" ? draft.count_quota : draft.token_quota;
}

function updatePlanQuota(draft: PlanDraft, value: string): PlanDraft {
  return draft.type === "count_based" ? { ...draft, count_quota: value } : { ...draft, token_quota: value };
}

export default function AdminPlansPage() {
  const [plans, setPlans] = useState<Plan[]>([]);
  const [policies, setPolicies] = useState<AccessPolicy[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [saving, setSaving] = useState(false);
  const [draft, setDraft] = useState({ name: "", type: "count_based", count_quota: 1000, token_quota: 100000, duration_days: 30, enabled: true });
  const [policyDraft, setPolicyDraft] = useState<PolicyDraft>(emptyPolicyDraft());
  const [editingPolicies, setEditingPolicies] = useState<Record<string, PolicyDraft>>({});
  const [editingPlans, setEditingPlans] = useState<Record<string, PlanDraft>>({});
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [copiedCodeID, setCopiedCodeID] = useState("");
  const [redeemCodes, setRedeemCodes] = useState<RedeemCode[]>([]);
  const [redeemStatus, setRedeemStatus] = useState("active");
  const [redeemDraft, setRedeemDraft] = useState({ code: "", plan_id: "", max_uses: "1" });

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) { setLoading(false); return; }
    Promise.all([
      adminApi.plans(token, 1, 100).then((data) => data.items).catch(() => []),
      adminApi.accessPolicies(token, 1, 100).then((data) => data.items).catch(() => []),
      adminApi.redeemCodes(token, 1, 50, redeemStatus).then((data) => data.items).catch(() => []),
    ]).then(([planItems, policyItems, codeItems]) => {
      setPlans(planItems);
      setPolicies(policyItems);
      setRedeemCodes(codeItems);
      setLoading(false);
    });
  }, [redeemStatus]);

  const policyByID = useMemo(() => new Map(policies.map((policy) => [policy.id, policy])), [policies]);

  function openCreate() {
    setDraft({ name: "", type: "count_based", count_quota: 1000, token_quota: 100000, duration_days: 30, enabled: true });
    setPolicyDraft(emptyPolicyDraft());
    setError("");
    setNotice("");
    setCreating(true);
  }

  async function createPlan() {
    if (!draft.name.trim()) return;
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    setSaving(true);
    setError("");
    setNotice("");
    try {
      const createdPolicy = await adminApi.createAccessPolicy(token, policyBody(policyDraft));
      const createdPlan = await adminApi.createPlan(token, {
        name: draft.name.trim(),
        type: draft.type,
        policy_id: createdPolicy.id,
        count_quota: draft.type === "count_based" ? draft.count_quota : 0,
        token_quota: draft.type === "token_based" ? draft.token_quota : 0,
        duration_days: draft.duration_days,
        model_ratios: "{}",
        completion_ratio: "{}",
        enabled: draft.enabled,
      });
      setPolicies((cur) => [createdPolicy, ...cur]);
      setPlans((cur) => [createdPlan, ...cur]);
      setCreating(false);
      setNotice(`套餐 ${createdPlan.name} 已创建。`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建失败");
    } finally {
      setSaving(false);
    }
  }

  async function savePolicy(plan: Plan) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    const currentPolicy = plan.policy_id ? policyByID.get(plan.policy_id) : undefined;
    const currentDraft = editingPolicies[plan.id] || draftFromPolicy(currentPolicy);
    setSaving(true);
    setError("");
    setNotice("");
    try {
      if (currentPolicy) {
        const updated = await adminApi.updateAccessPolicy(token, currentPolicy.id, policyBody(currentDraft));
        setPolicies((cur) => cur.map((policy) => policy.id === updated.id ? updated : policy));
        setNotice(`${plan.name} 的策略已保存。`);
      } else {
        const created = await adminApi.createAccessPolicy(token, policyBody(currentDraft));
        const updatedPlan = await adminApi.updatePlan(token, plan.id, { policy_id: created.id });
        setPolicies((cur) => [created, ...cur]);
        setPlans((cur) => cur.map((item) => item.id === plan.id ? updatedPlan : item));
        setNotice(`${plan.name} 已绑定新策略。`);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }

  function planDraft(plan: Plan): PlanDraft {
    return editingPlans[plan.id] || {
      type: plan.type,
      count_quota: String(plan.count_quota ?? 0),
      token_quota: String(plan.token_quota ?? 0),
      duration_days: String(plan.duration_days || 30),
    };
  }

  function setPlanDraft(planID: string, next: PlanDraft) {
    setEditingPlans((cur) => ({ ...cur, [planID]: next }));
  }

  async function savePlanMeta(plan: Plan) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    const current = planDraft(plan);
    const countQuota = Number(current.count_quota || 0);
    const tokenQuota = Number(current.token_quota || 0);
    const durationDays = Number(current.duration_days || 30);
    if (countQuota < 0 || tokenQuota < 0) {
      setError("套餐额度不能小于 0");
      return;
    }
    if (durationDays < 1) {
      setError("有效天数必须大于 0");
      return;
    }
    setSaving(true);
    setError("");
    setNotice("");
    try {
      const updated = await adminApi.updatePlan(token, plan.id, {
        type: current.type,
        count_quota: current.type === "count_based" ? countQuota : 0,
        token_quota: current.type === "token_based" ? tokenQuota : 0,
        duration_days: durationDays,
      });
      setPlans((cur) => cur.map((item) => item.id === plan.id ? updated : item));
      setEditingPlans((cur) => {
        const next = { ...cur };
        delete next[plan.id];
        return next;
      });
      setNotice(`${plan.name} 的套餐额度已保存。`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "保存套餐失败");
    } finally {
      setSaving(false);
    }
  }

  async function deletePlan(id: string) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    const plan = plans.find((item) => item.id === id);
    if (!confirm(`确认删除套餐 ${plan?.name || id}？已有订阅记录不会被物理删除，但新用户将不可再选择。`)) return;
    try {
      await adminApi.deletePlan(token, id);
      setPlans((cur) => cur.filter((p) => p.id !== id));
      setNotice("套餐已删除。");
    } catch { /* ignore */ }
  }

  async function togglePlan(plan: Plan) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    try {
      const updated = await adminApi.updatePlan(token, plan.id, { enabled: !plan.enabled });
      setPlans((cur) => cur.map((p) => (p.id === plan.id ? updated : p)));
    } catch { /* ignore */ }
  }

  function rowDraft(plan: Plan) {
    const policy = plan.policy_id ? policyByID.get(plan.policy_id) : undefined;
    return editingPolicies[plan.id] || draftFromPolicy(policy);
  }

  function setRowDraft(planID: string, next: PolicyDraft) {
    setEditingPolicies((cur) => ({ ...cur, [planID]: next }));
  }

  async function loadRedeemCodes(status = redeemStatus) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    const data = await adminApi.redeemCodes(token, 1, 50, status);
    setRedeemCodes(data.items ?? []);
  }

  async function createRedeemCode() {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    if (!redeemDraft.plan_id) {
      setError("请先选择兑换套餐");
      return;
    }
    if (Number(redeemDraft.max_uses || 1) < 1) {
      setError("兑换次数必须大于 0");
      return;
    }
    setSaving(true);
    setError("");
    setNotice("");
    try {
      const created = await adminApi.createRedeemCode(token, {
        code: redeemDraft.code.trim() || undefined,
        plan_id: redeemDraft.plan_id,
        max_uses: Number(redeemDraft.max_uses || 1),
      });
      setRedeemCodes((cur) => [created, ...cur]);
      setRedeemDraft({ code: "", plan_id: "", max_uses: "1" });
      setNotice(`兑换码 ${created.code} 已创建。`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建兑换码失败");
    } finally {
      setSaving(false);
    }
  }

  async function deleteRedeemCode(id: string) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    const item = redeemCodes.find((code) => code.id === id);
    if (!confirm(`确认删除兑换码 ${item?.code || id}？`)) return;
    await adminApi.deleteRedeemCode(token, id);
    setRedeemCodes((cur) => cur.filter((item) => item.id !== id));
    setNotice("兑换码已删除。");
  }

  async function copyRedeemCode(item: RedeemCode) {
    await navigator.clipboard?.writeText(item.code);
    setCopiedCodeID(item.id);
    window.setTimeout(() => setCopiedCodeID((current) => current === item.id ? "" : current), 1400);
  }

  return (
    <AppShell title="套餐管理" variant="admin">
      <PageHead
        title="套餐"
        description="配置套餐额度，并在套餐内维护对应的模型、并发和请求窗口限制。"
        action={<button className="btn primary" onClick={openCreate}><Plus /> 新建套餐</button>}
      />
      {creating ? (
        <div className="modal-backdrop" role="presentation">
          <section aria-modal="true" className="modal" role="dialog">
            <div className="modal-head">
              <div>
                <p className="eyebrow">New Plan</p>
                <h2>新建套餐</h2>
              </div>
              <button className="btn" disabled={saving} onClick={() => { setCreating(false); setError(""); }} type="button">关闭</button>
            </div>
            <div className="grid grid-2" style={{ marginTop: 12 }}>
              <div className="field">
                <label>套餐名称</label>
                <input className="input" value={draft.name} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} placeholder="Starter" />
              </div>
              <div className="field">
                <label>计费类型</label>
                <select className="input" value={draft.type} onChange={(e) => {
                  const type = e.target.value;
                  setDraft((d) => type === "count_based"
                    ? { ...d, type, count_quota: d.count_quota <= 0 ? defaultQuotaForType(type) : d.count_quota }
                    : { ...d, type, token_quota: d.token_quota <= 0 ? defaultQuotaForType(type) : d.token_quota });
                }}>
                  <option value="count_based">按次数</option>
                  <option value="token_based">按 Token</option>
                </select>
              </div>
              <div className="field">
                <label>{quotaLabel(draft.type)}</label>
                <input className="input" min={0} type="number" value={draft.type === "count_based" ? draft.count_quota : draft.token_quota} onChange={(e) => {
                  const value = Number(e.target.value);
                  setDraft((d) => d.type === "count_based" ? { ...d, count_quota: value } : { ...d, token_quota: value });
                }} />
              </div>
              <div className="field">
                <label>有效天数</label>
                <input className="input" type="number" min={1} value={draft.duration_days} onChange={(e) => setDraft((d) => ({ ...d, duration_days: Number(e.target.value) }))} />
              </div>
            </div>
            <div className="grid grid-2" style={{ marginTop: 8 }}>
              <div className="field"><label>允许模型</label><input className="input" value={policyDraft.allowed_models} onChange={(e) => setPolicyDraft((d) => ({ ...d, allowed_models: e.target.value }))} placeholder="留空不限制" /></div>
              <div className="field"><label>最大并发</label><input className="input" type="number" value={policyDraft.max_concurrency} onChange={(e) => setPolicyDraft((d) => ({ ...d, max_concurrency: e.target.value }))} /></div>
              <div className="field"><label>每小时窗口</label><input className="input" type="number" value={policyDraft.hourly_limit} onChange={(e) => setPolicyDraft((d) => ({ ...d, hourly_limit: e.target.value }))} /></div>
              <div className="field"><label>每周窗口</label><input className="input" type="number" value={policyDraft.weekly_limit} onChange={(e) => setPolicyDraft((d) => ({ ...d, weekly_limit: e.target.value }))} /></div>
              <div className="field"><label>每月窗口</label><input className="input" type="number" value={policyDraft.monthly_limit} onChange={(e) => setPolicyDraft((d) => ({ ...d, monthly_limit: e.target.value }))} /></div>
            </div>
            {error && <p className="form-error">{error}</p>}
            <div className="form-actions">
              <button className="btn" disabled={saving} onClick={() => { setCreating(false); setError(""); }}>取消</button>
              <button className="btn primary" disabled={saving} onClick={createPlan}>{saving ? "创建中" : "确认创建"}</button>
            </div>
          </section>
        </div>
      ) : null}
      {error && !creating ? <p className="form-error">{error}</p> : null}
      {notice && !creating ? <p className="form-success">{notice}</p> : null}
      <section className="card">
        <div className="table-wrap plans-table-wrap">
          <table>
            <thead><tr><th>套餐</th><th>额度</th><th>有效期</th><th>限制策略</th><th>状态</th><th></th></tr></thead>
            <tbody>
              {plans.map((p) => {
                const draftRow = rowDraft(p);
                const metaDraft = planDraft(p);
                const policy = p.policy_id ? policyByID.get(p.policy_id) : undefined;
                return (
                  <tr key={p.id}>
                    <td>
                      <strong>{p.name}</strong>
                      <div className="field compact-field" style={{ marginTop: 8 }}>
                        <label>计费类型</label>
                        <select className="input" value={metaDraft.type} onChange={(e) => {
                          const type = e.target.value;
                          setPlanDraft(p.id, type === "count_based"
                            ? { ...metaDraft, type, count_quota: Number(metaDraft.count_quota || 0) <= 0 ? String(defaultQuotaForType(type)) : metaDraft.count_quota }
                            : { ...metaDraft, type, token_quota: Number(metaDraft.token_quota || 0) <= 0 ? String(defaultQuotaForType(type)) : metaDraft.token_quota });
                        }}>
                          <option value="count_based">按次数</option>
                          <option value="token_based">按 Token</option>
                        </select>
                      </div>
                    </td>
                    <td>
                      <div className="field compact-field">
                        <label>{quotaLabel(metaDraft.type)}</label>
                        <input className="input" min={0} type="number" value={planQuotaValue(metaDraft)} onChange={(e) => setPlanDraft(p.id, updatePlanQuota(metaDraft, e.target.value))} />
                      </div>
                    </td>
                    <td>
                      <div className="field compact-field">
                        <label>天数</label>
                        <input className="input" min={1} type="number" value={metaDraft.duration_days} onChange={(e) => setPlanDraft(p.id, { ...metaDraft, duration_days: e.target.value })} />
                      </div>
                    </td>
                    <td className="plan-policy-cell">
                      <div className="plan-policy-editor">
                        <div className="field compact-field plan-policy-models"><label>允许模型</label><input className="input" value={draftRow.allowed_models} onChange={(e) => setRowDraft(p.id, { ...draftRow, allowed_models: e.target.value })} placeholder="不限制" /></div>
                        <div className="field compact-field"><label>并发</label><input className="input" type="number" value={draftRow.max_concurrency} onChange={(e) => setRowDraft(p.id, { ...draftRow, max_concurrency: e.target.value })} /></div>
                        <div className="field compact-field"><label>小时</label><input className="input" type="number" value={draftRow.hourly_limit} onChange={(e) => setRowDraft(p.id, { ...draftRow, hourly_limit: e.target.value })} /></div>
                        <div className="field compact-field"><label>周</label><input className="input" type="number" value={draftRow.weekly_limit} onChange={(e) => setRowDraft(p.id, { ...draftRow, weekly_limit: e.target.value })} /></div>
                        <div className="field compact-field"><label>月</label><input className="input" type="number" value={draftRow.monthly_limit} onChange={(e) => setRowDraft(p.id, { ...draftRow, monthly_limit: e.target.value })} /></div>
                        <button className="btn plan-policy-save" disabled={saving} onClick={() => savePlanMeta(p)} title="保存套餐额度"><Save /> 套餐</button>
                        <button className="btn plan-policy-save" disabled={saving} onClick={() => savePolicy(p)} title={policy ? "保存策略" : "创建并绑定策略"}><Save /> {policy ? "保存" : "绑定"}</button>
                      </div>
                    </td>
                    <td className="plan-status-cell">
                      <button className="btn plan-table-action" onClick={() => togglePlan(p)} type="button">
                        <StatusBadge value={p.enabled ? "enabled" : "disabled"} />
                      </button>
                    </td>
                    <td className="plan-delete-cell"><button className="btn danger icon-only plan-table-action" onClick={() => deletePlan(p.id)} title="删除套餐" type="button"><Trash2 /></button></td>
                  </tr>
                );
              })}
              {plans.length === 0 && !loading && (
                <tr><td colSpan={6} className="muted" style={{ textAlign: "center", padding: 24 }}>{loading ? "加载中…" : "暂无套餐"}</td></tr>
              )}
            </tbody>
          </table>
        </div>
        <div className="plan-mobile-list">
          {plans.map((p) => {
            const draftRow = rowDraft(p);
            const metaDraft = planDraft(p);
            const policy = p.policy_id ? policyByID.get(p.policy_id) : undefined;
            return (
              <article className="plan-mobile-card" key={`mobile-${p.id}`}>
                <div className="plan-mobile-head">
                  <div>
                    <strong>{p.name}</strong>
                    <span>{metaDraft.type === "count_based" ? "按次数" : "按 Token"} · {formatQuota(Number(planQuotaValue(metaDraft) || 0))} · {metaDraft.duration_days || 30} 天</span>
                  </div>
                  <StatusBadge value={p.enabled ? "enabled" : "disabled"} />
                </div>
                <div className="plan-mobile-policy">
                  <div className="field compact-field">
                    <label>计费类型</label>
                    <select className="input" value={metaDraft.type} onChange={(e) => {
                      const type = e.target.value;
                      setPlanDraft(p.id, type === "count_based"
                        ? { ...metaDraft, type, count_quota: Number(metaDraft.count_quota || 0) <= 0 ? String(defaultQuotaForType(type)) : metaDraft.count_quota }
                        : { ...metaDraft, type, token_quota: Number(metaDraft.token_quota || 0) <= 0 ? String(defaultQuotaForType(type)) : metaDraft.token_quota });
                    }}>
                      <option value="count_based">按次数</option>
                      <option value="token_based">按 Token</option>
                    </select>
                  </div>
                  <div className="field compact-field"><label>{quotaLabel(metaDraft.type)}</label><input className="input" min={0} type="number" value={planQuotaValue(metaDraft)} onChange={(e) => setPlanDraft(p.id, updatePlanQuota(metaDraft, e.target.value))} /></div>
                  <div className="field compact-field"><label>有效天数</label><input className="input" min={1} type="number" value={metaDraft.duration_days} onChange={(e) => setPlanDraft(p.id, { ...metaDraft, duration_days: e.target.value })} /></div>
                  <div className="field compact-field"><label>允许模型</label><input className="input" value={draftRow.allowed_models} onChange={(e) => setRowDraft(p.id, { ...draftRow, allowed_models: e.target.value })} placeholder="不限制" /></div>
                  <div className="field compact-field"><label>并发</label><input className="input" type="number" value={draftRow.max_concurrency} onChange={(e) => setRowDraft(p.id, { ...draftRow, max_concurrency: e.target.value })} /></div>
                  <div className="field compact-field"><label>小时</label><input className="input" type="number" value={draftRow.hourly_limit} onChange={(e) => setRowDraft(p.id, { ...draftRow, hourly_limit: e.target.value })} /></div>
                  <div className="field compact-field"><label>周</label><input className="input" type="number" value={draftRow.weekly_limit} onChange={(e) => setRowDraft(p.id, { ...draftRow, weekly_limit: e.target.value })} /></div>
                  <div className="field compact-field"><label>月</label><input className="input" type="number" value={draftRow.monthly_limit} onChange={(e) => setRowDraft(p.id, { ...draftRow, monthly_limit: e.target.value })} /></div>
                </div>
                <div className="plan-mobile-actions">
                  <button className="btn" disabled={saving} onClick={() => savePlanMeta(p)} type="button"><Save /> 保存套餐</button>
                  <button className="btn" disabled={saving} onClick={() => savePolicy(p)} type="button"><Save /> {policy ? "保存策略" : "绑定策略"}</button>
                  <button className="btn" onClick={() => togglePlan(p)} type="button">{p.enabled ? "停用" : "启用"}</button>
                  <button className="btn danger icon-only" onClick={() => deletePlan(p.id)} title="删除套餐" type="button"><Trash2 /></button>
                </div>
              </article>
            );
          })}
          {plans.length === 0 && !loading ? <EmptyPlanMobile /> : null}
        </div>
      </section>

      <section className="card card-pad redeem-panel" style={{ marginTop: 16 }}>
        <div className="section-head redeem-head">
          <div>
            <h2>兑换码</h2>
            <p className="muted">兑换码绑定套餐，套餐有效期从用户兑换成功时开始计算。</p>
          </div>
          <select className="input redeem-status-filter" value={redeemStatus} onChange={(e) => setRedeemStatus(e.target.value)}>
            <option value="active">可用</option>
            <option value="used">已用</option>
            <option value="all">全部</option>
          </select>
        </div>
        <div className="redeem-create-row">
          <div className="field compact-field redeem-code-field"><label>兑换码</label><input className="input" value={redeemDraft.code} onChange={(e) => setRedeemDraft((cur) => ({ ...cur, code: e.target.value }))} placeholder="留空自动生成" /></div>
          <div className="field compact-field redeem-plan-field"><label>套餐</label><select className="input" value={redeemDraft.plan_id} onChange={(e) => setRedeemDraft((cur) => ({ ...cur, plan_id: e.target.value }))}><option value="">选择套餐</option>{plans.map((plan) => <option value={plan.id} key={plan.id}>{plan.name} · {plan.duration_days || 30} 天</option>)}</select></div>
          <div className="field compact-field redeem-uses-field"><label>次数</label><input className="input" type="number" min={1} value={redeemDraft.max_uses} onChange={(e) => setRedeemDraft((cur) => ({ ...cur, max_uses: e.target.value }))} /></div>
          <button className="btn primary redeem-create-button" disabled={saving} onClick={createRedeemCode} type="button"><Plus /> 新增</button>
        </div>
        <div className="table-wrap" style={{ marginTop: 12 }}>
          <table>
            <thead><tr><th>兑换码</th><th>套餐</th><th>状态</th><th>使用次数</th><th>最近使用者</th><th></th></tr></thead>
            <tbody>
              {redeemCodes.map((item) => (
                <tr key={item.id}>
                  <td><code>{item.code}</code></td>
                  <td>{plans.find((plan) => plan.id === item.plan_id)?.name || item.plan_id}</td>
                  <td><StatusBadge value={item.status} /></td>
                  <td>{item.used_count || 0} / {item.max_uses || 1}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{item.used_by || "-"}</td>
                  <td>
                    <div className="row-actions">
                      <button className="btn icon-only" onClick={() => copyRedeemCode(item)} title={copiedCodeID === item.id ? "已复制" : "复制兑换码"} type="button"><Copy /></button>
                      <button className="btn danger icon-only" onClick={() => deleteRedeemCode(item.id)} title="删除兑换码" type="button"><Trash2 /></button>
                    </div>
                  </td>
                </tr>
              ))}
              {redeemCodes.length === 0 ? <tr><td colSpan={6} className="muted" style={{ textAlign: "center", padding: 18 }}>暂无兑换码</td></tr> : null}
            </tbody>
          </table>
        </div>
      </section>
    </AppShell>
  );
}

function EmptyPlanMobile() {
  return <div className="empty-state"><strong>暂无套餐</strong></div>;
}
