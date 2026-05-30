"use client";

import { useEffect, useMemo, useState } from "react";
import { Copy, Plus, RefreshCw, Save, Trash2 } from "lucide-react";
import { AppShell, PageHead, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import { formatQuota } from "@/lib/format";
import type { AccessPolicy, Channel, Plan, RedeemCode } from "@/types/api";

type PolicyDraft = {
  allowed_models: string;
  max_concurrency: string;
  hourly_limit: string;
  weekly_limit: string;
  monthly_limit: string;
};

type PlanDraft = {
  type: string;
  duration_days: string;
  public: boolean;
};

type ModelRatioDraft = {
  model: string;
  ratio: string;
};

const defaultMonthlyLimitForType = (type: string) => type === "count_based" ? 1000 : 100000;

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
  };
}

function monthlyQuotaLabel(type: string) {
  return type === "count_based" ? "每月次数" : "每月 Token";
}

function csvModels(value: string): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  value.split(/[\s,]+/).map((item) => item.trim()).filter(Boolean).forEach((item) => {
    if (seen.has(item)) return;
    seen.add(item);
    out.push(item);
  });
  return out;
}

function parseModelRatios(raw?: string): Record<string, number> {
  if (!raw) return {};
  try {
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    return Object.fromEntries(Object.entries(parsed).filter(([, value]) => typeof value === "number")) as Record<string, number>;
  } catch {
    return {};
  }
}

function modelRatioDraft(rawRatios: string, models: string[]): ModelRatioDraft[] {
  const ratios = parseModelRatios(rawRatios);
  models = [...new Set([...models, ...Object.keys(ratios)])];
  return models.map((model) => ({ model, ratio: String(ratios[model] ?? 1) }));
}

function modelRatiosJSON(rows: ModelRatioDraft[]): string {
  const out: Record<string, number> = {};
  for (const row of rows) {
    const model = row.model.trim();
    if (!model) continue;
    const ratio = Number(row.ratio || 0);
    if (!Number.isFinite(ratio) || ratio < 0) throw new Error("模型倍率必须是非负数字");
    if (!/^\d+$/.test(row.ratio.trim())) throw new Error("模型倍率必须是非负整数");
    out[model] = ratio;
  }
  return JSON.stringify(out);
}

function modelCSV(values: string[]): string {
  return csvModels(values.join(",")).join(",");
}

function preserveCursor(input: HTMLInputElement, update: (value: string) => void) {
  const start = input.selectionStart;
  const end = input.selectionEnd;
  update(input.value);
  window.requestAnimationFrame(() => {
    if (document.activeElement === input && start !== null && end !== null) {
      input.setSelectionRange(start, end);
    }
  });
}

function allPolicyChannelModels(channel: Channel): string[] {
  const aliases = parseAliases(channel.model_aliases || "");
  return modelCSV([
    ...csvModels(channel.models || ""),
    ...aliases.publicToUpstream.keys(),
    ...(channel.api_format === "antigravity" ? antigravityThinkingModels(channel.settings) : []),
  ]).split(",").filter(Boolean);
}

function parseAliases(raw: string) {
  const publicToUpstream = new Map<string, string>();
  for (const entry of raw.replace(/\r\n/g, "\n").replace(/;/g, "\n").split(/[\n,]/)) {
    const trimmed = entry.trim();
    if (!trimmed) continue;
    const sep = ["=>", "=", ":"].find((item) => trimmed.includes(item));
    if (!sep) continue;
    const [publicPart, ...rest] = trimmed.split(sep);
    const publicID = publicPart.trim();
    const upstream = rest.join(sep).trim();
    if (publicID && upstream && !publicToUpstream.has(publicID)) {
      publicToUpstream.set(publicID, upstream);
    }
  }
  return { publicToUpstream };
}

type AntigravityPlanTierGroup = { public_model: string; aliases: string[]; high: string; medium: string; low: string };

