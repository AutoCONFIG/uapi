"use client";

import { useEffect, useMemo, useState } from "react";
import { CheckCircle2, Clipboard, KeyRound, Pencil, Plus, Power, Trash2, X } from "lucide-react";
import { EmptyState, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { Account, Channel, OAuthStatus } from "@/types/api";

const channelDefaults: Record<string, string> = {
  openai: "https://api.openai.com/v1",
  anthropic: "https://api.anthropic.com/v1",
  gemini: "https://generativelanguage.googleapis.com/v1beta",
};

const codeChannelDefaults: Record<string, string> = {
  openai: "https://api.openai.com/v1",
  anthropic: "https://api.anthropic.com/v1",
  gemini: "https://generativelanguage.googleapis.com",
};

type ChannelPreset = {
  id: string;
  label: string;
  type: string;
  apiFormat: string;
  auth: "oauth" | "apikey";
  endpoint: string;
  models: string;
  note: string;
};

const codexModels = "gpt-5.5,gpt-5.4,gpt-5.4-mini,gpt-5.3-codex,gpt-5.2,gpt-image-2";
const geminiCodeModels = "auto,pro,flash,flash-lite,gemini-2.5-pro,gemini-2.5-flash,gemini-2.5-flash-lite,gemini-3-pro-preview,gemini-3.1-pro-preview,gemini-3-flash-preview,gemini-3.1-flash-lite-preview";
const claudeCodeModels = "claude-opus-4-6,claude-sonnet-4-6,claude-haiku-4-5-20251001,claude-opus-4-5-20251101,claude-sonnet-4-5-20250929,claude-opus-4-1-20250805,claude-opus-4-20250514,claude-sonnet-4-20250514,claude-3-7-sonnet-20250219,claude-3-5-sonnet-20241022,claude-3-5-haiku-20241022";

const channelPresets: ChannelPreset[] = [
  { id: "codex", label: "Codex", type: "openai", apiFormat: "codex", auth: "oauth", endpoint: codeChannelDefaults.openai, models: codexModels, note: "OpenAI Responses API / OAuth" },
  { id: "gemini_code", label: "Gemini Code", type: "gemini", apiFormat: "gemini_code", auth: "oauth", endpoint: codeChannelDefaults.gemini, models: geminiCodeModels, note: "Gemini API / OAuth" },
  { id: "claude_code", label: "Claude Code", type: "anthropic", apiFormat: "claude_code", auth: "oauth", endpoint: codeChannelDefaults.anthropic, models: claudeCodeModels, note: "Claude Code OAuth / Anthropic Messages API" },
  { id: "openai_responses_api", label: "OpenAI Responses API", type: "openai", apiFormat: "responses", auth: "apikey", endpoint: channelDefaults.openai, models: "", note: "OpenAI Responses API" },
  { id: "openai_chat_completions", label: "OpenAI Chat Completions API", type: "openai", apiFormat: "standard", auth: "apikey", endpoint: channelDefaults.openai, models: "", note: "OpenAI Chat Completions API" },
  { id: "gemini_api", label: "Gemini API", type: "gemini", apiFormat: "standard", auth: "apikey", endpoint: channelDefaults.gemini, models: "", note: "Gemini generateContent API" },
  { id: "anthropic_messages", label: "Anthropic Messages API", type: "anthropic", apiFormat: "standard", auth: "apikey", endpoint: channelDefaults.anthropic, models: "", note: "Anthropic Messages API" },
];

const defaultPreset = channelPresets[4];

function createInitialDraft(preset: ChannelPreset = defaultPreset) {
  return { name: "", preset: preset.id, type: preset.type, models: preset.models, apiFormat: preset.apiFormat };
}

function defaultEndpointForChannel(channel: Pick<Channel, "type" | "api_format" | "endpoint">): string {
  return channel.endpoint || channelPresets.find((preset) => preset.type === channel.type && preset.apiFormat === channel.api_format)?.endpoint || channelDefaults[channel.type] || "";
}

function isCodeChannel(channel: Pick<Channel, "api_format">): boolean {
  return channel.api_format === "codex" || channel.api_format === "gemini_code" || channel.api_format === "claude_code";
}

function presetTitleLines(preset: ChannelPreset): [string, string] {
  const map: Record<string, [string, string]> = {
    codex: ["OpenAI", "Codex"],
    gemini_code: ["Google", "Gemini Code"],
    claude_code: ["Anthropic", "Claude Code"],
    openai_responses_api: ["OpenAI", "Responses API"],
    openai_chat_completions: ["OpenAI", "Chat Completions"],
    gemini_api: ["Google", "Gemini API"],
    anthropic_messages: ["Anthropic", "Messages API"],
  };
  return map[preset.id] || [preset.type.toUpperCase(), preset.label];
}

export function AdminChannelConsole() {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [detailOpen, setDetailOpen] = useState(false);
  const [channelEditOpen, setChannelEditOpen] = useState(false);
  const [accountEditOpen, setAccountEditOpen] = useState(false);
  const [selectedAccountID, setSelectedAccountID] = useState("");
  const [reauthAccountID, setReauthAccountID] = useState("");
  const [error, setError] = useState("");
  const [draft, setDraft] = useState(createInitialDraft());
  const [credentialMode, setCredentialMode] = useState<"oauth" | "apikey">("apikey");
  const [apiKeyName, setApiKeyName] = useState("");
  const [apiKeyValue, setApiKeyValue] = useState("");
  const [apiKeyEndpoint, setApiKeyEndpoint] = useState("");
  const [credLoading, setCredLoading] = useState(false);
  const [credError, setCredError] = useState("");
  const [credNotice, setCredNotice] = useState("");
  const [oauthChannel, setOauthChannel] = useState<Channel | null>(null);
  const [oauthState, setOauthState] = useState("");
  const [oauthStatus, setOauthStatus] = useState<OAuthStatus | null>(null);
  const [oauthMode, setOauthMode] = useState<"browser" | "device" | "manual_callback">("browser");
  const [oauthUserCode, setOauthUserCode] = useState("");
  const [oauthAuthURL, setOauthAuthURL] = useState("");
  const [oauthCallbackURL, setOauthCallbackURL] = useState("");
  const [oauthJSON, setOauthJSON] = useState("");
  const [exportPassword, setExportPassword] = useState("");
  const [exportData, setExportData] = useState<Record<string, unknown> | null>(null);

  const token = typeof window !== "undefined" ? window.localStorage.getItem("uapi.admin.token") : "";
  const selected = channels.find((item) => item.id === selectedID) || null;
  const selectedAccount = accounts.find((item) => item.id === selectedAccountID) || null;


  function presetForChannel(channel: Channel): ChannelPreset {
    if (channel.api_format === "codex") return channelPresets[0];
    if (channel.api_format === "gemini_code") return channelPresets[1];
    if (channel.api_format === "claude_code") return channelPresets[2];
    if (channel.type === "openai" && channel.api_format === "responses") return channelPresets[3];
    if (channel.type === "openai") return channelPresets[4];
    if (channel.type === "gemini") return channelPresets[5];
    if (channel.type === "anthropic") return channelPresets[6];
    return { id: channel.type, label: channel.type.toUpperCase(), type: channel.type, apiFormat: channel.api_format || "standard", auth: "apikey", endpoint: channel.endpoint, models: "", note: channel.type };
  }

  function channelAccounts(channelID: string): Account[] {
    return accounts.filter((item) => item.channel_id === channelID);
  }

  function accountMetaSummary(account: Account): string {
    const meta = account.metadata || {};
    const email = typeof meta.email === "string" ? meta.email : typeof meta.email_address === "string" ? meta.email_address : "";
    const plan = typeof meta.chatgpt_plan_type === "string" ? meta.chatgpt_plan_type : typeof meta.subscription_type === "string" ? meta.subscription_type : "";
    const project = typeof meta.project_id === "string" ? meta.project_id : "";
    return [email, plan, project].filter(Boolean).join(" · ");
  }

  function accountValidation(account: Account): { message: string; url: string } | null {
    const meta = account.metadata || {};
    if (meta.setup_status !== "validation_required" && meta.validation_required !== true) return null;
    const validation = asRecord(meta.validation);
    const message = stringValue(validation?.reason_message) || "需要完成 Gemini Code 验证";
    const url = stringValue(validation?.validation_url);
    return { message, url };
  }

  function accountQuotaItems(account: Account): { label: string; values: string[] }[] {
    const meta = account.metadata || {};
    const items: { label: string; values: string[] }[] = [];
    const codexUsage = asRecord(meta.codex_usage);
    if (codexUsage) {
      const text = summarizeCodexUsage(codexUsage);
      if (text) items.push({ label: "Codex 额度", values: splitQuotaText(text) });
    }
    const geminiQuota = asRecord(meta.user_quota);
    if (geminiQuota) {
      const text = summarizeGeminiQuota(geminiQuota);
      if (text) items.push({ label: "Gemini 配额", values: splitQuotaText(text) });
    }
    const paidTier = asRecord(meta.paid_tier);
    const credits = asArray(paidTier?.availableCredits);
    if (credits.length > 0) {
      const text = credits.map((credit) => {
        const row = asRecord(credit);
        return [stringValue(row?.creditType), stringValue(row?.creditAmount)].filter(Boolean).join(" ");
      }).filter(Boolean).join(" · ");
      if (text) items.push({ label: "Gemini Credits", values: splitQuotaText(text) });
    }
    const anthropicUsage = asRecord(meta.usage);
    if (anthropicUsage) {
      const text = summarizeAnthropicUsage(anthropicUsage);
      if (text) items.push({ label: "Anthropic 用量", values: splitQuotaText(text) });
    }
    return items;
  }

  function loadData() {
    if (!token) { setLoading(false); return; }
    setLoading(true);
    Promise.all([
      adminApi.channels(token, 1, 500).then((r) => r.items).catch(() => []),
      adminApi.accounts(token, 1, 1000).then((r) => r.items).catch(() => []),
    ]).then(([ch, ac]) => {
      setChannels(ch);
      setAccounts(ac);
      setSelectedID((current) => current || ch[0]?.id || "");
    }).finally(() => setLoading(false));
  }

  useEffect(() => { loadData(); }, []);

  useEffect(() => {
    if (!selected) return;
    setCredentialMode(isCodeChannel(selected) ? "oauth" : "apikey");
    setApiKeyName(`${selected.type}-account-${channelAccounts(selected.id).length + 1}`);
    setApiKeyValue("");
    setApiKeyEndpoint(defaultEndpointForChannel(selected));
    setCredError("");
    setCredNotice("");
    setOauthState("");
    setOauthStatus(null);
    setOauthAuthURL("");
    setOauthCallbackURL("");
    setOauthJSON("");
  }, [selected?.id]);

  useEffect(() => {
    if (!oauthState || !token || oauthMode === "manual_callback") return;
    const timer = window.setInterval(() => {
      adminApi.channelOAuthStatus(token, oauthState)
        .then((status) => {
          setOauthStatus(status);
          if (status.status === "completed") {
            window.clearInterval(timer);
            autoBind(token, status.state);
          }
          if (status.status === "error" || status.status === "bound") {
            window.clearInterval(timer);
          }
        })
        .catch(() => undefined);
    }, 2000);
    return () => window.clearInterval(timer);
  }, [oauthState, oauthMode, token]);

  const stats = useMemo(() => {
    const channelIDs = new Set(channels.map((item) => item.id));
    const visibleAccounts = accounts.filter((item) => channelIDs.has(item.channel_id));
    return {
      enabledChannels: channels.filter((item) => item.enabled).length,
      enabledAccounts: visibleAccounts.filter((item) => item.enabled).length,
      totalAccounts: visibleAccounts.length,
    };
  }, [channels, accounts]);

  function channelHealth(channel: Channel): "healthy" | "warning" | "disabled" {
    if (!channel.enabled) return "disabled";
    const related = channelAccounts(channel.id);
    if (related.length === 0) return "warning";
    if (!related.some((item) => item.enabled)) return "warning";
    if (related.some((item) => item.cooldown_until)) return "warning";
    return "healthy";
  }

  const selectedAccounts = useMemo(() => selected ? channelAccounts(selected.id) : [], [selected?.id, accounts]);


  function applyPreset(preset: ChannelPreset) {
    setDraft((cur) => ({
      ...cur,
      preset: preset.id,
      type: preset.type,
      models: preset.models,
      apiFormat: preset.apiFormat,
      name: cur.name,
    }));
    setCredentialMode(preset.auth === "oauth" ? "oauth" : "apikey");
    setOauthState("");
    setOauthStatus(null);
    setOauthAuthURL("");
    setOauthCallbackURL("");
    setOauthJSON("");
    setApiKeyValue("");
  }

  async function createDraftChannel(selectAfterCreate: boolean, overrides: Partial<typeof draft> = {}): Promise<Channel | null> {
    if (!token || saving) return null;
    setSaving(true);
    setError("");
    const channelDraft = { ...draft, ...overrides };
    try {
      const created = await adminApi.createChannel(token, {
        name: channelDraft.name.trim() || `${channelDraft.type.toUpperCase()} 渠道`,
        type: channelDraft.type,
        group: channelDraft.name.trim() || `${channelDraft.type.toUpperCase()} 渠道`,
        models: channelDraft.models.trim(),
        priority: 100,
        api_format: channelDraft.apiFormat,
        force_stream: false,
        affinity_ttl: 0,
      });
      setChannels((cur) => [created, ...cur]);
      if (selectAfterCreate) {
        setSelectedID(created.id);
      }
      setDraft(createInitialDraft());
      return created;
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建失败");
      return null;
    } finally {
      setSaving(false);
    }
  }

  async function createChannelOnly() {
    const channel = await createDraftChannel(true);
    if (channel) closeCreateDrawer();
  }

  function closeCreateDrawer() {
    setCreateOpen(false);
    setError("");
    setCredError("");
    setCredNotice("");
    setApiKeyValue("");
    setApiKeyEndpoint("");
    setOauthChannel(null);
    setOauthState("");
    setOauthStatus(null);
    setOauthAuthURL("");
    setOauthCallbackURL("");
    setOauthJSON("");
  }

  async function patchChannel(id: string, body: Partial<Channel>) {
    if (!token) return;
    setCredError("");
    try {
      const updated = await adminApi.updateChannel(token, id, body);
      setChannels((cur) => cur.map((item) => item.id === id ? updated : item));
    } catch (err) {
      setCredError(err instanceof Error ? err.message : "更新渠道失败");
    }
  }

  async function deleteChannel(id: string) {
    if (!token) return;
    if (!confirm("确认删除该渠道？关联账号也会停止使用。")) return;
    setCredError("");
    try {
      await adminApi.deleteChannel(token, id);
      setChannels((cur) => {
        const next = cur.filter((item) => item.id !== id);
        setSelectedID(next[0]?.id || "");
        return next;
      });
      setAccounts((cur) => cur.filter((item) => item.channel_id !== id));
      setDetailOpen(false);
      setChannelEditOpen(false);
      setAccountEditOpen(false);
    } catch (err) {
      setCredError(err instanceof Error ? err.message : "删除渠道失败");
    }
  }

  async function addApiKey() {
    if (!token || !selected || !apiKeyValue.trim()) return;
    setCredLoading(true);
    setCredError("");
    try {
      const account = await adminApi.createAccount(token, {
        channel_id: selected.id,
        name: apiKeyName.trim() || `${selected.type}-key`,
        credentials: apiKeyValue.trim(),
        endpoint: apiKeyEndpoint.trim() || defaultEndpointForChannel(selected),
        weight: 100,
        enabled: true,
      });
      setAccounts((cur) => [account, ...cur]);
      setApiKeyName(`${selected.type}-account-${channelAccounts(selected.id).length + 2}`);
      setApiKeyValue("");
      setApiKeyEndpoint(defaultEndpointForChannel(selected));
      setCredNotice("密钥账号已添加。");
    } catch (err) {
      setCredError(err instanceof Error ? err.message : "添加失败");
    } finally {
      setCredLoading(false);
    }
  }

  async function startOAuth() {
    if (!token || !selected) return;
    setCredLoading(true);
    setCredError("");
    setCredNotice("");
    try {
      const started = await adminApi.startChannelOAuth(token, { channel_id: selected.id, provider: selected.type });
      setOauthChannel(selected);
      setOauthState(started.state);
      setOauthMode(started.mode);
      setOauthUserCode(started.user_code || "");
      setOauthAuthURL(started.auth_url);
      setOauthStatus({ state: started.state, provider: selected.type as "openai" | "gemini" | "anthropic", channel_id: selected.id, status: "pending", ready_to_bind: false, created_at: new Date().toISOString() });
      setCredNotice(started.mode === "manual_callback" ? "登录页已打开，完成后把本地回调 URL 或认证 JSON 粘贴回来。" : "认证页面已打开，完成后会自动绑定。");
      window.open(started.auth_url, "_blank", "noopener,noreferrer");
    } catch (err) {
      setCredError(err instanceof Error ? err.message : "发起认证失败");
    } finally {
      setCredLoading(false);
    }
  }

  async function startOAuthReauth(account: Account) {
    setReauthAccountID(account.id);
    await startOAuth();
  }

  async function completeOAuth() {
    if (!token || !oauthState || (!oauthCallbackURL.trim() && !oauthJSON.trim())) return;
    setCredLoading(true);
    setCredError("");
    try {
      const status = await adminApi.completeChannelOAuth(token, {
        state: oauthState,
        callback_url: oauthCallbackURL.trim() || undefined,
        oauth_json: oauthJSON.trim() || undefined,
      });
      setOauthStatus(status);
      if (status.status === "completed") {
        await autoBind(token, status.state);
      }
    } catch (err) {
      setCredError(err instanceof Error ? err.message : "认证完成失败");
    } finally {
      setCredLoading(false);
    }
  }

  async function autoBind(adminToken: string, state: string) {
    const channel = oauthChannel || selected;
    if (!channel) return;
    setCredLoading(true);
    try {
      const replacing = reauthAccountID ? accounts.find((item) => item.id === reauthAccountID) : null;
      const account = await adminApi.bindChannelOAuth(adminToken, {
        state,
        account_name: replacing?.name || `${channel.type}-oauth-${channelAccounts(channel.id).length + 1}`,
        weight: replacing?.weight ?? 100,
        enabled: replacing?.enabled ?? true,
      });
      if (replacing && token) {
        await adminApi.deleteAccount(token, replacing.id);
        setAccounts((cur) => [account, ...cur.filter((item) => item.id !== replacing.id)]);
        setSelectedAccountID(account.id);
        setCredNotice("OAuth 账号已重新认证。");
      } else {
        setAccounts((cur) => [account, ...cur]);
        setCredNotice("OAuth 凭证已绑定。");
      }
      setSelectedID(channel.id);
      setReauthAccountID("");
      setOauthState("");
      setOauthStatus(null);
      setOauthCallbackURL("");
      setOauthJSON("");
      if (createOpen) {
        window.setTimeout(() => closeCreateDrawer(), 500);
      }
    } catch (err) {
      setCredError(err instanceof Error ? err.message : "绑定失败");
    } finally {
      setCredLoading(false);
    }
  }

  async function exportAccountCredential(account: Account) {
    if (!token || !exportPassword.trim()) return;
    setCredLoading(true);
    setCredError("");
    setCredNotice("");
    setExportData(null);
    try {
      const data = await adminApi.exportAccount(token, { id: account.id, password: exportPassword });
      setExportData(data);
      setCredNotice("凭据已解密，仅在当前页面显示。");
    } catch (err) {
      setCredError(err instanceof Error ? err.message : "导出凭据失败");
    } finally {
      setCredLoading(false);
    }
  }

  async function patchAccount(id: string, body: Partial<Account>) {
    if (!token) return;
    setCredError("");
    try {
      const updated = await adminApi.updateAccount(token, id, body);
      setAccounts((cur) => cur.map((item) => item.id === id ? updated : item));
    } catch (err) {
      setCredError(err instanceof Error ? err.message : "更新凭证失败");
    }
  }

  async function updateAccountFromForm(account: Account, form: HTMLFormElement) {
    if (!token) return;
    const data = new FormData(form);
    const nextKey = String(data.get("credentials") || "").trim();
    const enabled = data.get("enabled") === "on";
    const body: Partial<{ name: string; credentials: string; endpoint: string; weight: number; enabled: boolean }> = {
      name: String(data.get("name") || account.name).trim() || account.name,
      weight: Number(data.get("weight") || account.weight || 0),
      enabled,
    };
    if (nextKey) body.credentials = nextKey;
    const nextEndpoint = String(data.get("endpoint") || "").trim();
    if (account.cred_type === "api_key" && selected) body.endpoint = nextEndpoint || defaultEndpointForChannel(selected);
    setCredLoading(true);
    setCredError("");
    try {
      const updated = await adminApi.updateAccount(token, account.id, body);
      setAccounts((cur) => cur.map((item) => item.id === account.id ? updated : item));
      setCredNotice("账号信息已更新。");
    } catch (err) {
      setCredError(err instanceof Error ? err.message : "更新账号失败");
    } finally {
      setCredLoading(false);
    }
  }

  async function deleteAccount(id: string) {
    if (!token) return;
    setCredError("");
    try {
      await adminApi.deleteAccount(token, id);
      setAccounts((cur) => cur.filter((item) => item.id !== id));
    } catch (err) {
      setCredError(err instanceof Error ? err.message : "删除凭证失败");
    }
  }

  return (
    <>
      <section className="ops-summary">
        <div className="ops-stat"><span>可用渠道</span><strong>{stats.enabledChannels} / {channels.length}</strong></div>
        <div className="ops-stat"><span>可用凭证</span><strong>{stats.enabledAccounts} / {stats.totalAccounts}</strong></div>
      </section>

      <section className="channel-workbench">
        <aside className="channel-list card">
          <div className="channel-list-head">
            <h2>渠道</h2>
            <div className="channel-list-actions">
              <span className="badge">{channels.length}</span>
              <button className="btn primary icon-only" onClick={() => setCreateOpen(true)} title="新增渠道" type="button"><Plus /></button>
            </div>
          </div>
          <div className="channel-list-items">
            {channels.map((channel) => {
              const related = channelAccounts(channel.id);
              const health = channelHealth(channel);
              return (
                <div
                  className={`channel-item${selected?.id === channel.id ? " active" : ""}`}
                  key={channel.id}
                  onClick={() => {
                    setSelectedID(channel.id);
                    setDetailOpen(false);
                    setChannelEditOpen(false);
                    setAccountEditOpen(false);
                  }}
                  role="button"
                  tabIndex={0}
                >
                  <span>
                    <strong>{channel.name}</strong>
                    <small>{related.length} 个账号</small>
                  </span>
                  <div className="channel-item-actions" onClick={(event) => event.stopPropagation()}>
                    <span className={`health-dot ${health}`} />
                    <button className="btn subtle icon-only" onClick={() => patchChannel(channel.id, { enabled: !channel.enabled })} title={channel.enabled ? "停用渠道" : "启用渠道"} type="button"><Power /></button>
                    <button className="btn subtle icon-only danger-icon" onClick={() => deleteChannel(channel.id)} title="删除渠道" type="button"><Trash2 /></button>
                  </div>
                </div>
              );
            })}
            {channels.length === 0 && !loading ? <EmptyState title="暂无渠道" description="先创建一个渠道，再添加账号。" /> : null}
                      </div>
        </aside>

        <section className="channel-detail card">
          {selected ? (
            <>
              <div className="channel-grid-head">
                <div className="channel-title-edit">
                  <input className="channel-title-input" defaultValue={selected.name} key={`title-${selected.id}`} onBlur={(e) => {
                    const value = e.target.value.trim();
                    if (value && value !== selected.name) patchChannel(selected.id, { name: value });
                  }} placeholder="渠道名称" />
                </div>
                <div className="channel-head-actions">
                  <button className="btn" onClick={() => setChannelEditOpen(true)} type="button"><Pencil /> 编辑渠道</button>
                  <button className="btn" onClick={() => setDetailOpen(true)} type="button"><Plus /> 新增账号</button>
                </div>
              </div>

              <div className="account-card-grid">
                {selectedAccounts.map((account) => {
                  const state = accountState(account);
                  const quotaItems = accountQuotaItems(account);
                  const validation = accountValidation(account);
                  return (
                    <article className={`account-card ${state.tone}`} key={account.id}>
                      <div className="account-card-head">
                        <span className={`account-light ${state.tone}`} title={state.label} />
                        <strong>{account.name}</strong>
                        <span className="badge">{account.cred_type === "oauth_token" ? "OAuth" : "Key"}</span>
                      </div>
                      {accountMetaSummary(account) ? <p>{accountMetaSummary(account)}</p> : <p>{selected.type} · 权重 {account.weight || 0}</p>}
                      <div className="account-card-meta">
                        <span>{state.label}</span>
                        {account.token_expiry ? <span>访问令牌 {new Date(account.token_expiry).toLocaleString()}</span> : <span>长期凭证</span>}
                      </div>
                      {quotaItems.length > 0 ? (
                        <div className="account-card-quota">
                          {quotaItems.slice(0, 2).map((item) => (
                            <div className="quota-block" key={item.label}>
                              <b>{item.label}</b>
                              {item.values.map((value) => <span key={`${item.label}-${value}`}>{value}</span>)}
                            </div>
                          ))}
                        </div>
                      ) : null}
                      {validation ? <a className="account-card-warning" href={validation.url || undefined} rel="noreferrer" target="_blank">{validation.message}</a> : null}
                      <div className="account-card-actions">
                        <button className="btn subtle" onClick={() => { setSelectedAccountID(account.id); setExportPassword(""); setExportData(null); setAccountEditOpen(true); }} type="button"><Pencil /> 编辑</button>
                        <button className="btn subtle" onClick={() => patchAccount(account.id, { enabled: !account.enabled })} type="button">{account.enabled ? "停用" : "启用"}</button>
                        <button className="btn danger" onClick={() => deleteAccount(account.id)} title="删除账号" type="button"><X /></button>
                      </div>
                    </article>
                  );
                })}
                {selectedAccounts.length === 0 && !loading ? <EmptyState title="暂无账号" description="点击添加账号，绑定密钥或完成 OAuth 登录。" /> : null}
              </div>
            </>
          ) : loading ? (
            <EmptyState title="加载中" description="正在加载渠道。" />
          ) : (
            <EmptyState title="暂无渠道" description="先创建渠道。" />
          )}
        </section>
      </section>

      {accountEditOpen && selected && selectedAccount ? (
        <div className="drawer-backdrop" onClick={() => setAccountEditOpen(false)} role="presentation">
          <aside aria-label="编辑账号" className="side-drawer channel-config-drawer" onClick={(event) => event.stopPropagation()}>
            <div className="drawer-head">
              <div>
                <p className="eyebrow">账号</p>
                <h2>编辑账号</h2>
              </div>
              <button className="btn" onClick={() => setAccountEditOpen(false)} title="关闭" type="button"><X /></button>
            </div>

            <div className="drawer-body">
              <form className="account-edit-form" onSubmit={(event) => { event.preventDefault(); updateAccountFromForm(selectedAccount, event.currentTarget); }}>
                <div className="field">
                  <label>账号名称</label>
                  <input className="input" name="name" defaultValue={selectedAccount.name} />
                </div>
                <div className="field">
                  <label>调度权重</label>
                  <input className="input" name="weight" type="number" min={0} defaultValue={selectedAccount.weight || 0} />
                </div>
                <label className="check-row">
                  <input name="enabled" type="checkbox" defaultChecked={selectedAccount.enabled} />
                  <span>启用账号</span>
                </label>
                {selectedAccount.cred_type === "api_key" ? (
                  <div className="field">
                    <label>更新密钥</label>
                    <input className="input" name="credentials" placeholder="留空则不更新" type="password" />
                  </div>
                ) : null}
                {selectedAccount.cred_type === "api_key" ? (
                  <div className="field">
                    <label>接入点（含路径）</label>
                    <input className="input" name="endpoint" defaultValue={selectedAccount.endpoint || defaultEndpointForChannel(selected)} placeholder="https://api.example.com/custom-route" />
                  </div>
                ) : null}
                <button className="btn primary" disabled={credLoading} type="submit">{credLoading ? "保存中" : "保存账号"}</button>
              </form>

              <div className="credential-export-panel">
                <div className="section-head">
                  <div><h2>{selectedAccount.cred_type === "oauth_token" ? "导出认证 JSON" : "导出密钥"}</h2><p className="muted">需要输入管理员密码后显示，便于导入其他工具。</p></div>
                </div>
                <div className="export-form-row">
                  <input className="input" value={exportPassword} onChange={(event) => setExportPassword(event.target.value)} placeholder="管理员密码" type="password" />
                  <button className="btn" disabled={credLoading || !exportPassword.trim()} onClick={() => exportAccountCredential(selectedAccount)} type="button"><KeyRound /> 显示</button>
                  {exportData ? <button className="btn subtle" onClick={() => navigator.clipboard?.writeText(JSON.stringify(exportData, null, 2))} type="button"><Clipboard /> 复制</button> : null}
                </div>
                {exportData ? <pre className="credential-export-json">{JSON.stringify(exportData, null, 2)}</pre> : null}
              </div>

              {selectedAccount.cred_type === "oauth_token" ? (
                <div className="credential-editor account-create-panel">
                  <div className="section-head">
                    <div><h2>重新认证</h2><p className="muted">完成 OAuth 后会替换当前账号的认证信息。</p></div>
                  </div>
                  <OAuthPanel
                    channel={selected}
                    loading={credLoading}
                    authURL={oauthAuthURL}
                    callbackURL={oauthCallbackURL}
                    jsonValue={oauthJSON}
                    mode={oauthMode}
                    notice={credNotice}
                    status={oauthStatus}
                    userCode={oauthUserCode}
                    onCallbackChange={setOauthCallbackURL}
                    onJSONChange={setOauthJSON}
                    onStart={() => startOAuthReauth(selectedAccount)}
                    onComplete={completeOAuth}
                  />
                </div>
              ) : null}
              {credNotice ? <p className="form-success">{credNotice}</p> : null}
              {credError ? <p className="form-error">{credError}</p> : null}
            </div>
          </aside>
        </div>
      ) : null}

      {channelEditOpen && selected ? (
        <div className="drawer-backdrop" onClick={() => setChannelEditOpen(false)} role="presentation">
          <aside aria-label="编辑渠道" className="side-drawer channel-config-drawer" onClick={(event) => event.stopPropagation()}>
            <div className="drawer-head">
              <div>
                <p className="eyebrow">渠道</p>
                <h2>编辑渠道</h2>
              </div>
              <button className="btn" onClick={() => setChannelEditOpen(false)} title="关闭" type="button"><X /></button>
            </div>

            <div className="drawer-body">
              <div className="resource-title">
                <StatusBadge value={selected.enabled ? "enabled" : "disabled"} />
                <span className="badge">{presetForChannel(selected).label}</span>
                <span className="badge">{selected.api_format || "standard"}</span>
              </div>
              <div className="channel-edit-grid drawer-channel-edit">
                <input className="input" defaultValue={selected.name} key={`drawer-name-${selected.id}`} onBlur={(e) => {
                  const value = e.target.value.trim();
                  if (value && value !== selected.name) patchChannel(selected.id, { name: value });
                }} placeholder="渠道名称" />
                <input className="input wide" defaultValue={selected.models} key={`drawer-models-${selected.id}`} onBlur={(e) => {
                  const value = e.target.value.trim();
                  if (value !== selected.models) patchChannel(selected.id, { models: value });
                }} placeholder="模型列表，留空表示不限制" />
              </div>
            </div>
          </aside>
        </div>
      ) : null}

      {detailOpen && selected ? (
        <div className="drawer-backdrop" onClick={() => setDetailOpen(false)} role="presentation">
          <aside aria-label="新增账号" className="side-drawer channel-config-drawer" onClick={(event) => event.stopPropagation()}>
            <div className="drawer-head">
              <div>
                <p className="eyebrow">新增账号</p>
                <h2>{selected.name}</h2>
              </div>
              <button className="btn" onClick={() => setDetailOpen(false)} title="关闭" type="button"><X /></button>
            </div>

            <div className="drawer-body">
              <div className="resource-title">
                <StatusBadge value={selected.enabled ? "enabled" : "disabled"} />
                <span className="badge">{presetForChannel(selected).label}</span>
                <span className="badge">{selectedAccounts.length} 个账号</span>
              </div>

              <div className="credential-editor account-create-panel">
                <div className="section-head">
                  <div><h2>新增账号</h2><p className="muted">账号添加后归入当前渠道，由 Gateway 统一调度。</p></div>
                  {(selected.type === "openai" || selected.type === "gemini" || selected.type === "anthropic") && !isCodeChannel(selected) ? (
                    <div className="segmented">
                      <button className={credentialMode === "apikey" ? "active" : ""} onClick={() => setCredentialMode("apikey")} type="button"><KeyRound /> 密钥</button>
                    </div>
                  ) : null}
                </div>

                {isCodeChannel(selected) ? (
                  <OAuthPanel
                    channel={selected}
                    loading={credLoading}
                    authURL={oauthAuthURL}
                    callbackURL={oauthCallbackURL}
                    jsonValue={oauthJSON}
                    mode={oauthMode}
                    notice={credNotice}
                    status={oauthStatus}
                    userCode={oauthUserCode}
                    onCallbackChange={setOauthCallbackURL}
                    onJSONChange={setOauthJSON}
                    onStart={startOAuth}
                    onComplete={completeOAuth}
                  />
                ) : credentialMode === "apikey" ? (
                  <div className="api-key-editor">
                    <input className="input" value={apiKeyName} onChange={(e) => setApiKeyName(e.target.value)} placeholder={`${selected.type}-key-01`} />
                    <input className="input" value={apiKeyEndpoint} onChange={(e) => setApiKeyEndpoint(e.target.value)} placeholder={`${defaultEndpointForChannel(selected)} 或自定义路径`} />
                    <input className="input" value={apiKeyValue} onChange={(e) => setApiKeyValue(e.target.value)} placeholder="密钥 / 令牌" type="password" />
                    <button className="btn primary" disabled={credLoading || !apiKeyValue.trim()} onClick={addApiKey} type="button"><Plus /> 添加</button>
                  </div>
                ) : null}
                {credNotice && credentialMode !== "oauth" ? <p className="form-success">{credNotice}</p> : null}
                {credError ? <p className="form-error">{credError}</p> : null}
              </div>
            </div>
          </aside>
        </div>
      ) : null}

      {createOpen ? (
        <div className="drawer-backdrop" onClick={closeCreateDrawer} role="presentation">
          <aside aria-label="新增渠道" className="side-drawer" onClick={(event) => event.stopPropagation()}>
            <div className="drawer-head">
              <div>
                <p className="eyebrow">新增渠道</p>
                <h2>新增渠道</h2>
              </div>
              <button className="btn" onClick={closeCreateDrawer} title="关闭" type="button"><X /></button>
            </div>

            <div className="drawer-body">
              <div className="preset-grid provider-pick-grid">
                {channelPresets.map((preset) => (
                  <button className={draft.preset === preset.id ? "active" : ""} key={preset.id} onClick={() => applyPreset(preset)} type="button">
                    <strong className="preset-title">
                      {presetTitleLines(preset).map((line) => <span key={`${preset.id}-${line}`}>{line}</span>)}
                    </strong>
                    <span className={`preset-auth ${preset.auth}`}>{preset.auth === "oauth" ? "OAuth 认证" : "密钥认证"}</span>
                  </button>
                ))}
              </div>

              <div className="field">
                <label>渠道名称</label>
                <input className="input" value={draft.name} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} placeholder={`${channelPresets.find((item) => item.id === draft.preset)?.label || draft.type} 渠道`} />
              </div>


              {draft.type === "openai" && !isCodeChannel({ api_format: draft.apiFormat } as Channel) ? (
                <div className="field">
                  <label>API 类型</label>
                  <div className="segmented">
                    <button className={draft.apiFormat === "standard" ? "active" : ""} onClick={() => setDraft((d) => ({ ...d, apiFormat: "standard" }))} type="button">对话补全</button>
                    <button className={draft.apiFormat === "responses" ? "active" : ""} onClick={() => setDraft((d) => ({ ...d, apiFormat: "responses" }))} type="button">响应接口</button>
                  </div>
                </div>
              ) : null}

              <button className="btn primary" disabled={saving} onClick={createChannelOnly} type="button">
                {saving ? "正在创建" : "创建渠道"}
              </button>
              <p className="muted">渠道只定义供应商、协议和模型范围；接入点随账号维护，可填写包含 /v1 或其他兼容路由的完整前缀。</p>

              {error ? <p className="form-error">{error}</p> : null}
            </div>
          </aside>
        </div>
      ) : null}
    </>
  );
}

