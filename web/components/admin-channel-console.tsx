"use client";

import { useMemo, useState } from "react";
import { CheckCircle2, KeyRound, Link2, Plus, ShieldCheck, X } from "lucide-react";
import { StatusBadge } from "@/components/shell";

type Channel = {
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
  scopes: string;
};

const emptyDraft: DraftChannel = {
  name: "",
  type: "openai",
  endpoint: "https://api.openai.com",
  models: "gpt-4.1,gpt-4o-mini",
  authMode: "api_key",
  clientId: "",
  scopes: "openid profile email offline_access",
};

export function AdminChannelConsole({ initialChannels }: { initialChannels: Channel[] }) {
  const [channels, setChannels] = useState(initialChannels);
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<DraftChannel>(emptyDraft);
  const [oauthStarted, setOauthStarted] = useState(false);

  const authSummary = useMemo(() => {
    if (draft.authMode === "api_key") {
      return "保存渠道后在同一渠道内添加一个或多个 API Key 凭证。";
    }
    return "OAuth 接入会先向后端请求授权 URL，再由 callback 完成账号绑定。";
  }, [draft.authMode]);

  function updateDraft<K extends keyof DraftChannel>(key: K, value: DraftChannel[K]) {
    setDraft((current) => ({ ...current, [key]: value }));
    if (key === "authMode") {
      setOauthStarted(false);
    }
  }

  function createChannel() {
    const name = draft.name.trim() || `${draft.type.toUpperCase()} Channel`;
    setChannels((current) => [
      {
        name,
        type: draft.type,
        status: draft.authMode === "oauth" && !oauthStarted ? "Pending OAuth" : "Healthy",
        accounts: draft.authMode === "oauth" ? 1 : 0,
        weight: 50,
        error: "0.00%",
      },
      ...current,
    ]);
    setOpen(false);
    setDraft(emptyDraft);
    setOauthStarted(false);
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
                <tr key={`${row.name}-${row.type}`}>
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
          <p className="muted">适合接入可刷新账号，前端负责发起授权，后端负责生成 URL、校验 state 和保存 token。</p>
          <span className="badge green"><ShieldCheck size={14} /> Ready in UI</span>
        </div>
        <div className="card card-pad">
          <h3>Gemini OAuth</h3>
          <p className="muted">支持 Google OAuth 接入入口，后端提供 callback 后即可完成绑定。</p>
          <span className="badge green"><ShieldCheck size={14} /> Ready in UI</span>
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
                <input
                  className="input"
                  id="channel-name"
                  onChange={(event) => updateDraft("name", event.target.value)}
                  placeholder="OpenAI Primary"
                  value={draft.name}
                />
              </div>
              <div className="field">
                <label htmlFor="channel-type">上游类型</label>
                <select
                  className="input"
                  id="channel-type"
                  onChange={(event) => updateDraft("type", event.target.value)}
                  value={draft.type}
                >
                  <option value="openai">OpenAI</option>
                  <option value="anthropic">Anthropic</option>
                  <option value="gemini">Gemini</option>
                </select>
              </div>
            </div>

            <div className="field">
              <label htmlFor="endpoint">Endpoint</label>
              <input
                className="input"
                id="endpoint"
                onChange={(event) => updateDraft("endpoint", event.target.value)}
                value={draft.endpoint}
              />
            </div>

            <div className="field">
              <label htmlFor="models">模型列表</label>
              <input
                className="input"
                id="models"
                onChange={(event) => updateDraft("models", event.target.value)}
                value={draft.models}
              />
            </div>

            <div className="field">
              <label>认证方式</label>
              <div className="segmented">
                <button
                  className={draft.authMode === "api_key" ? "active" : ""}
                  onClick={() => updateDraft("authMode", "api_key")}
                  type="button"
                >
                  <KeyRound size={15} /> API Key
                </button>
                <button
                  className={draft.authMode === "oauth" ? "active" : ""}
                  onClick={() => updateDraft("authMode", "oauth")}
                  type="button"
                >
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
                    <input
                      className="input"
                      id="client-id"
                      onChange={(event) => updateDraft("clientId", event.target.value)}
                      placeholder="app_... / google client id"
                      value={draft.clientId}
                    />
                  </div>
                  <div className="field">
                    <label htmlFor="scopes">Scopes</label>
                    <input
                      className="input"
                      id="scopes"
                      onChange={(event) => updateDraft("scopes", event.target.value)}
                      value={draft.scopes}
                    />
                  </div>
                </div>
                <button className="btn" onClick={() => setOauthStarted(true)} type="button">
                  <Link2 /> 生成授权链接
                </button>
                {oauthStarted ? (
                  <p className="oauth-result">
                    <CheckCircle2 size={16} /> 前端流程已准备好。接入真实后端后，这里会打开后端返回的授权 URL。
                  </p>
                ) : null}
              </div>
            ) : (
              <div className="empty-note">
                API Key 凭证后续在渠道详情内新增。密钥本身不在列表页明文展示。
              </div>
            )}

            <div className="modal-actions">
              <button className="btn" onClick={() => setOpen(false)} type="button">取消</button>
              <button className="btn primary" onClick={createChannel} type="button">保存渠道</button>
            </div>
          </section>
        </div>
      ) : null}
    </>
  );
}
