"use client";

import { useEffect, useMemo, useState } from "react";
import { Copy, Plus, SlidersHorizontal, Trash2 } from "lucide-react";
import { AppShell, EmptyState, PageHead, StatusBadge } from "@/components/shell";
import { userApi } from "@/lib/api";
import type { ApiKey } from "@/types/api";

type DisplayKey = {
  id?: string;
  name: string;
  key: string;
  status: string;
  created: string;
  ipWhitelist: string;
  expiresAt?: string;
  models: string;
  permissions: string;
};

const permissionOptions = [
  { value: "chat", label: "Chat" },
  { value: "responses", label: "Responses" },
  { value: "messages", label: "Messages" },
  { value: "gemini", label: "Gemini" },
];

function fromApiKey(key: ApiKey): DisplayKey {
  return {
    id: key.id,
    name: key.name,
    key: key.key,
    status: key.enabled ? "Enabled" : "Paused",
    created: key.created_at ? new Date(key.created_at).toLocaleDateString() : "-",
    ipWhitelist: key.ip_whitelist,
    expiresAt: key.expires_at,
    models: key.models,
    permissions: key.permissions,
  };
}

export default function KeysPage() {
  const [items, setItems] = useState<DisplayKey[]>([]);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [ipWhitelist, setIPWhitelist] = useState("");
  const [expiresAt, setExpiresAt] = useState("");
  const [models, setModels] = useState("");
  const [permissions, setPermissions] = useState<string[]>(["chat", "responses"]);
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.user.token");
    if (!token) return;
    userApi.keys(token)
      .then((data) => setItems(data.map(fromApiKey)))
      .catch(() => {});
  }, []);

  const permissionValue = useMemo(() => permissions.join(","), [permissions]);

  function togglePermission(value: string) {
    setPermissions((current) => current.includes(value) ? current.filter((item) => item !== value) : [...current, value]);
  }

  async function createKey() {
    const token = window.localStorage.getItem("uapi.user.token");
    const trimmedName = name.trim() || `API Key ${items.length + 1}`;
    if (!token) return;
    setLoading(true);
    setError("");

    try {
      const created = await userApi.createKey(token, {
        name: trimmedName,
        ip_whitelist: ipWhitelist,
        expires_at: expiresAt ? new Date(expiresAt).toISOString() : undefined,
        models,
        permissions: permissionValue,
      });
      setItems((current) => [fromApiKey(created), ...current]);
      setOpen(false);
      setName("");
      setIPWhitelist("");
      setExpiresAt("");
      setModels("");
      setPermissions(["chat", "responses"]);
      setAdvancedOpen(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建密钥失败");
    } finally {
      setLoading(false);
    }
  }

  function closeCreate() {
    if (loading) return;
    setOpen(false);
    setName("");
    setIPWhitelist("");
    setExpiresAt("");
    setModels("");
    setPermissions(["chat", "responses"]);
    setAdvancedOpen(false);
    setError("");
  }

  async function deleteKey(row: DisplayKey) {
    const token = window.localStorage.getItem("uapi.user.token");
    if (token && row.id) {
      try {
        await userApi.deleteKey(token, row.id);
        setItems((current) => current.filter((item) => item.id !== row.id));
      } catch { /* leave row visible */ }
    }
  }

  return (
    <AppShell title="API Keys">
      <PageHead
        eyebrow="Credentials"
        title="API Key 管理"
        description="为生产、测试和自动化任务拆分密钥，减少泄露后的影响面。"
        action={<button className="btn primary" onClick={() => setOpen(true)} type="button"><Plus /> 新建密钥</button>}
      />

      <section className="surface">
        <div className="stat-bar">
          <span className="stat-chip"><span>全部密钥</span><strong>{items.length}</strong></span>
          <span className="stat-chip"><span>启用中</span><strong>{items.filter((item) => item.status === "Enabled").length}</strong></span>
          <span className="stat-chip"><span>受限密钥</span><strong>{items.filter((item) => item.models || item.ipWhitelist || item.expiresAt).length}</strong></span>
        </div>

        <div className="key-list">
          {items.length > 0 ? items.map((key) => (
            <article className="key-row" key={key.id || key.key}>
              <div>
                <strong>{key.name}</strong>
                <div className="key-meta"><span>创建 {key.created}</span><StatusBadge value={key.status} /></div>
              </div>
              <code>{key.key}</code>
              <div className="key-meta"><span>权限</span><strong>{key.permissions || "All"}</strong></div>
              <div className="key-meta"><span>模型</span><strong>{key.models || "All"}</strong><span>{key.expiresAt ? new Date(key.expiresAt).toLocaleDateString() : "Never"}</span></div>
              <div className="row-actions">
                <button className="btn icon-only" onClick={() => navigator.clipboard?.writeText(key.key)} title="复制" type="button"><Copy /></button>
                <button className="btn danger icon-only" onClick={() => deleteKey(key)} title="删除" type="button"><Trash2 /></button>
              </div>
            </article>
          )) : (
            <section className="card"><EmptyState title="暂无密钥" description="创建一个 API Key 后即可开始调用。默认配置已适合大多数场景。" action={<button className="btn primary" onClick={() => setOpen(true)} type="button"><Plus /> 新建密钥</button>} /></section>
          )}
        </div>
      </section>

      {open ? (
        <div className="modal-backdrop" role="presentation">
          <section aria-modal="true" className="modal" role="dialog">
            <div className="modal-head">
              <div>
                <p className="eyebrow">New Key</p>
                <h2>新建密钥</h2>
              </div>
              <button className="btn" disabled={loading} onClick={closeCreate} type="button">关闭</button>
            </div>

            <div className="field">
              <label htmlFor="key-name">名称</label>
              <input className="input" id="key-name" onChange={(event) => setName(event.target.value)} placeholder={`API Key ${items.length + 1}`} value={name} />
            </div>
            <button className="btn subtle" onClick={() => setAdvancedOpen((value) => !value)} type="button">
              <SlidersHorizontal /> {advancedOpen ? "收起高级限制" : "高级限制"}
            </button>
            {advancedOpen ? (
              <div className="surface" style={{ marginTop: 14 }}>
                <div className="field">
                  <label htmlFor="key-ip">IP 白名单</label>
                  <input className="input" id="key-ip" onChange={(event) => setIPWhitelist(event.target.value)} placeholder="留空不限制" value={ipWhitelist} />
                </div>
                <div className="grid grid-2">
              <div className="field">
                <label htmlFor="key-expiry">过期时间</label>
                <input className="input" id="key-expiry" onChange={(event) => setExpiresAt(event.target.value)} type="datetime-local" value={expiresAt} />
              </div>
              <div className="field">
                <label htmlFor="key-models">模型限制</label>
                <input className="input" id="key-models" onChange={(event) => setModels(event.target.value)} placeholder="留空不限制" value={models} />
              </div>
            </div>
                <div className="field">
                  <label>权限范围</label>
                  <div className="segmented">
                    {permissionOptions.map((option) => (
                      <button className={permissions.includes(option.value) ? "active" : ""} key={option.value} onClick={() => togglePermission(option.value)} type="button">
                        {option.label}
                      </button>
                    ))}
                  </div>
                </div>
              </div>
            ) : null}
            {error ? <p className="form-error">{error}</p> : null}
            <div className="form-actions">
              <button className="btn" disabled={loading} onClick={closeCreate} type="button">取消</button>
              <button className="btn primary" disabled={loading} onClick={createKey} type="button">{loading ? "创建中" : "创建密钥"}</button>
            </div>
          </section>
        </div>
      ) : null}
    </AppShell>
  );
}
