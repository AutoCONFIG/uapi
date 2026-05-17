"use client";

import { useEffect, useMemo, useState } from "react";
import { CheckCircle2, KeyRound, Link2, Plus, ShieldCheck, X } from "lucide-react";
import { StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { Channel as ApiChannel, OAuthStatus } from "@/types/api";

type InitialChannel = {
  name: string;
  type: string;
  status: string;
  accounts: number;
  weight: number;
  error: string;
};

type DisplayChannel = {
  id?: string;
  name: string;
  type: string;
  status: string;
  accounts: number;
  weight: number;
  error: string;
};

type DraftChannel = {
  name: string;
  type: string;
  endpoint: string;
  models: string;
  authMode: "api_key" | "oauth";
  clientId: string;
  clientSecret: string;
  accountName: string;
};

const emptyDraft: DraftChannel = {
  name: "",
  type: "openai",
  endpoint: "https://api.openai.com",
  models: "gpt-4.1,gpt-4o-mini",
  authMode: "api_key",
  clientId: "",
  clientSecret: "",
  accountName: "",
};

function fromApiChannel(channel: ApiChannel): DisplayChannel {
  return {
    id: channel.id,
    name: channel.name,
    type: channel.type,
    status: channel.enabled ? "Healthy" : "Disabled",
    accounts: 0,
    weight: channel.priority,
    error: "0.00%",
  };
}

export function AdminChannelConsole({ initialChannels }: { initialChannels: InitialChannel[] }) {
  const [channels, setChannels] = useState<DisplayChannel[]>(initialChannels);
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<DraftChannel>(emptyDraft);
  const [loading, setLoading] = useState(false);
  const [oauthState, setOauthState] = useState("");
  const [oauthStatus, setOauthStatus] = useState<OAuthStatus | null>(null);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    adminApi.channels(token)
      .then((data) => setChannels(data.items.map(fromApiChannel)))
      .catch(() => {
        // Keep static preview mocks when the API server is not available.
      });
  }, []);

  useEffect(() => {
    if (!oauthState) return;
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    const timer = window.setInterval(() => {
      adminApi.channelOAuthStatus(token, oauthState)
        .then((status) => {
          setOauthStatus(status);
          if (status.status === "completed" || status.status === "error" || status.status === "bound") {
            window.clearInterval(timer);
          }
        })
        .catch(() => undefined);
    }, 3000);
    return () => window.clearInterval(timer);
  }, [oauthState]);

  const authSummary = useMemo(() => {
    if (draft.authMode === "api_key") {
      return "保存渠道后在同一渠道内添加一个或多个 API Key 凭证。";
    }
    return "OAuth 接入会先向后端请求授权 URL，再由 callback 完成账号绑定。";
  }, [draft.authMode]);

  function updateDraft<K extends keyof DraftChannel>(key: K, value: DraftChannel[K]) {
    setDraft((current) => ({ ...current, [key]: value }));
    setNotice("");
    setError("");
  }

  async function createChannel() {
    const token = window.localStorage.getItem("uapi.admin.token");
    const name = draft.name.trim() || `${draft.type.toUpperCase()} Channel`;
    setLoading(true);
    setError("");
    setNotice("");

    if (!token) {
      setChannels((current) => [
        { name, type: draft.type, status: draft.authMode === "oauth" ? "Pending OAuth" : "Healthy", accounts: 0, weight: 50, error: "0.00%" },
        ...current,
      ]);
      setOpen(false);
      setDraft(emptyDraft);
      setLoading(false);
      return;
    }

    try {
      const channel = await adminApi.createChannel(token, {
        name,
        type: draft.type,
        endpoint: draft.endpoint,
        models: draft.models,
        priority: 50,
        api_format: "standard",
        force_stream: false,
        affinity_ttl: 0,
      });
      setChannels((current) => [fromApiChannel(channel), ...current]);

      if (draft.authMode === "oauth") {
        const started = await adminApi.startChannelOAuth(token, {
          channel_id: channel.id,
          provider: channel.type,
          account_name: draft.accountName || `${name} OAuth`,
          client_id: draft.clientId || undefined,
          client_secret: draft.clientSecret || undefined,
        });
        setOauthState(started.state);
        setOauthStatus({ state: started.state, provider: channel.type as "openai" | "gemini", channel_id: channel.id, status: "pending", ready_to_bind: false, created_at: new Date().toISOString() });
        setNotice("授权链接已生成，完成浏览器授权后回到这里绑定凭证。");
        window.open(started.auth_url, "_blank", "noopener,noreferrer");
        return;
      }

      setOpen(false);
      setDraft(emptyDraft);
    } catch (err) {
      setError(err instanceof Error ? err.message : "保存渠道失败");
    } finally {
      setLoading(false);
    }
  }

  async function bindOAuth() {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token || !oauthState) return;
    setLoading(true);
    setError("");
    try {
      await adminApi.bindChannelOAuth(token, {
        state: oauthState,
        account_name: draft.accountName || `${draft.type}-oauth`,
        weight: 1,
        enabled: true,
      });
      setNotice("OAuth 凭证已绑定到渠道。");
      setOpen(false);
      setDraft(emptyDraft);
      setOauthState("");
      setOauthStatus(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "绑定 OAuth 凭证失败");
    } finally {
      setLoading(false);
    }
  }

  return (
    <>
      <section className="card card-pad channel-toolbar">
        <div>
          <h2>渠道列表</h2>
          <p className="muted" style={{ margin: 0 }}>
            每个渠道包含上游配置和凭证接入，多个凭证在渠道内形成池。
          </p>
        </div>
        <button className="btn primary" onClick={() => setOpen(true)} type="button">
          <Plus /> 新建渠道
        </button>
      </section>

      <section className="card" style={{ marginTop: 16 }}>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>名称</th>
                <th>类型</th>
                <th>状态</th>
                <th>凭证池</th>
                <th>权重</th>
                <th>错误率</th>
                <th>接入</th>
              </tr>
            </thead>
            <tbody>
              {channels.map((row) => (
                <tr key={`${row.id ?? row.name}-${row.type}`}>
                  <td>{row.name}</td>
                  <td>{row.type}</td>
                  <td><StatusBadge value={row.status} /></td>
                  <td>{row.accounts} 个凭证</td>
                  <td>{row.weight}</td>
                  <td>{row.error}</td>
                  <td>
                    <span className="badge">
                      {row.type === "openai" || row.type === "gemini" ? <Link2 size={14} /> : <KeyRound size={14} />}
                      {row.type === "openai" || row.type === "gemini" ? "OAuth / Key" : "API Key"}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      <section className="grid grid-3" style={{ marginTop: 16 }}>
        <div className="card card-pad">
          <h3>OpenAI / Codex OAuth</h3>
          <p className="muted">适合接入可刷新账号，后端负责生成 URL、校验 state 和保存 token。</p>
          <span className="badge green"><ShieldCheck size={14} /> Backend ready</span>
        </div>
        <div className="card card-pad">
          <h3>Gemini OAuth</h3>
          <p className="muted">支持 Google OAuth 接入入口，callback 完成后可绑定为渠道凭证。</p>
          <span className="badge green"><ShieldCheck size={14} /> Backend ready</span>
        </div>
        <div className="card card-pad">
          <h3>API Key</h3>
          <p className="muted">适合 Anthropic 或静态上游密钥，作为渠道内凭证池的一项管理。</p>
          <span className="badge"><KeyRound size={14} /> Manual secret</span>
        </div>
      </section>

      {open ? (
        <div className="modal-backdrop" role="presentation">
          <section aria-modal="true" className="modal" role="dialog">
            <div className="modal-head">
              <div>
                <p className="eyebrow">New Channel</p>
                <h2>新建渠道</h2>
              </div>
              <button className="btn" onClick={() => setOpen(false)} title="关闭" type="button"><X /></button>
            </div>

            <div className="grid grid-2">
              <div className="field">
                <label htmlFor="channel-name">渠道名称</label>
                <input className="input" id="channel-name" onChange={(event) => updateDraft("name", event.target.value)} placeholder="OpenAI Primary" value={draft.name} />
              </div>
              <div className="field">
                <label htmlFor="channel-type">上游类型</label>
                <select className="input" id="channel-type" onChange={(event) => updateDraft("type", event.target.value)} value={draft.type}>
                  <option value="openai">OpenAI</option>
                  <option value="anthropic">Anthropic</option>
                  <option value="gemini">Gemini</option>
                </select>
              </div>
            </div>

            <div className="field">
              <label htmlFor="endpoint">Endpoint</label>
              <input className="input" id="endpoint" onChange={(event) => updateDraft("endpoint", event.target.value)} value={draft.endpoint} />
            </div>

            <div className="field">
              <label htmlFor="models">模型列表</label>
              <input className="input" id="models" onChange={(event) => updateDraft("models", event.target.value)} value={draft.models} />
            </div>

            <div className="field">
              <label>认证方式</label>
              <div className="segmented">
                <button className={draft.authMode === "api_key" ? "active" : ""} onClick={() => updateDraft("authMode", "api_key")} type="button">
                  <KeyRound size={15} /> API Key
                </button>
                <button className={draft.authMode === "oauth" ? "active" : ""} onClick={() => updateDraft("authMode", "oauth")} type="button">
                  <Link2 size={15} /> OAuth
                </button>
              </div>
              <p className="muted" style={{ margin: "6px 0 0", fontSize: 13 }}>{authSummary}</p>
            </div>

            {draft.authMode === "oauth" ? (
              <div className="oauth-panel">
                <div className="grid grid-2">
                  <div className="field">
                    <label htmlFor="client-id">Client ID</label>
                    <input className="input" id="client-id" onChange={(event) => updateDraft("clientId", event.target.value)} placeholder="留空使用内置默认值" value={draft.clientId} />
                  </div>
                  <div className="field">
                    <label htmlFor="client-secret">Client Secret</label>
                    <input className="input" id="client-secret" onChange={(event) => updateDraft("clientSecret", event.target.value)} placeholder="Gemini 如需 secret 可填写" type="password" value={draft.clientSecret} />
                  </div>
                </div>
                <div className="field">
                  <label htmlFor="account-name">凭证名称</label>
                  <input className="input" id="account-name" onChange={(event) => updateDraft("accountName", event.target.value)} placeholder="openai-oauth-01" value={draft.accountName} />
                </div>
                {oauthStatus ? (
                  <p className="oauth-result">
                    <CheckCircle2 size={16} /> OAuth 状态：{oauthStatus.status}
                  </p>
                ) : null}
              </div>
            ) : (
              <div className="empty-note">
                API Key 凭证后续在渠道详情内新增。密钥本身不在列表页明文展示。
              </div>
            )}

            {notice ? <p className="oauth-result">{notice}</p> : null}
            {error ? <p className="form-error">{error}</p> : null}

            <div className="modal-actions">
              <button className="btn" onClick={() => setOpen(false)} type="button">取消</button>
              {oauthStatus?.ready_to_bind ? (
                <button className="btn primary" disabled={loading} onClick={bindOAuth} type="button">绑定 OAuth 凭证</button>
              ) : (
                <button className="btn primary" disabled={loading} onClick={createChannel} type="button">
                  {draft.authMode === "oauth" ? "保存并授权" : "保存渠道"}
                </button>
              )}
            </div>
          </section>
        </div>
      ) : null}
    </>
  );
}