type AccountTone = "healthy" | "warning" | "exhausted";

function accountState(account: Account): { tone: AccountTone; label: string } {
  const meta = account.metadata || {};
  const now = Date.now();
  const expiry = account.token_expiry ? Date.parse(account.token_expiry) : 0;
  const invalidStatus = ["invalid", "revoked", "expired", "auth_failed", "unauthorized", "disabled"].includes(String(meta.status || meta.setup_status || "").toLowerCase());
  const needsValidation = meta.validation_required === true || meta.setup_status === "validation_required";
  if (!account.enabled || invalidStatus || needsValidation || (expiry > 0 && expiry <= now)) {
    return { tone: "exhausted", label: "渠道失效" };
  }
  const usage = accountUsagePercent(account);
  if ((usage !== null && usage >= 100) || Boolean(account.cooldown_until)) return { tone: "warning", label: "额度耗尽" };
  return { tone: "healthy", label: "账号正常" };
}

function accountUsagePercent(account: Account): number | null {
  const meta = account.metadata || {};
  const candidates: number[] = [];
  const codexUsage = asRecord(meta.codex_usage);
  if (codexUsage) {
    const limits = asRecord(codexUsage.rate_limits) || asRecord(codexUsage.rateLimits) || codexUsage;
    for (const key of ["primary", "secondary", "credits"]) {
      const row = asRecord(limits[key]);
      const percent = numberValue(row?.used_percent ?? row?.usedPercent ?? row?.utilization);
      if (percent !== null) candidates.push(normalizePercent(percent));
    }
  }
  const geminiQuota = asRecord(meta.user_quota);
  if (geminiQuota) {
    const buckets = asArray(geminiQuota.buckets).map(asRecord).filter(Boolean) as Record<string, unknown>[];
    for (const bucket of buckets) {
      const remaining = numberValue(bucket.remainingFraction);
      if (remaining !== null) candidates.push(Math.max(0, Math.min(100, (1 - remaining) * 100)));
    }
  }
  const anthropicUsage = asRecord(meta.usage);
  if (anthropicUsage) {
    for (const key of ["five_hour", "seven_day", "seven_day_sonnet", "seven_day_opus", "seven_day_oauth_apps"]) {
      const row = asRecord(anthropicUsage[key]);
      const percent = numberValue(row?.utilization);
      if (percent !== null) candidates.push(normalizePercent(percent));
    }
  }
  return candidates.length > 0 ? Math.max(...candidates) : null;
}

