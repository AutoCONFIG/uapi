"use client";

import { useEffect, useState } from "react";
import { Plus, Trash2 } from "lucide-react";
import { AppShell, PageHead, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { AccessPolicy } from "@/types/api";

export default function AccessPoliciesPage() {
  const [policies, setPolicies] = useState<AccessPolicy[]>([]);
  const [loading, setLoading] = useState(true);
  const [creating, setCreating] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [form, setForm] = useState({
    name: "",
    allowed_models: "",
    max_concurrency: "0",
    hourly_limit: "0",
    weekly_limit: "0",
    monthly_limit: "0",
  });

  useEffect(() => { loadPolicies(); }, []);

  function loadPolicies() {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) { setLoading(false); return; }
    setLoading(true);
    adminApi.accessPolicies(token, 1, 100)
      .then((data) => setPolicies(data.items))
      .catch(() => {})
      .finally(() => setLoading(false));
  }

  async function createPolicy() {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    setSaving(true);
    setError("");
    try {
      const created = await adminApi.createAccessPolicy(token, {
        name: form.name.trim(),
        allowed_models: form.allowed_models.trim(),
        max_concurrency: Number(form.max_concurrency || 0),
        hourly_limit: Number(form.hourly_limit || 0),
        weekly_limit: Number(form.weekly_limit || 0),
        monthly_limit: Number(form.monthly_limit || 0),
        enabled: true,
      });
      setPolicies((cur) => [created, ...cur]);
      setCreating(false);
      setForm({ name: "", allowed_models: "", max_concurrency: "0", hourly_limit: "0", weekly_limit: "0", monthly_limit: "0" });
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建失败");
    } finally {
      setSaving(false);
    }
  }

  async function patchPolicy(id: string, body: Partial<AccessPolicy>) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    try {
      const updated = await adminApi.updateAccessPolicy(token, id, body);
      setPolicies((cur) => cur.map((policy) => policy.id === id ? updated : policy));
    } catch { /* ignore */ }
  }

  async function deletePolicy(id: string) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    try {
      await adminApi.deleteAccessPolicy(token, id);
      setPolicies((cur) => cur.filter((policy) => policy.id !== id));
    } catch { /* ignore */ }
  }

  return (
    <AppShell title="访问策略" variant="admin">
      <PageHead
        eyebrow="Admin / Access Policies"
        title="访问策略"
        description="按 API Key 绑定策略，限制可用模型、固定窗口请求数和最大并发。"
        action={<button className="btn primary" onClick={() => setCreating(true)}><Plus /> 新增策略</button>}
      />

      {creating ? (
        <div className="modal-backdrop" role="presentation">
          <section aria-modal="true" className="modal" role="dialog">
            <div className="modal-head">
              <div>
                <p className="eyebrow">New Policy</p>
                <h2>新增策略</h2>
              </div>
              <button className="btn" onClick={() => { setCreating(false); setError(""); }} type="button">关闭</button>
            </div>
            <div className="grid grid-2" style={{ marginTop: 12 }}>
              <div className="field"><label>名称</label><input className="input" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="standard-user" /></div>
              <div className="field"><label>允许模型</label><input className="input" value={form.allowed_models} onChange={(e) => setForm({ ...form, allowed_models: e.target.value })} placeholder="gpt-4o,gpt-4.1" /></div>
              <div className="field"><label>最大并发</label><input className="input" type="number" value={form.max_concurrency} onChange={(e) => setForm({ ...form, max_concurrency: e.target.value })} /></div>
              <div className="field"><label>每小时请求</label><input className="input" type="number" value={form.hourly_limit} onChange={(e) => setForm({ ...form, hourly_limit: e.target.value })} /></div>
              <div className="field"><label>每周请求</label><input className="input" type="number" value={form.weekly_limit} onChange={(e) => setForm({ ...form, weekly_limit: e.target.value })} /></div>
              <div className="field"><label>每月请求</label><input className="input" type="number" value={form.monthly_limit} onChange={(e) => setForm({ ...form, monthly_limit: e.target.value })} /></div>
            </div>
            {error && <p className="form-error">{error}</p>}
            <div className="form-actions">
              <button className="btn" onClick={() => { setCreating(false); setError(""); }}>取消</button>
              <button className="btn primary" onClick={createPolicy} disabled={saving}>确认创建</button>
            </div>
          </section>
        </div>
      ) : null}

      <section className="card">
        <div className="table-wrap">
          <table>
            <thead><tr><th>名称</th><th>模型</th><th>并发</th><th>小时/周/月</th><th>状态</th><th></th></tr></thead>
            <tbody>
              {policies.map((policy) => (
                <tr key={policy.id}>
                  <td>{policy.name}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{policy.allowed_models || "不限制"}</td>
                  <td>{policy.max_concurrency || "不限制"}</td>
                  <td>{policy.hourly_limit || "-"} / {policy.weekly_limit || "-"} / {policy.monthly_limit || "-"}</td>
                  <td>
                    <button className="btn" style={{ padding: "2px 8px" }} onClick={() => patchPolicy(policy.id, { enabled: !policy.enabled })}>
                      <StatusBadge value={policy.enabled ? "enabled" : "disabled"} />
                    </button>
                  </td>
                  <td><button className="btn" style={{ padding: "2px 8px" }} onClick={() => deletePolicy(policy.id)}><Trash2 size={14} /></button></td>
                </tr>
              ))}
              {policies.length === 0 && !loading && (
                <tr><td colSpan={6} className="muted" style={{ textAlign: "center", padding: 24 }}>暂无访问策略</td></tr>
              )}
            </tbody>
          </table>
        </div>
      </section>
    </AppShell>
  );
}
