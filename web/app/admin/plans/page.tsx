"use client";

import { useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { AppShell, PageHead, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { Plan } from "@/types/api";

export default function AdminPlansPage() {
  const [plans, setPlans] = useState<Plan[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [draft, setDraft] = useState({ name: "", type: "count_based", token_quota: 0, enabled: true });
  const [error, setError] = useState("");

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) { setLoading(false); return; }
    adminApi.plans(token)
      .then((data) => setPlans(data.items))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  async function createPlan() {
    if (!draft.name.trim()) return;
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    setError("");
    try {
      const created = await adminApi.createPlan(token, {
        name: draft.name,
        type: draft.type,
        token_quota: draft.token_quota,
        enabled: draft.enabled,
      });
      setPlans((cur) => [created, ...cur]);
      setCreating(false);
      setDraft({ name: "", type: "count_based", token_quota: 0, enabled: true });
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建失败");
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

  return (
    <AppShell title="套餐管理" variant="admin">
      <PageHead
        eyebrow="Admin / Plans"
        title="套餐和限额"
        description="配置 token quota、计费类型和启用状态。"
        action={
          <button className="btn primary" onClick={() => setCreating(true)}><Plus /> 新建套餐</button>
        }
      />
      {creating ? (
        <div className="modal-backdrop" role="presentation">
          <section aria-modal="true" className="modal" role="dialog">
            <div className="modal-head">
              <div>
                <p className="eyebrow">New Plan</p>
                <h2>新建套餐</h2>
              </div>
              <button className="btn" onClick={() => { setCreating(false); setError(""); }} type="button">关闭</button>
            </div>
            <div className="grid grid-2" style={{ marginTop: 12 }}>
              <div className="field">
                <label>名称</label>
                <input className="input" value={draft.name} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} placeholder="Starter" />
              </div>
              <div className="field">
                <label>计费类型</label>
                <select className="input" value={draft.type} onChange={(e) => setDraft((d) => ({ ...d, type: e.target.value }))}>
                  <option value="count_based">按次数</option>
                  <option value="token_based">按 Token</option>
                </select>
              </div>
            </div>
            <div className="field" style={{ marginTop: 8 }}>
              <label>Token 额度</label>
              <input className="input" type="number" value={draft.token_quota} onChange={(e) => setDraft((d) => ({ ...d, token_quota: Number(e.target.value) }))} />
            </div>
            {error && <p className="form-error">{error}</p>}
            <div className="form-actions">
              <button className="btn" onClick={() => { setCreating(false); setError(""); }}>取消</button>
              <button className="btn primary" onClick={createPlan}>确认创建</button>
            </div>
          </section>
        </div>
      ) : null}
      <section className="card">
        <div className="table-wrap">
          <table>
            <thead><tr><th>名称</th><th>类型</th><th>额度</th><th>状态</th><th></th></tr></thead>
            <tbody>
              {plans.map((p) => (
                <tr key={p.id}>
                  <td>{p.name}</td>
                  <td>{p.type}</td>
                  <td>{p.token_quota > 0 ? `${(p.token_quota / 1_000_000).toFixed(1)}M` : "—"}</td>
                  <td>
                    <button className="btn" style={{ padding: "2px 8px" }} onClick={() => togglePlan(p)}>
                      <StatusBadge value={p.enabled ? "enabled" : "disabled"} />
                    </button>
                  </td>
                  <td><button className="btn" style={{ padding: "2px 8px" }} onClick={() => deletePlan(p.id)}><Trash2 size={14} /></button></td>
                </tr>
              ))}
              {plans.length === 0 && !loading && (
                <tr><td colSpan={5} className="muted" style={{ textAlign: "center", padding: 24 }}>
                  {loading ? "加载中…" : "暂无套餐"}
                </td></tr>
              )}
            </tbody>
          </table>
        </div>
      </section>
    </AppShell>
  );
}