function numberValue(value: unknown): number | null {
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value === "string" && value.trim() !== "") {
    const parsed = Number(value);
    if (Number.isFinite(parsed)) return parsed;
  }
  return null;
}

function normalizePercent(value: number): number {
  return value <= 1 ? value * 100 : value;
}


function splitQuotaText(text: string): string[] {
  return text.split(" · ").map((item) => item.trim()).filter(Boolean);
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : null;
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function stringValue(value: unknown): string {
  if (typeof value === "string") return value;
  if (typeof value === "number" && Number.isFinite(value)) return String(value);
  if (typeof value === "boolean") return value ? "true" : "false";
  return "";
}

function percentValue(value: unknown): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return "";
  return `${Math.round(value)}%`;
}

function fractionPercentValue(value: unknown): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return "";
  return `${Math.round(value * 100)}%`;
}

function summarizeCodexUsage(usage: Record<string, unknown>): string {
  const rateLimits = asRecord(usage.rate_limits) || asRecord(usage.rateLimits) || usage;
  const primary = asRecord(rateLimits.primary);
  const secondary = asRecord(rateLimits.secondary);
  const credits = asRecord(rateLimits.credits);
  const bits = [
    stringValue(rateLimits.plan_type || rateLimits.planType),
    primary ? `主窗口 ${percentValue(primary.used_percent || primary.usedPercent)}` : "",
    secondary ? `次窗口 ${percentValue(secondary.used_percent || secondary.usedPercent)}` : "",
    credits ? `Credits ${stringValue(credits.balance) || (credits.unlimited === true ? "unlimited" : "")}` : "",
  ].filter(Boolean);
  return bits.join(" · ");
}

