"use client";

import { useEffect, useState } from "react";
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
  { value: "responses", label: "响应" },
  { value: "messages", label: "消息" },
  { value: "gemini", label: "Gemini" },
  { value: "images", label: "Images" },
];

function fromApiKey(key: ApiKey): DisplayKey {
  return {
    id: key.id,
    name: key.name,
    key: key.key,
    status: key.enabled ? "已启用" : "已暂停",
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

  function togglePermission(value: string) {
    setPermissions((current) => current.includes(value) ? current.filter((item) => item !== value) : [...current, value]);
  }

  async function createKey() {
    const token = window.localStorage.getItem("uapi.user.token");
    const trimmedName = name.trim() || `密钥 ${items.length + 1}`;
    if (!token) return;
    setLoading(true);
    setError("");

    try {
      const created = await userApi.createKey(token, {
        name: trimmedName,
        ip_whitelist: ipWhitelist,
        expires_at: expiresAt ? new Date(expiresAt).toISOString() : undefined,
        models,
        permissions: permissions.join(","),
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
    <AppShell title="密钥管理">
      <PageHead
        title="当前密钥"
        description="普通用户默认保留一个可用 Key，创建后可再次查看和复制。"
        action={items.length === 0 ? <button className="btn primary" onClick={() => setOpen(true)} type="button"><Plus /> 新建密钥</button> : undefined}
      />

      <section className="surface">
        <div className="key-list">
          {items.length > 0 ? items.map((key) => (
            <article className="key-row" key={key.id || key.key}>
              <div>
                <strong>{key.name}</strong>
                <div className="key-meta"><span>创建 {key.created}</span><StatusBadge value={key.status} /></div>
              </div>
              <code>{key.key}</code>
              <div className="key-meta"><span>模型</span><strong>{key.models || "全部"}</strong><span>{key.expiresAt ? new Date(key.expiresAt).toLocaleDateString() : "永不过期"}</span></div>
              <div className="row-actions">
                <button className="btn icon-only" onClick={() => navigator.clipboard?.writeText(key.key)} title="复制" type="button"><Copy /></button>
                <button className="btn danger icon-only" onClick={() => deleteKey(key)} title="删除" type="button"><Trash2 /></button>
              </div>
            </article>
          )) : (
            <section className="card"><EmptyState title="暂无密钥" description="创建一个密钥后即可开始调用。" action={<button className="btn primary" onClick={() => setOpen(true)} type="button"><Plus /> 新建密钥</button>} /></section>
          )}
        </div>
      </section>

      {open ? (
        <div className="modal-backdrop" role="presentation">
          <section aria-modal="true" className="modal" role="dialog">
            <div className="modal-head">
              <div>
                <h2>新建密钥</h2>
                <p className="muted" style={{ margin: 0, fontSize: 13 }}>创建后可在当前页面再次查看完整 Key。</p>
              </div>
              <button className="btn" disabled={loading} onClick={closeCreate} type="button">关闭</button>
            </div>

            <div className="field">
              <label htmlFor="key-name">名称</label>
              <input className="input" id="key-name" onChange={(event) => setName(event.target.value)} placeholder={`密钥 ${items.length + 1}`} value={name} />
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
