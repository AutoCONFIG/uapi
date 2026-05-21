"use client";

import { useEffect, useState } from "react";
import { RefreshCw, Plus, Trash2 } from "lucide-react";
import { AppShell, EmptyState, PageHead, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { AccessPolicy, ApiKey } from "@/types/api";

export default function TokensPage() {
  const [tokens, setTokens] = useState<ApiKey[]>([]);
  const [policies, setPolicies] = useState<AccessPolicy[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [createOpen, setCreateOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [newName, setNewName] = useState("");
  const [newKey, setNewKey] = useState("");
  const [newPolicyID, setNewPolicyID] = useState("");
  const [error, setError] = useState("");

  function generateTokenKey() {
    const bytes = new Uint8Array(18);
    window.crypto.getRandomValues(bytes);
    const suffix = Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
    return `sk-relay-${suffix}`;
  }

  function openCreate() {
    setNewName(`Platform Key ${tokens.length + 1}`);
    setNewKey(generateTokenKey());
    setNewPolicyID("");
    setError("");
    setCreateOpen(true);
  }

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) { setLoading(false); return; }
    adminApi.tokens(token)
      .then((data) => { setTokens(data.items); setTotal(data.total); })
      .catch(() => {})
      .finally(() => setLoading(false));
    adminApi.accessPolicies(token, 1, 100)
      .then((data) => setPolicies(data.items))
      .catch(() => {});
  }, []);

  async function createToken() {
    const name = newName.trim() || `Platform Key ${tokens.length + 1}`;
    const key = newKey.trim() || generateTokenKey();
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    setSaving(true);
    setError("");
    try {
      const created = await adminApi.createToken(token, {
        name,
        key,
        enabled: true,
        policy_id: newPolicyID || undefined,
      });
      setTokens((cur) => [created, ...cur]);
      setTotal((cur) => cur + 1);
      setNewName("");
      setNewKey("");
      setNewPolicyID("");
      setCreateOpen(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建失败");
    } finally {
      setSaving(false);
    }
  }

  function closeCreate() {
    if (saving) return;
    setCreateOpen(false);
    setNewName("");
    setNewKey("");
    setNewPolicyID("");
    setError("");
  }

  async function deleteToken(id: string) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    try {
      await adminApi.deleteToken(token, id);
      setTokens((cur) => cur.filter((t) => t.id !== id));
      setTotal((cur) => cur - 1);
    } catch { /* ignore */ }
  }

  return (
    <AppShell title="令牌管理" variant="admin">
      <PageHead
        eyebrow="Admin / Tokens"
        title="平台 API Key"
        description="管理员视角查看所有用户密钥、启停状态和创建时间。"
        action={
          <button className="btn primary" onClick={openCreate}><Plus /> 创建令牌</button>
        }
      />
      {createOpen ? (
        <div className="modal-backdrop" role="presentation">
          <section aria-modal="true" className="modal" role="dialog">
            <div className="modal-head">
              <div>
                <p className="eyebrow">New Token</p>
                <h2>新建令牌</h2>
              </div>
              <button className="btn" disabled={saving} onClick={closeCreate} type="button">关闭</button>
            </div>
            <div className="grid grid-2" style={{ marginTop: 12 }}>
              <div className="field">
                <label>名称</label>
                <input className="input" value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="Production gateway" />
              </div>
              <div className="field">
                <label>Key</label>
                <div className="input-row">
                  <input className="input" value={newKey} onChange={(e) => setNewKey(e.target.value)} placeholder="sk-..." />
                  <button className="btn" onClick={() => setNewKey(generateTokenKey())} title="重新生成" type="button"><RefreshCw /></button>
                </div>
              </div>
              <div className="field">
                <label>访问策略</label>
                <select className="input" value={newPolicyID} onChange={(e) => setNewPolicyID(e.target.value)}>
                  <option value="">不绑定</option>
                  {policies.map((policy) => (
                    <option value={policy.id} key={policy.id}>{policy.name}</option>
                  ))}
                </select>
              </div>
            </div>
            {error && <p className="form-error">{error}</p>}
            <div className="modal-actions">
              <button className="btn" disabled={saving} onClick={closeCreate} type="button">取消</button>
              <button className="btn primary" disabled={saving} onClick={createToken} type="button">{saving ? "创建中" : "确认创建"}</button>
            </div>
          </section>
        </div>
      ) : null}
      <section className="card">
        <div className="table-wrap">
          <table>
            <thead><tr><th>名称</th><th>Key</th><th>状态</th><th>策略</th><th>模型</th><th>创建时间</th><th></th></tr></thead>
            <tbody>
              {tokens.map((t) => (
                <tr key={t.id}>
                  <td>{t.name}</td>
                  <td><code>{t.key.slice(0, 12)}...</code></td>
                  <td><StatusBadge value={t.enabled ? "enabled" : "disabled"} /></td>
                  <td className="muted" style={{ fontSize: 12 }}>{policies.find((p) => p.id === t.policy_id)?.name || "—"}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{t.models || "—"}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{new Date(t.created_at).toLocaleDateString()}</td>
                  <td><button className="btn" style={{ padding: "2px 8px" }} onClick={() => deleteToken(t.id)}><Trash2 size={14} /></button></td>
                </tr>
              ))}
              {tokens.length === 0 && !loading && (
                <tr><td colSpan={7}><EmptyState title="暂无令牌" description="创建平台 API Key 后，可在这里统一查看、绑定策略和管理状态。" /></td></tr>
              )}
            </tbody>
          </table>
        </div>
      </section>
    </AppShell>
  );
}