function summarizeGeminiQuota(quota: Record<string, unknown>): string {
  const buckets = asArray(quota.buckets).map(asRecord).filter(Boolean) as Record<string, unknown>[];
  if (buckets.length === 0) return "";
  const remainingPercents = buckets
    .map((bucket) => numberValue(bucket.remainingFraction))
    .filter((value): value is number => value !== null)
    .map((value) => Math.max(0, Math.min(100, value * 100)));
  const remainingAmounts = buckets.map((bucket) => stringValue(bucket.remainingAmount)).filter(Boolean);
  const reset = buckets.map((bucket) => stringValue(bucket.resetTime)).filter(Boolean).sort()[0] || "";
  const remaining = remainingPercents.length > 0
    ? `${Math.round(Math.min(...remainingPercents))}%`
    : remainingAmounts[0] || "";
  return [`${buckets.length} 个额度桶`, remaining ? `最低剩余 ${remaining}` : "", reset ? `重置 ${reset}` : ""].filter(Boolean).join(" · ");
}

function summarizeAnthropicUsage(usage: Record<string, unknown>): string {
  return ["five_hour", "seven_day", "seven_day_sonnet", "seven_day_opus", "seven_day_oauth_apps"].map((key) => {
    const limit = asRecord(usage[key]);
    if (!limit) return "";
    const used = percentValue(limit.utilization);
    const reset = stringValue(limit.resets_at);
    return [key.replaceAll("_", " "), used ? `${used} used` : "", reset ? `reset ${reset}` : ""].filter(Boolean).join(" ");
  }).filter(Boolean).join(" · ");
}