function antigravityTierGroups(raw?: string): AntigravityPlanTierGroup[] {
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    const groups = Array.isArray(parsed?.tier_groups) ? parsed.tier_groups : [];
    return groups.map((item: unknown): AntigravityPlanTierGroup => {
      const record = item && typeof item === "object" && !Array.isArray(item) ? item as Record<string, unknown> : {};
      return {
        public_model: String(record.public_model || "").trim(),
        aliases: Array.isArray(record.aliases) ? record.aliases.map((alias) => String(alias).trim()).filter(Boolean) : csvModels(String(record.aliases || "")),
        high: String(record.high || "").trim(),
        medium: String(record.medium || "").trim(),
        low: String(record.low || "").trim(),
      };
    }).filter((group: AntigravityPlanTierGroup) => group.public_model);
  } catch {
    return [];
  }
}

function antigravityThinkingModels(raw?: string): string[] {
  return antigravityTierGroups(raw).map((group) => group.public_model).filter(Boolean);
}

function policyFromDraft(id: string, draft: PolicyDraft, existing?: AccessPolicy): AccessPolicy {
  return {
    id,
    allowed_models: draft.allowed_models.trim(),
    max_concurrency: Number(draft.max_concurrency || 0),
    hourly_limit: Number(draft.hourly_limit || 0),
    weekly_limit: Number(draft.weekly_limit || 0),
    monthly_limit: Number(draft.monthly_limit || 0),
    enabled: true,
    created_at: existing?.created_at || new Date().toISOString(),
    updated_at: new Date().toISOString(),
  };
}

