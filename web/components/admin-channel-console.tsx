"use client";

import { useEffect, useMemo, useState } from "react";
import { CheckCircle2, Clipboard, Filter, KeyRound, Link2, Plus, RefreshCw, Search, Trash2, X } from "lucide-react";
import { EmptyState, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { Account, Channel, OAuthStatus } from "@/types/api";

const channelDefaults: Record<string, string> = {
  openai: "https://api.openai.com",
  anthropic: "https://api.anthropic.com",
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
  { id: "codex", label: "Codex", type: "openai", apiFormat: "codex", auth: "oauth", endpoint: channelDefaults.openai, models: codexModels, note: "OpenAI Responses API / OAuth" },
  { id: "gemini_code", label: "Gemini Code", type: "gemini", apiFormat: "gemini_code", auth: "oauth", endpoint: channelDefaults.gemini, models: geminiCodeModels, note: "Gemini API / OAuth" },
  { id: "claude_code", label: "Claude Code", type: "anthropic", apiFormat: "claude_code", auth: "oauth", endpoint: channelDefaults.anthropic, models: claudeCodeModels, note: "Claude Code OAuth / Anthropic Messages API" },
  { id: "openai_responses_api", label: "OpenAI Responses API", type: "openai", apiFormat: "responses", auth: "apikey", endpoint: channelDefaults.openai, models: "", note: "OpenAI Responses API" },
  { id: "openai_chat_completions", label: "OpenAI Chat Completions API", type: "openai", apiFormat: "standard", auth: "apikey", endpoint: channelDefaults.openai, models: "", note: "OpenAI Chat Completions API" },
  { id: "gemini_api", label: "Gemini API", type: "gemini", apiFormat: "standard", auth: "apikey", endpoint: channelDefaults.gemini, models: "", note: "Gemini generateContent API" },
  { id: "anthropic_messages", label: "Anthropic Messages API", type: "anthropic", apiFormat: "standard", auth: "apikey", endpoint: channelDefaults.anthropic, models: "", note: "Anthropic Messages API" },
];

const defaultPreset = channelPresets[4];

function createInitialDraft(preset: ChannelPreset = defaultPreset) {
  return { name: "", preset: preset.id, type: preset.type, group: "default", endpoint: preset.endpoint, models: preset.models, apiFormat: preset.apiFormat };
}


function isCodeChannel(channel: Pick<Channel, "api_format">): boolean {
  return channel.api_format === "codex" || channel.api_format === "gemini_code" || channel.api_format === "claude_code";
}

export function AdminChannelConsole() {
  const [channels, setChannels] = useState<Channel[]>([]);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [activeGroup, setActiveGroup] = useState("default");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [detailOpen, setDetailOpen] = useState(false);
  const [error, setError] = useState("");
  const [channelQuery, setChannelQuery] = useState("");
  const [healthFilter, setHealthFilter] = useState<"all" | "healthy" | "warning" | "disabled">("all");
  const [providerFilter, setProviderFilter] = useState("all");
  const [draft, setDraft] = useState(createInitialDraft());
  const [credentialMode, setCredentialMode] = useState<"oauth" | "apikey">("apikey");
  const [apiKeyName, setApiKeyName] = useState("");
  const [apiKeyValue, setApiKeyValue] = useState("");
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

  const token = typeof window !== "undefined" ? window.localStorage.getItem("uapi.admin.token") : "";
  const selected = channels.find((item) => item.id === selectedID) || null;

  function channelGroup(channel: Channel): string {
    return channel.group?.trim() || "default";
  }

  function groupLabel(group: string): string {
    return group === "default" ? "默认渠道" : group;
  }

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

  function accountQuotaItems(account: Account): { label: string; value: string }[] {
    const meta = account.metadata || {};
    const items: { label: string; value: string }[] = [];
    const codexUsage = asRecord(meta.codex_usage);
    if (codexUsage) {
      const text = summarizeCodexUsage(codexUsage);
      if (text) items.push({ label: "Codex 额度", value: text });
    }
    const geminiQuota = asRecord(meta.user_quota);
    if (geminiQuota) {
      const text = summarizeGeminiQuota(geminiQuota);
      if (text) items.push({ label: "Gemini 配额", value: text });
    }
    const paidTier = asRecord(meta.paid_tier);
    const credits = asArray(paidTier?.availableCredits);
    if (credits.length > 0) {
      const text = credits.map((credit) => {
        const row = asRecord(credit);
        return [stringValue(row?.creditType), stringValue(row?.creditAmount)].filter(Boolean).join(" ");
      }).filter(Boolean).join(" · ");
      if (text) items.push({ label: "Gemini Credits", value: text });
    }
    const anthropicUsage = asRecord(meta.usage);
    if (anthropicUsage) {
      const text = summarizeAnthropicUsage(anthropicUsage);
      if (text) items.push({ label: "Anthropic 用量", value: text });
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
      const firstGroup = ch[0] ? channelGroup(ch[0]) : "default";
      setActiveGroup((current) => current === "default" && ch.length > 0 && !ch.some((item) => channelGroup(item) === "default") ? firstGroup : current);
      setSelectedID((current) => current || ch.find((item) => channelGroup(item) === firstGroup)?.id || ch[0]?.id || "");
    }).finally(() => setLoading(false));
  }

  useEffect(() => { loadData(); }, []);

  useEffect(() => {
    if (!selected) return;
    setCredentialMode(isCodeChannel(selected) ? "oauth" : "apikey");
    setApiKeyName(`${selected.type}-account-${channelAccounts(selected.id).length + 1}`);
    setApiKeyValue("");
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

  const channelGroups = useMemo(() => {
    const groups = Array.from(new Set(channels.map((item) => channelGroup(item)))).sort();
    return groups.map((group) => {
      const items = channels.filter((item) => channelGroup(item) === group);
      const enabled = items.filter((item) => item.enabled).length;
      const warning = items.filter((item) => channelHealth(item) === "warning").length;
      return { type: group, label: groupLabel(group), items, enabled, warning };
    });
  }, [channels, accounts]);

  const visibleChannels = useMemo(() => {
    const query = channelQuery.trim().toLowerCase();
    return channels.filter((item) => {
      if (channelGroup(item) !== activeGroup) return false;
      if (providerFilter !== "all" && item.type !== providerFilter) return false;
      if (healthFilter !== "all" && channelHealth(item) !== healthFilter) return false;
      if (!query) return true;
      const haystack = [item.name, item.type, item.group, item.endpoint, item.models, item.api_format, presetForChannel(item).label].join(" ").toLowerCase();
      return haystack.includes(query);
    });
  }, [activeGroup, channels, accounts, channelQuery, healthFilter, providerFilter]);


  function applyPreset(preset: ChannelPreset) {
    setDraft((cur) => ({
      ...cur,
      preset: preset.id,
      type: preset.type,
      endpoint: preset.endpoint,
      models: preset.models,
      apiFormat: preset.apiFormat,
      name: cur.name || ` Channel`,
    }));
    setCredentialMode(preset.auth === "oauth" ? "oauth" : "apikey");
    setOauthState("");
    setOauthStatus(null);
    setOauthAuthURL("");
    setOauthCallbackURL("");
    setOauthJSON("");
    setApiKeyValue("");
  }

  function codePresetForProvider(type: string): ChannelPreset {
    return channelPresets.find((item) => item.auth === "oauth" && item.type === type) || defaultPreset;
  }

  function codeLoginLabel(type: string): string {
    if (type === "openai") return "Codex 登录";
    if (type === "gemini") return "Gemini Code 登录";
    return "Claude Code 登录";
  }

  async function createDraftChannel(selectAfterCreate: boolean, overrides: Partial<typeof draft> = {}): Promise<Channel | null> {
    if (!token || saving) return null;
    setSaving(true);
    setError("");
    const channelDraft = { ...draft, ...overrides };
    try {
      const created = await adminApi.createChannel(token, {
        name: channelDraft.name.trim() || `${channelDraft.type.toUpperCase()} Channel`,
        type: channelDraft.type,
        group: channelDraft.group.trim() || "default",
        endpoint: channelDraft.endpoint.trim() || channelDefaults[channelDraft.type],
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
      setActiveGroup(created.group || "default");
      setDraft(createInitialDraft());
      return created;
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建失败");
      return null;
    } finally {
      setSaving(false);
    }
  }

  async function startNewOAuth() {
    const preset = codePresetForProvider(draft.type);
    const channel = await createDraftChannel(false, { preset: preset.id, apiFormat: preset.apiFormat, type: preset.type, endpoint: draft.endpoint || preset.endpoint, models: preset.models });
    if (!channel || !token) return;
    setCredLoading(true);
    setCredError("");
    setCredNotice("");
    try {
      const started = await adminApi.startChannelOAuth(token, { channel_id: channel.id, provider: channel.type });
      setOauthChannel(channel);
      setOauthState(started.state);
      setOauthMode(started.mode);
      setOauthUserCode(started.user_code || "");
      setOauthAuthURL(started.auth_url);
      setOauthStatus({ state: started.state, provider: channel.type as "openai" | "gemini" | "anthropic", channel_id: channel.id, status: "pending", ready_to_bind: false, created_at: new Date().toISOString() });
      setCredNotice("登录页已打开，完成后把本地回调 URL 或认证 JSON 粘贴回来。");
      window.open(started.auth_url, "_blank", "noopener,noreferrer");
    } catch (err) {
      setCredError(err instanceof Error ? err.message : "发起认证失败");
    } finally {
      setCredLoading(false);
    }
  }

  async function createAPIKeyChannel() {
    if (!token || !apiKeyValue.trim()) return;
    const channel = await createDraftChannel(false);
    if (!channel) return;
    setCredLoading(true);
    setCredError("");
    try {
      const account = await adminApi.createAccount(token, {
        channel_id: channel.id,
        name: apiKeyName.trim() || `${channel.type}-key`,
        credentials: apiKeyValue.trim(),
        weight: 100,
        enabled: true,
      });
      setAccounts((cur) => [account, ...cur]);
      setSelectedID(channel.id);
      closeCreateDrawer();
    } catch (err) {
      setCredError(err instanceof Error ? err.message : "添加失败");
    } finally {
      setCredLoading(false);
    }
  }

  function closeCreateDrawer() {
    setCreateOpen(false);
    setError("");
    setCredError("");
    setCredNotice("");
    setApiKeyValue("");
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
    if (!confirm("确认删除该渠道？关联凭证也会停止使用。")) return;
    setCredError("");
    try {
      await adminApi.deleteChannel(token, id);
      setChannels((cur) => {
        const next = cur.filter((item) => item.id !== id);
        const scoped = next.filter((item) => channelGroup(item) === activeGroup);
        setSelectedID(scoped[0]?.id || next[0]?.id || "");
        return next;
      });
      setAccounts((cur) => cur.filter((item) => item.channel_id !== id));
      setDetailOpen(false);
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
        weight: 100,
        enabled: true,
      });
      setAccounts((cur) => [account, ...cur]);
      setApiKeyName(`${selected.type}-account-${channelAccounts(selected.id).length + 2}`);
      setApiKeyValue("");
      setCredNotice("API Key 已添加。");
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
      const account = await adminApi.bindChannelOAuth(adminToken, {
        state,
        account_name: `${channel.type}-oauth-${channelAccounts(channel.id).length + 1}`,
        weight: 100,
        enabled: true,
      });
      setAccounts((cur) => [account, ...cur]);
      setSelectedID(channel.id);
      setCredNotice("OAuth 凭证已绑定。");
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
        <div className="ops-actions">
          <button className="btn" onClick={loadData} type="button" title="刷新"><RefreshCw /> 刷新</button>
          <button className="btn primary" onClick={() => setCreateOpen(true)} type="button"><Plus /> 新增渠道</button>
        </div>
      </section>

      <section className="channel-workbench">
        <aside className="channel-list card">
          <div className="channel-list-head">
            <h2>渠道分类</h2>
            <span className="badge">{channels.length}</span>
          </div>
          <div className="channel-filter-stack">
            <label className="search-box">
              <Search />
              <input value={channelQuery} onChange={(e) => setChannelQuery(e.target.value)} placeholder="搜索渠道、模型、Endpoint" />
            </label>
            <div className="filter-row">
              <select className="input compact-select" value={providerFilter} onChange={(e) => setProviderFilter(e.target.value)}>
                <option value="all">全部供应商</option>
                <option value="openai">OpenAI</option>
                <option value="anthropic">Anthropic</option>
                <option value="gemini">Gemini</option>
              </select>
              <select className="input compact-select" value={healthFilter} onChange={(e) => setHealthFilter(e.target.value as typeof healthFilter)}>
                <option value="all">全部状态</option>
                <option value="healthy">可用</option>
                <option value="warning">需处理</option>
                <option value="disabled">停用</option>
              </select>
            </div>
          </div>
          <div className="channel-list-items">
            {channelGroups.map((group) => {
              return (
                <button
                  className={`channel-item${activeGroup === group.type ? " active" : ""}`}
                  key={group.type}
                  onClick={() => {
                    setActiveGroup(group.type);
                    setSelectedID(group.items[0]?.id || "");
                    setDetailOpen(false);
                  }}
                  type="button"
                >
                  <span>
                    <strong>{group.label}</strong>
                    <small>{group.enabled} 可用 · {group.warning} 需处理</small>
                  </span>
                  <span className="badge">{group.items.length}</span>
                </button>
              );
            })}
            {channels.length === 0 && !loading ? <EmptyState title="暂无渠道" description="先在上方创建一个渠道。" /> : null}
          </div>
        </aside>

        <section className="channel-detail card">
          <div className="channel-grid-head">
            <div>
              <h2>{channelGroups.find((group) => group.type === activeGroup)?.label || groupLabel(activeGroup)}</h2>
              <p className="muted">点击渠道方块打开抽屉，查看详情、编辑凭证和开关状态。</p>
            </div>
            <span className="badge">{visibleChannels.length} 个渠道</span>
          </div>

          <div className="channel-tile-grid">
            {visibleChannels.map((channel) => {
              const related = channelAccounts(channel.id);
              const count = related.length;
              const enabledAccounts = related.filter((account) => account.enabled).length;
              const health = channelHealth(channel);
              return (
                <div
                  className={`channel-tile ${health}${selected?.id === channel.id ? " active" : ""}`}
                  key={channel.id}
                  onClick={() => {
                    setSelectedID(channel.id);
                    setDetailOpen(true);
                  }}
                  role="button"
                  tabIndex={0}
                >
                  <span className="channel-tile-top">
                    <span className={`health-dot ${health}`} />
                    <strong>{channel.name}</strong>
                  </span>
                  <small>{channel.endpoint}</small>
                  <span className="channel-tile-meta">
                    <span>{enabledAccounts}/{count} 凭证</span>
                    <span>{presetForChannel(channel).label}</span>
                  </span>
                  <span className="channel-tile-actions" onClick={(event) => event.stopPropagation()}>
                    <button className="btn subtle icon-only" onClick={() => patchChannel(channel.id, { enabled: !channel.enabled })} title={channel.enabled ? "停用" : "启用"} type="button"><Filter /></button>
                    <button className="btn subtle icon-only" onClick={() => { setSelectedID(channel.id); setDetailOpen(true); }} title="添加凭证" type="button"><Plus /></button>
                    <button className="btn subtle icon-only" onClick={() => navigator.clipboard?.writeText(channel.endpoint)} title="复制 Endpoint" type="button"><Clipboard /></button>
                  </span>
                </div>
              );
            })}
            {visibleChannels.length === 0 && !loading ? <EmptyState title="暂无渠道" description="这个分类下还没有渠道。" /> : null}
          </div>

          {visibleChannels.length === 0 && loading ? <EmptyState title="加载中" description="正在加载渠道。" /> : null}
        </section>
      </section>

      {detailOpen && selected ? (
        <div className="drawer-backdrop" role="presentation">
          <aside aria-label="渠道配置" className="side-drawer channel-config-drawer">
            <div className="drawer-head">
              <div>
                <p className="eyebrow">{presetForChannel(selected).label}</p>
                <h2>{selected.name}</h2>
              </div>
              <button className="btn" onClick={() => setDetailOpen(false)} title="关闭" type="button"><X /></button>
            </div>

            <div className="drawer-body">
              <div className="resource-title">
                <StatusBadge value={selected.enabled ? "enabled" : "disabled"} />
                <span className="badge">{groupLabel(channelGroup(selected))}</span>
                <span className="badge">{selected.api_format || "standard"}</span>
              </div>
              <div className="channel-edit-grid">
                <input className="input" defaultValue={selected.name} key={`name-${selected.id}`} onBlur={(e) => {
                  const value = e.target.value.trim();
                  if (value && value !== selected.name) patchChannel(selected.id, { name: value });
                }} placeholder="渠道名称" />
                <input className="input" defaultValue={channelGroup(selected)} key={`group-${selected.id}`} onBlur={(e) => {
                  const value = e.target.value.trim() || "default";
                  if (value !== channelGroup(selected)) {
                    patchChannel(selected.id, { group: value });
                    setActiveGroup(value);
                  }
                }} placeholder="默认渠道" />
                <input className="input wide" defaultValue={selected.endpoint} key={`endpoint-${selected.id}`} onBlur={(e) => {
                  const value = e.target.value.trim();
                  if (value && value !== selected.endpoint) patchChannel(selected.id, { endpoint: value });
                }} placeholder="Endpoint" />
                <input className="input wide" defaultValue={selected.models} key={`models-${selected.id}`} onBlur={(e) => {
                  const value = e.target.value.trim();
                  if (value !== selected.models) patchChannel(selected.id, { models: value });
                }} placeholder="模型列表，留空表示不限制" />
              </div>
              <div className="resource-actions">
                <button className="btn" onClick={() => patchChannel(selected.id, { enabled: !selected.enabled })} type="button">{selected.enabled ? "停用" : "启用"}</button>
                <button className="btn danger" onClick={() => deleteChannel(selected.id)} type="button"><Trash2 /> 删除</button>
              </div>

              <div className="credential-editor">
                <div className="section-head">
                  <div><h2>添加凭证</h2><p className="muted">凭证添加后立即进入 Gateway 调度池。</p></div>
                  {(selected.type === "openai" || selected.type === "gemini" || selected.type === "anthropic") && !isCodeChannel(selected) ? (
                    <div className="segmented">
                      <button className={credentialMode === "apikey" ? "active" : ""} onClick={() => setCredentialMode("apikey")} type="button"><KeyRound /> API Key</button>
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
                    <input className="input" value={apiKeyValue} onChange={(e) => setApiKeyValue(e.target.value)} placeholder="API Key / Token" type="password" />
                    <button className="btn primary" disabled={credLoading || !apiKeyValue.trim()} onClick={addApiKey} type="button"><Plus /> 添加</button>
                  </div>
                ) : null}
                {credNotice && credentialMode !== "oauth" ? <p className="form-success">{credNotice}</p> : null}
                {credError ? <p className="form-error">{credError}</p> : null}
              </div>

              <div className="credential-list-panel">
                <h2>凭证池</h2>
                <div className="credential-list">
                  {channelAccounts(selected.id).map((account) => (
                    <div className="credential-row" key={account.id}>
                      <span>
                        {account.cred_type === "oauth_token" ? <Link2 /> : <KeyRound />}
                        <span className="credential-main">
                          <strong>{account.name}</strong>
                          {accountMetaSummary(account) ? <small>{accountMetaSummary(account)}</small> : null}
                          {accountValidation(account) ? (
                            <small className="credential-warning">
                              {accountValidation(account)?.message}
                              {accountValidation(account)?.url ? <a href={accountValidation(account)?.url} rel="noreferrer" target="_blank">打开验证</a> : null}
                            </small>
                          ) : null}
                          {accountQuotaItems(account).length > 0 ? (
                            <span className="credential-quota">
                              {accountQuotaItems(account).map((item) => (
                                <small key={`${item.label}-${item.value}`}><b>{item.label}</b>{item.value}</small>
                              ))}
                            </span>
                          ) : null}
                        </span>
                      </span>
                      <button className="btn subtle" onClick={() => patchAccount(account.id, { enabled: !account.enabled })} type="button"><StatusBadge value={account.enabled ? "enabled" : "disabled"} /></button>
                      <button className="btn danger" onClick={() => deleteAccount(account.id)} title="删除凭证" type="button"><X /></button>
                    </div>
                  ))}
                  {channelAccounts(selected.id).length === 0 ? <EmptyState title="暂无凭证" description="先添加 OAuth 或 API Key。" /> : null}
                </div>
              </div>
            </div>
          </aside>
        </div>
      ) : null}

      {createOpen ? (
        <div className="drawer-backdrop" role="presentation">
          <aside aria-label="新增渠道" className="side-drawer">
            <div className="drawer-head">
              <div>
                <p className="eyebrow">New Channel</p>
                <h2>新增渠道</h2>
              </div>
              <button className="btn" onClick={closeCreateDrawer} title="关闭" type="button"><X /></button>
            </div>

            <div className="drawer-body">
              <div className="preset-grid provider-pick-grid">
                {channelPresets.map((preset) => (
                  <button className={draft.preset === preset.id ? "active" : ""} key={preset.id} onClick={() => applyPreset(preset)} type="button">
                    <strong>{preset.label}</strong>
                    <small>{preset.note}</small>
                    <span className={`preset-auth `}>{preset.auth === "oauth" ? "OAuth" : "API Key"}</span>
                  </button>
                ))}
              </div>

              <div className="field">
                <label>渠道名称</label>
                <input className="input" value={draft.name} onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} placeholder={`${channelPresets.find((item) => item.id === draft.preset)?.label || draft.type} Channel`} />
              </div>

              <div className="field">
                <label>渠道分组</label>
                <input className="input" value={draft.group} onChange={(e) => setDraft((d) => ({ ...d, group: e.target.value }))} placeholder="默认渠道" />
              </div>

              <div className="field">
                <label>Endpoint</label>
                <input className="input" value={draft.endpoint} onChange={(e) => setDraft((d) => ({ ...d, endpoint: e.target.value }))} />
              </div>

              {draft.type === "openai" ? (
                <div className="field">
                  <label>API 类型</label>
                  <div className="segmented">
                    <button className={draft.apiFormat === "standard" ? "active" : ""} onClick={() => setDraft((d) => ({ ...d, apiFormat: "standard" }))} type="button">Chat Completions</button>
                    <button className={draft.apiFormat === "responses" ? "active" : ""} onClick={() => setDraft((d) => ({ ...d, apiFormat: "responses" }))} type="button">Responses API</button>
                  </div>
                </div>
              ) : null}

              <div className="drawer-oauth">
                <div className="field">
                  <label>API Key</label>
                  <input className="input" value={apiKeyValue} onChange={(e) => setApiKeyValue(e.target.value)} placeholder={draft.type === "anthropic" ? "sk-ant-..." : "API Key"} type="password" />
                </div>
                <button className="btn primary" disabled={saving || credLoading || !apiKeyValue.trim()} onClick={createAPIKeyChannel} type="button">
                  {saving || credLoading ? "正在创建" : "创建 API 渠道"}
                </button>
              </div>

              <div className="drawer-oauth code-login-panel">
                {!oauthStatus ? (
                  <>
                    <button className="btn" disabled={saving || credLoading} onClick={startNewOAuth} type="button">
                      <Link2 /> {credLoading || saving ? "正在发起" : codeLoginLabel(draft.type)}
                    </button>
                    <p className="muted">使用账号登录创建 {codeLoginLabel(draft.type).replace(" 登录", "")} OAuth 渠道，后续通过刷新 token 保持可用。</p>
                  </>
                ) : (
                  <OAuthPanel
                    channel={oauthChannel || ({ id: "", name: draft.name, type: draft.type, group: draft.group || "default", endpoint: draft.endpoint, enabled: true, models: "", priority: 100, api_format: codePresetForProvider(draft.type).apiFormat, force_stream: false, affinity_ttl: 0, created_at: "" } as Channel)}
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
                    onStart={startNewOAuth}
                    onComplete={completeOAuth}
                  />
                )}
                {credNotice && oauthStatus ? <p className="form-success">{credNotice}</p> : null}
              </div>

              {error ? <p className="form-error">{error}</p> : null}
              {credError ? <p className="form-error">{credError}</p> : null}
            </div>
          </aside>
        </div>
      ) : null}
    </>
  );
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
  return buckets.slice(0, 4).map((bucket) => {
    const model = stringValue(bucket.modelId) || stringValue(bucket.tokenType) || "all";
    const remaining = stringValue(bucket.remainingAmount) || fractionPercentValue(bucket.remainingFraction);
    const reset = stringValue(bucket.resetTime);
    return [model, remaining ? `剩余 ${remaining}` : "", reset ? `重置 ${reset}` : ""].filter(Boolean).join(" ");
  }).join(" · ");
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
