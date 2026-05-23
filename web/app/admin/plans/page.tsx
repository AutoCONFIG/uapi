"use client";

import { useEffect, useMemo, useState } from "react";
import { Plus, Save, Trash2 } from "lucide-react";
import { AppShell, PageHead, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { AccessPolicy, Plan } from "@/types/api";

type PolicyDraft = {
  name: string;
  allowed_models: string;
  max_concurrency: string;
  hourly_limit: string;
  weekly_limit: string;
  monthly_limit: string;
};

const emptyPolicyDraft = (name = ""): PolicyDraft => ({
  name,
  allowed_models: "",
  max_concurrency: "0",
  hourly_limit: "0",
  weekly_limit: "0",
  monthly_limit: "0",
});

function draftFromPolicy(policy?: AccessPolicy, fallbackName = ""): PolicyDraft {
  if (!policy) return emptyPolicyDraft(fallbackName);
  return {
    name: policy.name,
    allowed_models: policy.allowed_models || "",
    max_concurrency: String(policy.max_concurrency || 0),
    hourly_limit: String(policy.hourly_limit || 0),
    weekly_limit: String(policy.weekly_limit || 0),
    monthly_limit: String(policy.monthly_limit || 0),
  };
}

function policyBody(draft: PolicyDraft) {
  return {
    name: draft.name.trim(),
    allowed_models: draft.allowed_models.trim(),
    max_concurrency: Number(draft.max_concurrency || 0),
    hourly_limit: Number(draft.hourly_limit || 0),
    weekly_limit: Number(draft.weekly_limit || 0),
    monthly_limit: Number(draft.monthly_limit || 0),
    enabled: true,
  };
}

function formatQuota(value: number) {
  if (!value) return "不限制";
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return String(value);
}

export default function AdminPlansPage() {
  const [plans, setPlans] = useState<Plan[]>([]);
  const [policies, setPolicies] = useState<AccessPolicy[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [saving, setSaving] = useState(false);
  const [draft, setDraft] = useState({ name: "", type: "count_based", token_quota: 0, enabled: true });
  const [policyDraft, setPolicyDraft] = useState<PolicyDraft>(emptyPolicyDraft());
  const [editingPolicies, setEditingPolicies] = useState<Record<string, PolicyDraft>>({});
  const [error, setError] = useState("");

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) { setLoading(false); return; }
    Promise.all([
      adminApi.plans(token, 1, 100).then((data) => data.items).catch(() => []),
      adminApi.accessPolicies(token, 1, 100).then((data) => data.items).catch(() => []),
    ]).then(([planItems, policyItems]) => {
      setPlans(planItems);
      setPolicies(policyItems);
      setLoading(false);
    });
  }, []);

  const policyByID = useMemo(() => new Map(policies.map((policy) => [policy.id, policy])), [policies]);

  function openCreate() {
    setDraft({ name: "", type: "count_based", token_quota: 0, enabled: true });
    setPolicyDraft(emptyPolicyDraft());
    setError("");
    setCreating(true);
  }

  async function createPlan() {
    if (!draft.name.trim()) return;
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    setSaving(true);
    setError("");
    try {
      const policyName = policyDraft.name.trim() || `${draft.name.trim()} 限制策略`;
      const createdPolicy = await adminApi.createAccessPolicy(token, policyBody({ ...policyDraft, name: policyName }));
      const createdPlan = await adminApi.createPlan(token, {
        name: draft.name.trim(),
        type: draft.type,
        policy_id: createdPolicy.id,
        token_quota: draft.token_quota,
        limits: "{}",
        model_ratios: "{}",
        completion_ratio: "{}",
        enabled: draft.enabled,
      });
      setPolicies((cur) => [createdPolicy, ...cur]);
      setPlans((cur) => [createdPlan, ...cur]);
      setCreating(false);
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
    const currentDraft = editingPolicies[plan.id] || draftFromPolicy(currentPolicy, `${plan.name} 限制策略`);
    setSaving(true);
    setError("");
    try {
      if (currentPolicy) {
        const updated = await adminApi.updateAccessPolicy(token, currentPolicy.id, policyBody(currentDraft));
        setPolicies((cur) => cur.map((policy) => policy.id === updated.id ? updated : policy));
      } else {
        const created = await adminApi.createAccessPolicy(token, policyBody(currentDraft));
        const updatedPlan = await adminApi.updatePlan(token, plan.id, { policy_id: created.id });
        setPolicies((cur) => [created, ...cur]);
        setPlans((cur) => cur.map((item) => item.id === plan.id ? updatedPlan : item));
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }

  async function deletePlan(id: string) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    try {
      await adminApi.deletePlan(token, id);
      setPlans((cur) => cur.filter((p) => p.id !== id));
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
    return editingPolicies[plan.id] || draftFromPolicy(policy, `${plan.name} 限制策略`);
  }

  function setRowDraft(planID: string, next: PolicyDraft) {
    setEditingPolicies((cur) => ({ ...cur, [planID]: next }));
  }

  return (
    <AppShell title="套餐管理" variant="admin">
      <PageHead
        eyebrow="Admin / Plans"
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
                <select className="input" value={draft.type} onChange={(e) => setDraft((d) => ({ ...d, type: e.target.value }))}>
                  <option value="count_based">按次数</option>
                  <option value="token_based">按 Token</option>
                </select>
              </div>
              <div className="field">
                <label>Token 额度</label>
                <input className="input" type="number" value={draft.token_quota} onChange={(e) => setDraft((d) => ({ ...d, token_quota: Number(e.target.value) }))} />
              </div>
              <div className="field">
                <label>策略名称</label>
                <input className="input" value={policyDraft.name} onChange={(e) => setPolicyDraft((d) => ({ ...d, name: e.target.value }))} placeholder="留空使用套餐名称" />
              </div>
            </div>
            <div className="grid grid-2" style={{ marginTop: 8 }}>
              <div className="field"><label>允许模型</label><input className="input" value={policyDraft.allowed_models} onChange={(e) => setPolicyDraft((d) => ({ ...d, allowed_models: e.target.value }))} placeholder="留空不限制" /></div>
              <div className="field"><label>最大并发</label><input className="input" type="number" value={policyDraft.max_concurrency} onChange={(e) => setPolicyDraft((d) => ({ ...d, max_concurrency: e.target.value }))} /></div>
              <div className="field"><label>每小时请求</label><input className="input" type="number" value={policyDraft.hourly_limit} onChange={(e) => setPolicyDraft((d) => ({ ...d, hourly_limit: e.target.value }))} /></div>
              <div className="field"><label>每周请求</label><input className="input" type="number" value={policyDraft.weekly_limit} onChange={(e) => setPolicyDraft((d) => ({ ...d, weekly_limit: e.target.value }))} /></div>
              <div className="field"><label>每月请求</label><input className="input" type="number" value={policyDraft.monthly_limit} onChange={(e) => setPolicyDraft((d) => ({ ...d, monthly_limit: e.target.value }))} /></div>
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
      <section className="card">
        <div className="table-wrap">
          <table>
            <thead><tr><th>套餐</th><th>额度</th><th>限制策略</th><th>状态</th><th></th></tr></thead>
            <tbody>
              {plans.map((p) => {
                const draftRow = rowDraft(p);
                const policy = p.policy_id ? policyByID.get(p.policy_id) : undefined;
                return (
                  <tr key={p.id}>
                    <td><strong>{p.name}</strong><div className="muted" style={{ fontSize: 12 }}>{p.type === "count_based" ? "按次数" : "按 Token"}</div></td>
                    <td>{formatQuota(p.token_quota)}</td>
                    <td style={{ minWidth: 520 }}>
                      <div className="grid grid-2">
                        <div className="field"><label>策略名称</label><input className="input" value={draftRow.name} onChange={(e) => setRowDraft(p.id, { ...draftRow, name: e.target.value })} /></div>
                        <div className="field"><label>允许模型</label><input className="input" value={draftRow.allowed_models} onChange={(e) => setRowDraft(p.id, { ...draftRow, allowed_models: e.target.value })} placeholder="不限制" /></div>
                        <div className="field"><label>最大并发</label><input className="input" type="number" value={draftRow.max_concurrency} onChange={(e) => setRowDraft(p.id, { ...draftRow, max_concurrency: e.target.value })} /></div>
                        <div className="field"><label>小时/周/月</label><div className="input-row"><input className="input" type="number" value={draftRow.hourly_limit} onChange={(e) => setRowDraft(p.id, { ...draftRow, hourly_limit: e.target.value })} /><input className="input" type="number" value={draftRow.weekly_limit} onChange={(e) => setRowDraft(p.id, { ...draftRow, weekly_limit: e.target.value })} /><input className="input" type="number" value={draftRow.monthly_limit} onChange={(e) => setRowDraft(p.id, { ...draftRow, monthly_limit: e.target.value })} /></div></div>
                      </div>
                      <button className="btn" disabled={saving} onClick={() => savePolicy(p)} style={{ marginTop: 8 }}><Save /> {policy ? "保存策略" : "创建并绑定策略"}</button>
                    </td>
                    <td>
                      <button className="btn" style={{ padding: "2px 8px" }} onClick={() => togglePlan(p)}>
                        <StatusBadge value={p.enabled ? "enabled" : "disabled"} />
                      </button>
                    </td>
                    <td><button className="btn" style={{ padding: "2px 8px" }} onClick={() => deletePlan(p.id)}><Trash2 size={14} /></button></td>
                  </tr>
                );
              })}
              {plans.length === 0 && !loading && (
                <tr><td colSpan={5} className="muted" style={{ textAlign: "center", padding: 24 }}>{loading ? "加载中…" : "暂无套餐"}</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </section>
    </AppShell>
  );
}
