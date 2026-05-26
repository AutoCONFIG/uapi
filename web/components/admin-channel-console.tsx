"use client";

import { useEffect, useMemo, useState } from "react";
import { CheckCircle2, Clipboard, KeyRound, Pencil, Plus, Power, RefreshCw, Trash2, X } from "lucide-react";
import { EmptyState, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { Account, Channel, OAuthStatus } from "@/types/api";

const channelDefaults: Record<string, string> = {
  openai: "https://api.openai.com/v1",
  anthropic: "https://api.anthropic.com/v1",
  gemini: "https://generativelanguage.googleapis.com/v1beta",
};

const oauthChannelDefaults: Record<string, string> = {
  openai: "https://api.openai.com/v1",
  anthropic: "https://api.anthropic.com/v1",
  gemini: "https://generativelanguage.googleapis.com",
  antigravity: "https://cloudcode-pa.googleapis.com",
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
const antigravityModels = "claude-opus-4-6-thinking,claude-sonnet-4-6,gemini-3-pro-high,gemini-3-pro-low,gemini-pro-agent,gemini-3.1-pro-low,gemini-3-flash,gemini-3-flash-agent,gemini-3.1-flash-lite,gpt-oss-120b-medium";

const channelPresets: ChannelPreset[] = [
  { id: "antigravity", label: "Antigravity", type: "antigravity", apiFormat: "antigravity", auth: "oauth", endpoint: oauthChannelDefaults.antigravity, models: antigravityModels, note: "Google Antigravity OAuth" },
  { id: "codex", label: "Codex", type: "openai", apiFormat: "codex", auth: "oauth", endpoint: oauthChannelDefaults.openai, models: codexModels, note: "OpenAI Responses API / OAuth" },
  { id: "gemini_code", label: "Gemini Code", type: "gemini", apiFormat: "gemini_code", auth: "oauth", endpoint: oauthChannelDefaults.gemini, models: geminiCodeModels, note: "Gemini API / OAuth" },
  { id: "claude_code", label: "Claude Code", type: "anthropic", apiFormat: "claude_code", auth: "oauth", endpoint: oauthChannelDefaults.anthropic, models: claudeCodeModels, note: "Claude Code OAuth / Anthropic Messages API" },
  { id: "openai_responses_api", label: "OpenAI Responses API", type: "openai", apiFormat: "responses", auth: "apikey", endpoint: channelDefaults.openai, models: "", note: "OpenAI Responses API" },
  { id: "openai_chat_completions", label: "OpenAI Chat Completions API", type: "openai", apiFormat: "standard", auth: "apikey", endpoint: channelDefaults.openai, models: "", note: "OpenAI Chat Completions API" },
  { id: "gemini_api", label: "Gemini API", type: "gemini", apiFormat: "standard", auth: "apikey", endpoint: channelDefaults.gemini, models: "", note: "Gemini generateContent API" },
  { id: "anthropic_messages", label: "Anthropic Messages API", type: "anthropic", apiFormat: "standard", auth: "apikey", endpoint: channelDefaults.anthropic, models: "", note: "Anthropic Messages API" },
];

const defaultPreset = channelPresets[5];

function createInitialDraft(preset: ChannelPreset = defaultPreset) {
  return { name: "", preset: preset.id, type: preset.type, models: preset.models, apiFormat: preset.apiFormat };
}

function defaultEndpointForChannel(channel: Pick<Channel, "type" | "api_format" | "endpoint">): string {
  return channel.endpoint || channelPresets.find((preset) => preset.type === channel.type && preset.apiFormat === channel.api_format)?.endpoint || channelDefaults[channel.type] || "";
}

function splitEndpoint(endpoint: string, fallback: string): { baseURL: string; path: string } {
  const value = endpoint.trim() || fallback;
  try {
    const parsed = new URL(value);
    const path = parsed.pathname === "/" ? "" : parsed.pathname;
    return { baseURL: parsed.origin, path };
  } catch {
    return { baseURL: value.replace(/\/+(v1beta|v1)\/?$/i, ""), path: "" };
  }
}

function defaultEndpointPath(channel: Pick<Channel, "type" | "api_format" | "endpoint">): string {
  return splitEndpoint(defaultEndpointForChannel(channel), "").path || (channel.type === "gemini" ? "/v1beta" : "/v1");
}

function composeEndpoint(baseURL: string, path: string, defaultPath: string): string {
  const base = baseURL.trim().replace(/\/+$/, "");
  if (!base) return "";
  const route = (path.trim() || defaultPath).replace(/^\/*/, "");
  return route ? `${base}/${route}` : base;
}

function isOAuthChannel(channel: Pick<Channel, "api_format">): boolean {
  return channel.api_format === "codex" || channel.api_format === "gemini_code" || channel.api_format === "claude_code" || channel.api_format === "antigravity";
}

function supportsModelSync(channel: Pick<Channel, "api_format">): boolean {
  return !channel.api_format || channel.api_format === "standard" || channel.api_format === "responses" || channel.api_format === "antigravity";
}

function presetTitleLines(preset: ChannelPreset): [string, string] {
  const map: Record<string, [string, string]> = {
    antigravity: ["Google", "Antigravity"],
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
  const [apiKeyBaseURL, setApiKeyBaseURL] = useState("");
  const [apiKeyPath, setApiKeyPath] = useState("");
  const [credLoading, setCredLoading] = useState(false);
  const [modelSyncing, setModelSyncing] = useState(false);
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
    if (channel.api_format === "antigravity") return channelPresets[0];
    if (channel.api_format === "codex") return channelPresets[1];
    if (channel.api_format === "gemini_code") return channelPresets[2];
    if (channel.api_format === "claude_code") return channelPresets[3];
    if (channel.type === "openai" && channel.api_format === "responses") return channelPresets[4];
    if (channel.type === "openai") return channelPresets[5];
    if (channel.type === "gemini") return channelPresets[6];
    if (channel.type === "anthropic") return channelPresets[7];
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

  function accountQuotaItems(account: Account): QuotaDisplayItem[] {
    return buildQuotaDisplayItems(account);
  }

  useEffect(() => {
    if (!token) { setLoading(false); return; }
    let cancelled = false;
    setLoading(true);
    Promise.all([
      adminApi.channels(token, 1, 500).then((r) => r.items).catch(() => []),
      adminApi.accounts(token, 1, 1000).then((r) => r.items).catch(() => []),
    ]).then(([ch, ac]) => {
      if (cancelled) return;
      setChannels(ch);
      setAccounts(ac);
      setSelectedID((current) => current || ch[0]?.id || "");
    }).finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    if (!selected) return;
    setCredentialMode(isOAuthChannel(selected) ? "oauth" : "apikey");
    const endpoint = splitEndpoint("", defaultEndpointForChannel(selected));
    setApiKeyName(`${selected.type}-account-${channelAccounts(selected.id).length + 1}`);
    setApiKeyValue("");
    setApiKeyBaseURL(endpoint.baseURL);
    setApiKeyPath(endpoint.path);
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
    setApiKeyBaseURL("");
    setApiKeyPath("");
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

  async function syncChannelModels(channel: Channel) {
    if (!token || modelSyncing || !supportsModelSync(channel)) return;
    setModelSyncing(true);
    setCredError("");
    setCredNotice("");
    try {
      const result = await adminApi.syncChannelModels(token, channel.id);
      setChannels((cur) => cur.map((item) => item.id === channel.id ? result.channel : item));
      setCredNotice(`已从上游获取 ${result.count} 个模型。`);
    } catch (err) {
      setCredError(err instanceof Error ? err.message : "获取上游模型失败");
    } finally {
      setModelSyncing(false);
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
        endpoint: composeEndpoint(apiKeyBaseURL, apiKeyPath, defaultEndpointPath(selected)) || defaultEndpointForChannel(selected),
        weight: 100,
        enabled: true,
      });
      setAccounts((cur) => [account, ...cur]);
      const endpoint = splitEndpoint("", defaultEndpointForChannel(selected));
      setApiKeyName(`${selected.type}-account-${channelAccounts(selected.id).length + 2}`);
      setApiKeyValue("");
      setApiKeyBaseURL(endpoint.baseURL);
      setApiKeyPath(endpoint.path);
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
      setOauthStatus({ state: started.state, provider: selected.type as OAuthStatus["provider"], channel_id: selected.id, status: "pending", ready_to_bind: false, created_at: new Date().toISOString() });
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
    const nextBaseURL = String(data.get("endpoint_base_url") || "").trim();
    const nextPath = String(data.get("endpoint_path") || "").trim();
    if (account.cred_type === "api_key" && selected) {
      body.endpoint = composeEndpoint(nextBaseURL, nextPath, defaultEndpointPath(selected)) || defaultEndpointForChannel(selected);
    }
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
                  <button className="btn" disabled={modelSyncing || selectedAccounts.length === 0 || !supportsModelSync(selected)} onClick={() => syncChannelModels(selected)} title={supportsModelSync(selected) ? "从上游同步模型" : "此 OAuth 渠道使用预置模型列表"} type="button"><RefreshCw /> {modelSyncing ? "获取中" : "获取模型"}</button>
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
                          {quotaItems.slice(0, 3).map((item) => (
                            <div className="quota-compact-item" key={item.key} title={item.detail || item.label}>
                              <div className="quota-compact-header">
                                <span className="quota-label">{item.label}</span>
                                <span className={`quota-percent ${quotaTone(item.remainingPercent)}`}>{item.remainingPercent}%</span>
                              </div>
                              <div className="quota-compact-bar-track">
                                <div className={`quota-compact-bar ${quotaTone(item.remainingPercent)}`} style={{ width: `${item.remainingPercent}%` }} />
                              </div>
                              {item.resetText ? <span className="quota-compact-reset">{item.resetText}</span> : null}
                            </div>
                          ))}
                        </div>
                      ) : null}
                      {validation ? <a className="account-card-warning" href={validation.url || undefined} rel="noreferrer" target="_blank">{validation.message}</a> : null}
                      <div className="account-card-actions">
                        <button className="btn subtle" onClick={() => { setSelectedAccountID(account.id); setExportPassword(""); setExportData(null); setAccountEditOpen(true); }} type="button"><Pencil /> 编辑</button>
                        <button className="btn subtle" onClick={() => patchAccount(account.id, { enabled: !account.enabled })} type="button">{account.enabled ? "停用" : "启用"}</button>
                        <button className="btn danger icon-only" onClick={() => deleteAccount(account.id)} title="删除账号" type="button"><X /></button>
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
                {selectedAccount.cred_type === "api_key" ? (() => {
                  const endpoint = splitEndpoint(selectedAccount.endpoint || "", defaultEndpointForChannel(selected));
                  return (
                    <div className="endpoint-split-row">
                      <div className="field">
                        <label>Base URL</label>
                        <input className="input" name="endpoint_base_url" defaultValue={endpoint.baseURL} placeholder="https://api.example.com" />
                      </div>
                      <div className="field endpoint-path-field">
                        <label>路径</label>
                        <input className="input" name="endpoint_path" defaultValue={endpoint.path} placeholder={defaultEndpointPath(selected)} />
                      </div>
                    </div>
                  );
                })() : null}
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
                <div className="model-sync-row">
                  <input className="input wide" defaultValue={selected.models} key={`drawer-models-${selected.id}`} onBlur={(e) => {
                    const value = e.target.value.trim();
                    if (value !== selected.models) patchChannel(selected.id, { models: value });
                  }} placeholder="模型列表，留空表示不限制" />
                  <button className="btn" disabled={modelSyncing || selectedAccounts.length === 0 || !supportsModelSync(selected)} onClick={() => syncChannelModels(selected)} title={supportsModelSync(selected) ? "从上游同步模型" : "此 OAuth 渠道使用预置模型列表"} type="button"><RefreshCw /> {modelSyncing ? "获取中" : "从上游获取"}</button>
                </div>
                <textarea className="input wide" defaultValue={selected.model_aliases || ""} key={`drawer-model-aliases-${selected.id}`} onBlur={(e) => {
                  const value = e.target.value.trim();
                  if (value !== (selected.model_aliases || "")) patchChannel(selected.id, { model_aliases: value });
                }} placeholder={"模型重定向，每行 上游模型=对外模型，例如 gemini-pro-agent=gemini-3.1-pro"} rows={4} />
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
                  {(selected.type === "openai" || selected.type === "gemini" || selected.type === "anthropic") && !isOAuthChannel(selected) ? (
                    <div className="segmented">
                      <button className={credentialMode === "apikey" ? "active" : ""} onClick={() => setCredentialMode("apikey")} type="button"><KeyRound /> 密钥</button>
                    </div>
                  ) : null}
                </div>

                {isOAuthChannel(selected) ? (
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
                    <input className="input" value={apiKeyBaseURL} onChange={(e) => setApiKeyBaseURL(e.target.value)} placeholder="https://api.example.com" />
                    <input className="input" value={apiKeyPath} onChange={(e) => setApiKeyPath(e.target.value)} placeholder={defaultEndpointPath(selected)} />
                    <div className="api-key-secret-row">
                      <input className="input" value={apiKeyValue} onChange={(e) => setApiKeyValue(e.target.value)} placeholder="密钥 / 令牌" type="password" />
                      <button className="btn primary" disabled={credLoading || !apiKeyValue.trim()} onClick={addApiKey} type="button"><Plus /> 添加</button>
                    </div>
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


              {draft.type === "openai" && !isOAuthChannel({ api_format: draft.apiFormat } as Channel) ? (
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
              <p className="muted">渠道只定义供应商、协议和模型范围；账号接入点由 Base URL 和路径组成，路径留空时使用标准路由。</p>

              {error ? <p className="form-error">{error}</p> : null}
            </div>
          </aside>
        </div>
      ) : null}
    </>
  );
}


type QuotaDisplayItem = {
  key: string;
  label: string;
  remainingPercent: number;
  resetText?: string;
  detail?: string;
};

function buildQuotaDisplayItems(account: Account): QuotaDisplayItem[] {
  const meta = account.metadata || {};
  const items: QuotaDisplayItem[] = [];
  const codexUsage = asRecord(meta.codex_usage);
  if (codexUsage) {
    const limits = asRecord(codexUsage.rate_limits) || asRecord(codexUsage.rateLimits) || codexUsage;
    addUsageLimit(items, "codex-primary", "Codex 主窗口", asRecord(limits.primary));
    addUsageLimit(items, "codex-secondary", "Codex 次窗口", asRecord(limits.secondary));
    const credits = asRecord(limits.credits);
    if (credits) {
      const balance = stringValue(credits.balance) || (credits.unlimited === true ? "unlimited" : "");
      items.push({ key: "codex-credits", label: "Credits", remainingPercent: credits.unlimited === true ? 100 : 0, detail: balance ? `Credits ${balance}` : "" });
    }
  }
  const geminiQuota = asRecord(meta.user_quota);
  if (geminiQuota) {
    const buckets = asArray(geminiQuota.buckets).map(asRecord).filter(Boolean) as Record<string, unknown>[];
    for (const [index, bucket] of buckets.entries()) {
      const remaining = numberValue(bucket.remainingFraction);
      const amount = stringValue(bucket.remainingAmount);
      if (remaining === null && !amount) continue;
      const percent = remaining !== null ? Math.round(clampPercent(remaining * 100)) : 0;
      const reset = stringValue(bucket.resetTime);
      items.push({
        key: `gemini-${index}`,
        label: quotaBucketLabel(bucket, index + 1),
        remainingPercent: percent,
        resetText: formatResetTimeShort(reset),
        detail: [amount ? `剩余 ${amount}` : `剩余 ${percent}%`, reset ? `重置 ${reset}` : ""].filter(Boolean).join(" · "),
      });
    }
  }
  const geminiCredits = asRecord(meta.credits) || asRecord(meta.credit_balance);
  if (geminiCredits) {
    const balance = stringValue(geminiCredits.balance) || stringValue(geminiCredits.remaining) || stringValue(geminiCredits.amount);
    if (balance) items.push({ key: "gemini-credits", label: "Credits", remainingPercent: 100, detail: `Credits ${balance}` });
  }
  const anthropicUsage = asRecord(meta.usage);
  if (anthropicUsage) {
    for (const key of ["five_hour", "seven_day", "seven_day_sonnet", "seven_day_opus", "seven_day_oauth_apps"]) {
      addUsageLimit(items, `anthropic-${key}`, key.replaceAll("_", " "), asRecord(anthropicUsage[key]));
    }
  }
  return items.sort((left, right) => left.remainingPercent - right.remainingPercent);
}

function addUsageLimit(items: QuotaDisplayItem[], key: string, label: string, row: Record<string, unknown> | null) {
  if (!row) return;
  const used = numberValue(row.used_percent ?? row.usedPercent ?? row.utilization);
  if (used === null) return;
  const usedPercent = normalizePercent(used);
  const remainingPercent = Math.round(clampPercent(100 - usedPercent));
  const reset = stringValue(row.resets_at ?? row.reset_at ?? row.resetAt ?? row.resetTime);
  items.push({
    key,
    label,
    remainingPercent,
    resetText: formatResetTimeShort(reset),
    detail: [`剩余 ${remainingPercent}%`, reset ? `重置 ${reset}` : ""].filter(Boolean).join(" · "),
  });
}

function quotaBucketLabel(bucket: Record<string, unknown>, index: number): string {
  return stringValue(bucket.model) || stringValue(bucket.name) || stringValue(bucket.type) || `额度桶 ${index}`;
}

function quotaTone(percent: number): string {
  if (percent >= 50) return "high";
  if (percent > 0) return "medium";
  return "low";
}

function clampPercent(value: number): number {
  return Math.max(0, Math.min(100, value));
}

function formatResetTimeShort(raw: string): string {
  if (!raw) return "";
  const reset = new Date(raw);
  if (Number.isNaN(reset.getTime())) return raw;
  const diffMs = reset.getTime() - Date.now();
  if (diffMs <= 0) return "已重置";
  const totalMinutes = Math.max(1, Math.floor(diffMs / 60000));
  const days = Math.floor(totalMinutes / 1440);
  const hours = Math.floor((totalMinutes % 1440) / 60);
  const minutes = totalMinutes % 60;
  const relative = [days ? `${days}天` : "", hours ? `${hours}时` : "", !days && minutes ? `${minutes}分` : ""].filter(Boolean).join(" ");
  const month = String(reset.getMonth() + 1).padStart(2, "0");
  const day = String(reset.getDate()).padStart(2, "0");
  const hh = String(reset.getHours()).padStart(2, "0");
  const mm = String(reset.getMinutes()).padStart(2, "0");
  return `${relative || "<1分"}后 (${month}/${day} ${hh}:${mm})`;
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
      : channel.type === "antigravity"
        ? "http://localhost:51121/oauth-callback?code=...&state=..."
        : "https://platform.claude.com/oauth/code/callback?code=...&state=...";
  const jsonPlaceholder = channel.type === "openai"
    ? "{\"callback_url\":\"http://localhost:1455/auth/callback?code=...&state=...\"}"
    : channel.type === "gemini"
      ? "{\"access_token\":\"ya29...\",\"refresh_token\":\"1//...\",\"expiry_date\":1710000000000}"
      : channel.type === "antigravity"
        ? "{\"access_token\":\"ya29...\",\"refresh_token\":\"1//...\",\"expiry_date\":1710000000000}"
        : "{\"callback_url\":\"https://platform.claude.com/oauth/code/callback?code=...&state=...\"}";
  const providerHint = channel.type === "openai"
    ? "按 Codex OAuth 方式认证。"
    : channel.type === "gemini"
      ? "按 Gemini Code OAuth 方式认证。"
      : channel.type === "antigravity"
        ? "按 Antigravity OAuth 方式认证。"
        : "按 Claude Code OAuth 方式认证。";

  if (!status) {
    return (
      <div className="oauth-inline">
        <p className="muted">{providerHint}</p>
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