function OAuthPanel({
  channel,
  loading,
  authURL,
  callbackURL,
  jsonValue,
  mode,
  notice,
  status,
  userCode,
  onCallbackChange,
  onJSONChange,
  onStart,
  onComplete,
}: {
  channel: Channel;
  loading: boolean;
  authURL: string;
  callbackURL: string;
  jsonValue: string;
  mode: "browser" | "device" | "manual_callback";
  notice: string;
  status: OAuthStatus | null;
  userCode: string;
  onCallbackChange: (value: string) => void;
  onJSONChange: (value: string) => void;
  onStart: () => void;
  onComplete: () => void;
}) {
  const callbackPlaceholder = channel.type === "openai"
    ? "http://localhost:1455/auth/callback?code=...&state=..."
    : channel.type === "gemini"
      ? "http://127.0.0.1:1456/oauth2callback?code=...&state=..."
      : "https://platform.claude.com/oauth/code/callback?code=...&state=...";
  const jsonPlaceholder = channel.type === "openai"
    ? "{\"callback_url\":\"http://localhost:1455/auth/callback?code=...&state=...\"}"
    : channel.type === "gemini"
      ? "{\"access_token\":\"ya29...\",\"refresh_token\":\"1//...\",\"expiry_date\":1710000000000}"
      : "{\"callback_url\":\"https://platform.claude.com/oauth/code/callback?code=...&state=...\"}";

  if (!status) {
    return (
      <div className="oauth-inline">
        <p className="muted">{channel.type === "openai" ? "按 Codex 客户端方式认证。" : channel.type === "gemini" ? "按 Gemini Code 客户端方式认证。" : "按 Claude Code 客户端方式认证。"}</p>
        <button className="btn primary" disabled={loading} onClick={onStart} type="button">{loading ? "正在生成" : "开始 OAuth"}</button>
      </div>
    );
  }

  if (status.status === "completed") {
    return <p className="form-success"><CheckCircle2 /> 认证成功，正在绑定。</p>;
  }

  if (status.status === "error") {
    return <p className="form-error">认证失败：{status.error}</p>;
  }

  return (
    <div className="oauth-inline">
      {mode === "manual_callback" ? (
        <>
          <div className="oauth-actions">
            <a className="btn primary" href={authURL} rel="noreferrer" target="_blank">打开登录页</a>
            <button className="btn" onClick={() => navigator.clipboard?.writeText(authURL)} type="button"><Clipboard /> 复制链接</button>
          </div>
          <textarea className="input" onChange={(e) => onCallbackChange(e.target.value)} placeholder={callbackPlaceholder} rows={3} value={callbackURL} />
          <textarea className="input" onChange={(e) => onJSONChange(e.target.value)} placeholder={jsonPlaceholder} rows={4} value={jsonValue} />
          <button className="btn primary" disabled={loading || (!callbackURL.trim() && !jsonValue.trim())} onClick={onComplete} type="button">{loading ? "处理中" : "完成并绑定"}</button>
          <p className="muted">可粘贴本地回调 URL，也可粘贴包含认证参数的 JSON。</p>
        </>
      ) : (
        <>
          {userCode ? <code className="oauth-large-code">{userCode}</code> : null}
          <p className="muted">{notice || "等待认证完成。"}</p>
        </>
      )}
    </div>
  );
}