export default function AdminPlansPage() {
  const [plans, setPlans] = useState<Plan[]>([]);
  const [policies, setPolicies] = useState<AccessPolicy[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [saving, setSaving] = useState(false);
  const [draft, setDraft] = useState({ name: "", type: "count_based", duration_days: 30, enabled: true, public: false });
  const [policyDraft, setPolicyDraft] = useState<PolicyDraft>(emptyPolicyDraft());
  const [editingPolicies, setEditingPolicies] = useState<Record<string, PolicyDraft>>({});
  const [editingPlans, setEditingPlans] = useState<Record<string, PlanDraft>>({});
  const [modelRatios, setModelRatios] = useState("");
  const [editingModelRatios, setEditingModelRatios] = useState<ModelRatioDraft[]>([]);
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
      adminApi.channels(token, 1, 500).then((data) => data.items).catch(() => []),
      adminApi.settings(token).catch(() => undefined),
    ]).then(([planItems, policyItems, channelItems, settings]) => {
      setPlans(planItems);
      setPolicies(policyItems);
      setChannels(channelItems);
      const ratios = settings?.model_ratios || "{}";
      setModelRatios(ratios);
      setEditingModelRatios(modelRatioDraft(ratios, []));
      setLoading(false);
    });
  }, []);

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    adminApi.redeemCodes(token, 1, 50, redeemStatus).then((data) => data.items).catch(() => []).then((codeItems) => {
      setRedeemCodes(codeItems);
    });
  }, [redeemStatus]);

  const policyByID = useMemo(() => new Map(policies.map((policy) => [policy.id, policy])), [policies]);
  const policyModelIDs = useMemo(() => {
    const seen = new Set<string>();
    const out: string[] = [];
    for (const channel of channels) {
      for (const model of allPolicyChannelModels(channel)) {
        if (seen.has(model)) continue;
        seen.add(model);
        out.push(model);
      }
    }
    return out;
  }, [channels]);

  function openCreate() {
    setDraft({ name: "", type: "count_based", duration_days: 30, enabled: true, public: false });
    setPolicyDraft({ ...emptyPolicyDraft(), hourly_limit: "1000", weekly_limit: "1000", monthly_limit: "1000" });
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
      const createdPlan = await adminApi.createPlan(token, {
        name: draft.name.trim(),
        type: draft.type,
        duration_days: draft.duration_days,
        enabled: draft.enabled,
        public: draft.public,
        ...policyBody(policyDraft),
      });
      if (createdPlan.policy_id) {
        setPolicies((cur) => [policyFromDraft(createdPlan.policy_id!, policyDraft), ...cur]);
      }
      setPlans((cur) => [createdPlan, ...cur]);
      setCreating(false);
      setNotice(`套餐 ${createdPlan.name} 已创建。`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建失败");
    } finally {
      setSaving(false);
    }
  }

  async function savePlan(plan: Plan) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    const currentPolicy = plan.policy_id ? policyByID.get(plan.policy_id) : undefined;
    const currentDraft = editingPolicies[plan.id] || draftFromPolicy(currentPolicy);
    const currentMeta = planDraft(plan);
    const durationDays = Number(currentMeta.duration_days || 30);
    if (durationDays < 1) {
      setError("有效天数必须大于 0");
      return;
    }
    setSaving(true);
    setError("");
    setNotice("");
    try {
      const nextPlan = await adminApi.updatePlan(token, plan.id, {
        type: currentMeta.type,
        duration_days: durationDays,
        public: currentMeta.public,
        ...policyBody(currentDraft),
      });
      if (nextPlan.policy_id) {
        setPolicies((cur) => {
          const nextPolicy = policyFromDraft(nextPlan.policy_id!, currentDraft, currentPolicy);
          return cur.some((policy) => policy.id === nextPolicy.id)
            ? cur.map((policy) => policy.id === nextPolicy.id ? nextPolicy : policy)
            : [nextPolicy, ...cur];
        });
      }
      setPlans((cur) => cur.map((item) => item.id === plan.id ? nextPlan : item));
      setEditingPlans((cur) => {
        const next = { ...cur };
        delete next[plan.id];
        return next;
      });
      setEditingPolicies((cur) => {
        const next = { ...cur };
        delete next[plan.id];
        return next;
      });
      setNotice(`${plan.name} 已保存。`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }

  function planDraft(plan: Plan): PlanDraft {
    return editingPlans[plan.id] || {
      type: plan.type,
      duration_days: String(plan.duration_days || 30),
      public: Boolean(plan.public),
    };
  }

  function setPlanDraft(planID: string, next: PlanDraft) {
    setEditingPlans((cur) => ({ ...cur, [planID]: next }));
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

  function autofillRatioRows() {
    const current = parseModelRatios(modelRatios);
    setEditingModelRatios(policyModelIDs.map((model) => ({ model, ratio: String(current[model] ?? 1) })));
  }

  async function saveModelRatios() {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    setSaving(true);
    setError("");
    setNotice("");
    try {
      const nextRatios = modelRatiosJSON(editingModelRatios);
      const updated = await adminApi.updateSettings(token, { model_ratios: nextRatios });
      setModelRatios(updated.model_ratios || "{}");
      setEditingModelRatios(modelRatioDraft(updated.model_ratios || "{}", []));
      setNotice("全局模型倍率已保存。");
    } catch (err) {
      setError(err instanceof Error ? err.message : "保存倍率失败");
    } finally {
      setSaving(false);
    }
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
        action={
          <div className="row-actions">
            <button className="btn" onClick={() => document.getElementById("model-ratio-panel")?.scrollIntoView({ behavior: "smooth", block: "start" })} type="button">模型倍率</button>
            <button className="btn primary" onClick={openCreate}><Plus /> 新建套餐</button>
          </div>
        }
      />
      {creating ? (
        <div className="modal-backdrop plan-create-backdrop" role="presentation">
          <section aria-modal="true" className="modal plan-create-modal" role="dialog">
            <div className="modal-head">
              <div>
                <p className="eyebrow">New Plan</p>
                <h2>新建套餐</h2>
              </div>
              <button className="btn" disabled={saving} onClick={() => { setCreating(false); setError(""); }} type="button">关闭</button>
            </div>
            <div className="plan-create-basic">
              <div className="field">
                <label>套餐名称</label>
                <input className="input" value={draft.name} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} placeholder="Starter" />
              </div>
              <div className="field">
                <label>计费类型</label>
                <select className="input" value={draft.type} onChange={(e) => {
                  const type = e.target.value;
                  const monthly = String(defaultMonthlyLimitForType(type));
                  setDraft((d) => ({ ...d, type }));
                  setPolicyDraft((d) => ({ ...d, hourly_limit: monthly, weekly_limit: monthly, monthly_limit: monthly }));
                }}>
                  <option value="count_based">按次数</option>
                  <option value="token_based">按 Token</option>
                </select>
              </div>
              <div className="field">
                <label>有效天数</label>
                <input className="input" type="number" min={1} value={draft.duration_days} onChange={(e) => setDraft((d) => ({ ...d, duration_days: Number(e.target.value) }))} />
              </div>
              <label className="check-row">
                <input type="checkbox" checked={draft.public} onChange={(e) => setDraft((d) => ({ ...d, public: e.target.checked }))} />
                用户可见
              </label>
            </div>
            <div className="plan-create-policy">
              <div className="field plan-create-models">
                <label>允许模型</label>
                <input className="input" value={policyDraft.allowed_models} onChange={(e) => preserveCursor(e.currentTarget, (value) => setPolicyDraft((d) => ({ ...d, allowed_models: value })))} placeholder="留空不限制" />
                <button className="btn subtle" disabled={policyModelIDs.length === 0} onClick={() => setPolicyDraft((d) => ({ ...d, allowed_models: modelCSV(policyModelIDs) }))} type="button"><RefreshCw /> 填入全部</button>
              </div>
              <div className="field plan-create-monthly"><label>{monthlyQuotaLabel(draft.type)}</label><input className="input" min={0} type="number" value={policyDraft.monthly_limit} onChange={(e) => setPolicyDraft((d) => ({ ...d, monthly_limit: e.target.value }))} /></div>
              <div className="field"><label>最大并发</label><input className="input" min={0} type="number" value={policyDraft.max_concurrency} onChange={(e) => setPolicyDraft((d) => ({ ...d, max_concurrency: e.target.value }))} /></div>
              <div className="field"><label>每 5 小时窗口</label><input className="input" min={0} type="number" value={policyDraft.hourly_limit} onChange={(e) => setPolicyDraft((d) => ({ ...d, hourly_limit: e.target.value }))} /></div>
              <div className="field"><label>每周窗口</label><input className="input" min={0} type="number" value={policyDraft.weekly_limit} onChange={(e) => setPolicyDraft((d) => ({ ...d, weekly_limit: e.target.value }))} /></div>
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
      <section className="card card-pad plan-list-panel">
        <div className="plan-desktop-list">
          {plans.map((p) => {
            const draftRow = rowDraft(p);
            const metaDraft = planDraft(p);
            return (
              <article className="plan-row-card" key={p.id}>
                <div className="plan-row-head">
                  <div>
                    <strong>{p.name}</strong>
                    <span>{metaDraft.type === "count_based" ? "按次数" : "按 Token"} · {metaDraft.duration_days || 30} 天</span>
                  </div>
                  <div className="row-actions">
                    <button className="btn plan-status-button" onClick={() => togglePlan(p)} type="button">
                      <StatusBadge value={p.enabled ? "enabled" : "disabled"} />
                    </button>
                    <span className={`badge ${metaDraft.public ? "green" : ""}`}>{metaDraft.public ? "公开" : "隐藏"}</span>
                    <button className="btn danger icon-only" onClick={() => deletePlan(p.id)} title="删除套餐" type="button"><Trash2 /></button>
                  </div>
                </div>
                <div className="plan-row-grid">
                  <div className="plan-row-basic">
                    <div className="field compact-field">
                      <label>计费类型</label>
                      <select className="input" value={metaDraft.type} onChange={(e) => {
                        const type = e.target.value;
                        setPlanDraft(p.id, { ...metaDraft, type });
                      }}>
                        <option value="count_based">按次数</option>
                        <option value="token_based">按 Token</option>
                      </select>
                    </div>
                    <div className="field compact-field">
                      <label>有效天数</label>
                      <input className="input" inputMode="numeric" value={metaDraft.duration_days} onChange={(e) => preserveCursor(e.currentTarget, (value) => setPlanDraft(p.id, { ...metaDraft, duration_days: value }))} />
                    </div>
                    <label className="check-row plan-public-toggle">
                      <input type="checkbox" checked={metaDraft.public} onChange={(e) => setPlanDraft(p.id, { ...metaDraft, public: e.target.checked })} />
                      用户可见
                    </label>
                  </div>
                  <div className="plan-row-policy">
                    <div className="field compact-field plan-policy-models">
                      <label>允许模型</label>
                      <div className="plan-model-input-row">
                        <input className="input" value={draftRow.allowed_models} onChange={(e) => preserveCursor(e.currentTarget, (value) => setRowDraft(p.id, { ...draftRow, allowed_models: value }))} placeholder="不限制" />
                        <button className="btn subtle" disabled={policyModelIDs.length === 0} onClick={() => setRowDraft(p.id, { ...draftRow, allowed_models: modelCSV(policyModelIDs) })} type="button"><RefreshCw /> 填入全部</button>
                      </div>
                    </div>
                    <div className="field compact-field"><label>并发</label><input className="input" inputMode="numeric" value={draftRow.max_concurrency} onChange={(e) => preserveCursor(e.currentTarget, (value) => setRowDraft(p.id, { ...draftRow, max_concurrency: value }))} /></div>
                    <div className="field compact-field"><label>5 小时</label><input className="input" inputMode="numeric" value={draftRow.hourly_limit} onChange={(e) => preserveCursor(e.currentTarget, (value) => setRowDraft(p.id, { ...draftRow, hourly_limit: value }))} /></div>
                    <div className="field compact-field"><label>周</label><input className="input" inputMode="numeric" value={draftRow.weekly_limit} onChange={(e) => preserveCursor(e.currentTarget, (value) => setRowDraft(p.id, { ...draftRow, weekly_limit: value }))} /></div>
                    <div className="field compact-field"><label>{monthlyQuotaLabel(metaDraft.type)}</label><input className="input" inputMode="numeric" value={draftRow.monthly_limit} onChange={(e) => preserveCursor(e.currentTarget, (value) => setRowDraft(p.id, { ...draftRow, monthly_limit: value }))} /></div>
                    <button className="btn plan-policy-save" disabled={saving} onClick={() => savePlan(p)} title="保存套餐"><Save /> 保存</button>
                  </div>
                </div>
              </article>
            );
          })}
          {plans.length === 0 && !loading ? <div className="empty-state"><strong>暂无套餐</strong></div> : null}
        </div>
        <div className="plan-mobile-list">
          {plans.map((p) => {
            const draftRow = rowDraft(p);
            const metaDraft = planDraft(p);
            return (
              <article className="plan-mobile-card" key={`mobile-${p.id}`}>
                <div className="plan-mobile-head">
                  <div>
                    <strong>{p.name}</strong>
                    <span>{metaDraft.type === "count_based" ? "按次数" : "按 Token"} · {monthlyQuotaLabel(metaDraft.type)} {formatQuota(Number(draftRow.monthly_limit || 0))} · {metaDraft.duration_days || 30} 天</span>
                  </div>
                  <StatusBadge value={p.enabled ? "enabled" : "disabled"} />
                </div>
                <div className="plan-mobile-policy">
                  <div className="field compact-field">
                    <label>计费类型</label>
                    <select className="input" value={metaDraft.type} onChange={(e) => {
                      const type = e.target.value;
                      setPlanDraft(p.id, { ...metaDraft, type });
                    }}>
                      <option value="count_based">按次数</option>
                      <option value="token_based">按 Token</option>
                    </select>
                  </div>
                  <div className="field compact-field"><label>有效天数</label><input className="input" inputMode="numeric" value={metaDraft.duration_days} onChange={(e) => preserveCursor(e.currentTarget, (value) => setPlanDraft(p.id, { ...metaDraft, duration_days: value }))} /></div>
                  <label className="check-row">
                    <input type="checkbox" checked={metaDraft.public} onChange={(e) => setPlanDraft(p.id, { ...metaDraft, public: e.target.checked })} />
                    用户可见
                  </label>
                  <div className="field compact-field">
                    <label>允许模型</label>
                    <input className="input" value={draftRow.allowed_models} onChange={(e) => preserveCursor(e.currentTarget, (value) => setRowDraft(p.id, { ...draftRow, allowed_models: value }))} placeholder="不限制" />
                    <button className="btn subtle" disabled={policyModelIDs.length === 0} onClick={() => setRowDraft(p.id, { ...draftRow, allowed_models: modelCSV(policyModelIDs) })} type="button"><RefreshCw /> 填入全部</button>
                  </div>
                  <div className="field compact-field"><label>并发</label><input className="input" inputMode="numeric" value={draftRow.max_concurrency} onChange={(e) => preserveCursor(e.currentTarget, (value) => setRowDraft(p.id, { ...draftRow, max_concurrency: value }))} /></div>
                  <div className="field compact-field"><label>5 小时</label><input className="input" inputMode="numeric" value={draftRow.hourly_limit} onChange={(e) => preserveCursor(e.currentTarget, (value) => setRowDraft(p.id, { ...draftRow, hourly_limit: value }))} /></div>
                  <div className="field compact-field"><label>周</label><input className="input" inputMode="numeric" value={draftRow.weekly_limit} onChange={(e) => preserveCursor(e.currentTarget, (value) => setRowDraft(p.id, { ...draftRow, weekly_limit: value }))} /></div>
                  <div className="field compact-field"><label>{monthlyQuotaLabel(metaDraft.type)}</label><input className="input" inputMode="numeric" value={draftRow.monthly_limit} onChange={(e) => preserveCursor(e.currentTarget, (value) => setRowDraft(p.id, { ...draftRow, monthly_limit: value }))} /></div>
                </div>
                <div className="plan-mobile-actions">
                  <button className="btn" disabled={saving} onClick={() => savePlan(p)} type="button"><Save /> 保存</button>
                  <button className="btn" onClick={() => togglePlan(p)} type="button">{p.enabled ? "停用" : "启用"}</button>
                  <button className="btn danger icon-only" onClick={() => deletePlan(p.id)} title="删除套餐" type="button"><Trash2 /></button>
                </div>
              </article>
            );
          })}
          {plans.length === 0 && !loading ? <EmptyPlanMobile /> : null}
        </div>
      </section>

      <section className="card card-pad model-ratio-panel" id="model-ratio-panel" style={{ marginTop: 16 }}>
        <div className="section-head">
          <div>
            <h2>模型倍率</h2>
            <p className="muted">全局模型消耗倍率，所有套餐共用，倍率为非负整数。</p>
          </div>
          <div className="row-actions">
            <button className="btn" disabled={policyModelIDs.length === 0} onClick={autofillRatioRows} type="button"><RefreshCw /> 自动填入</button>
            <button className="btn icon-only" onClick={() => setEditingModelRatios((rows) => [...rows, { model: "", ratio: "1" }])} title="新增模型倍率" type="button"><Plus /></button>
          </div>
        </div>
        <div className="model-ratio-editor standalone">
          {editingModelRatios.map((row, index) => (
            <div className="model-ratio-row" key={`global-ratio-${index}`}>
              <div className="field compact-field model-ratio-model-field">
                <label>模型</label>
                <input aria-label="模型" className="input" value={row.model} onChange={(e) => preserveCursor(e.currentTarget, (value) => {
                  const next = [...editingModelRatios];
                  next[index] = { ...row, model: value };
                  setEditingModelRatios(next);
                })} placeholder="模型 ID" />
              </div>
              <div className="field compact-field model-ratio-value-field">
                <label>倍率</label>
                <input aria-label="倍率" className="input model-ratio-input" min={0} placeholder="1" step={1} type="number" value={row.ratio} onChange={(e) => {
                  const next = [...editingModelRatios];
                  next[index] = { ...row, ratio: e.target.value };
                  setEditingModelRatios(next);
                }} />
              </div>
              <button className="btn danger icon-only" onClick={() => setEditingModelRatios((rows) => rows.filter((_, i) => i !== index))} title="删除倍率" type="button"><Trash2 /></button>
            </div>
          ))}
          {editingModelRatios.length === 0 ? <div className="empty-state"><strong>暂无模型倍率</strong></div> : null}
        </div>
        <div className="form-actions">
          <button className="btn" disabled={saving} onClick={saveModelRatios} type="button"><Save /> 保存倍率</button>
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
