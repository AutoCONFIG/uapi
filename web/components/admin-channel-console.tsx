"use client";

import { useEffect, useMemo, useState } from "react";
import { CheckCircle2, Clipboard, Eye, EyeOff, KeyRound, Pencil, Plus, Power, RefreshCw, Trash2, X } from "lucide-react";
import { EmptyState, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import { apiKeyChannelPresets, channelDefaults, channelPresets, defaultChannelPreset, isOAuthAPIFormat, oauthProviderForChannel, presetForChannel, presetTitleLines, oauthChannelPresets, type ChannelPreset } from "@/lib/channel-presets";
import type { Account, Channel, OAuthStatus } from "@/types/api";

function createInitialDraft(preset: ChannelPreset = defaultChannelPreset) {
  return { name: "", preset: preset.id, type: preset.type, models: preset.models, modelAliases: preset.modelAliases || "", apiFormat: preset.apiFormat };
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
  return isOAuthAPIFormat(channel.api_format);
}

function supportsModelSync(channel: Pick<Channel, "api_format">): boolean {
  return !channel.api_format || channel.api_format === "standard" || channel.api_format === "responses" || channel.api_format === "antigravity";
}

function modelValues(raw: string): string[] {
  const seen = new Set<string>();
  const values: string[] = [];
  raw.split(/[\s,]+/).map((item) => item.trim()).filter(Boolean).forEach((item) => {
    if (seen.has(item)) return;
    seen.add(item);
    values.push(item);
  });
  return values;
}

function modelCSV(values: string[]): string {
  return modelValues(values.join(",")).join(",");
}

function mergeModelCSV(current: string, extra: string): string {
  return modelCSV([...modelValues(current), ...modelValues(extra)]);
}

type ModelAliasMaps = {
  upstreamToPublic: Map<string, string>;
  publicToUpstream: Map<string, string>;
};

function parseModelAliases(raw: string): ModelAliasMaps {
  const upstreamToPublic = new Map<string, string>();
  const publicToUpstream = new Map<string, string>();
  for (const entry of raw.replace(/\r\n/g, "\n").replace(/;/g, "\n").split(/[\n,]/)) {
    const trimmed = entry.trim();
    if (!trimmed) continue;
    const sep = ["=>", "=", ":"].find((item) => trimmed.includes(item));
    if (!sep) continue;
    const [publicPart, ...rest] = trimmed.split(sep);
    const publicID = publicPart.trim();
    const upstream = rest.join(sep).trim();
    if (!publicID || !upstream || publicToUpstream.has(publicID)) continue;
    publicToUpstream.set(publicID, upstream);
    if (!upstreamToPublic.has(upstream)) upstreamToPublic.set(upstream, publicID);
  }
  return { upstreamToPublic, publicToUpstream };
}

function modelAliasesText(aliases: ModelAliasMaps): string {
  return [...aliases.publicToUpstream.entries()].map(([publicID, upstream]) => `${publicID}=${upstream}`).join("\n");
}

function modelRedirectsText(aliases: ModelAliasMaps): string {
  return [...aliases.publicToUpstream.entries()]
    .filter(([publicID, upstream]) => publicID !== upstream)
    .map(([publicID, upstream]) => `${publicID}=${upstream}`)
    .join("\n");
}

type AntigravitySettings = {
  thinking_routing: boolean;
  tier_fallback: boolean;
  medium_token_threshold: number;
  long_token_threshold: number;
  tier_groups: AntigravityTierGroup[];
};

type AntigravityTierGroup = {
  public_model: string;
  route_type: "public" | "redirect" | "tier";
  aliases: string[];
  high: string;
  medium: string;
  low: string;
  fallback_order: string[];
};

type AntigravityTierKey = "high" | "medium" | "low";

const antigravityTierDefs: Array<{ key: AntigravityTierKey; label: string; placeholder: string }> = [
  { key: "high", label: "High", placeholder: "gemini-3-flash" },
  { key: "medium", label: "Medium", placeholder: "gemini-3.5-flash-medium" },
  { key: "low", label: "Low", placeholder: "gemini-3.5-flash-low" },
];

function antigravitySettings(raw?: string): AntigravitySettings {
  const fallback = defaultAntigravitySettings(false);
  if (!raw) return fallback;
  try {
    const parsed = JSON.parse(raw);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return fallback;
    return {
      thinking_routing: typeof parsed.thinking_routing === "boolean" ? parsed.thinking_routing : fallback.thinking_routing,
      tier_fallback: typeof parsed.tier_fallback === "boolean" ? parsed.tier_fallback : fallback.tier_fallback,
      medium_token_threshold: Number(parsed.medium_token_threshold || fallback.medium_token_threshold),
      long_token_threshold: Number(parsed.long_token_threshold || fallback.long_token_threshold),
      tier_groups: parseTierGroups(parsed.tier_groups),
    };
  } catch {
    return fallback;
  }
}

function defaultAntigravitySettings(enabled = true): AntigravitySettings {
  return {
    thinking_routing: enabled,
    tier_fallback: enabled,
    medium_token_threshold: 8000,
    long_token_threshold: 32000,
    tier_groups: [
      { public_model: "gemini-3.5-flash", route_type: "tier", aliases: ["gemini-3-flash"], high: "gemini-3-flash", medium: "gemini-3.5-flash-medium", low: "gemini-3.5-flash-low", fallback_order: ["medium", "low", "high"] },
      { public_model: "gemini-3.1-pro", route_type: "tier", aliases: ["gemini-3.1-pro-high", "gemini-3-pro-high", "gemini-3-pro-low"], high: "gemini-pro-agent", medium: "", low: "gemini-3.1-pro-low", fallback_order: ["low", "high"] },
      { public_model: "gemini-3-pro", route_type: "public", aliases: [], high: "gemini-3-pro", medium: "", low: "", fallback_order: ["high"] },
      { public_model: "claude-opus-4-6", route_type: "redirect", aliases: [], high: "claude-opus-4-6-thinking", medium: "", low: "", fallback_order: ["high"] },
      { public_model: "claude-sonnet-4-6", route_type: "public", aliases: ["claude-sonnet-4-6-thinking"], high: "claude-sonnet-4-6", medium: "", low: "", fallback_order: ["high"] },
      { public_model: "gpt-oss-120b", route_type: "tier", aliases: [], high: "gpt-oss-120b", medium: "", low: "gpt-oss-120b-medium", fallback_order: ["low", "high"] },
      { public_model: "nano-banana-2", route_type: "tier", aliases: ["gemini-3-pro-image", "gemini-3-pro-image-preview"], high: "gemini-3.1-flash-image", medium: "", low: "gemini-3-pro-image", fallback_order: ["low", "high"] },
    ],
  };
}

function parseTierGroups(value: unknown): AntigravityTierGroup[] {
  if (!Array.isArray(value)) return defaultAntigravitySettings(false).tier_groups;
  return value.map((item) => {
    const record = item && typeof item === "object" && !Array.isArray(item) ? item as Record<string, unknown> : {};
    const rawRouteType = stringValue(record.route_type);
    const routeType: AntigravityTierGroup["route_type"] = rawRouteType === "redirect" || rawRouteType === "public" ? rawRouteType : "tier";
    return {
      public_model: stringValue(record.public_model),
      route_type: routeType,
      aliases: csvValues(record.aliases),
      high: stringValue(record.high),
      medium: stringValue(record.medium),
      low: stringValue(record.low),
      fallback_order: csvValues(record.fallback_order),
    };
  }).filter((group) => group.public_model.trim());
}

function csvValues(value: unknown): string[] {
  if (Array.isArray(value)) return value.map(stringValue).map((item) => item.trim()).filter(Boolean);
  return stringValue(value).split(",").map((item) => item.trim()).filter(Boolean);
}

function antigravitySettingsJSON(settings: AntigravitySettings): string {
  const medium = Math.max(1, Math.floor(settings.medium_token_threshold || 8000));
  const long = Math.max(medium + 1, Math.floor(settings.long_token_threshold || 32000));
  const tierKeys = new Set<AntigravityTierKey>(["high", "medium", "low"]);
  return JSON.stringify({
    thinking_routing: settings.thinking_routing,
    tier_fallback: settings.tier_fallback,
    medium_token_threshold: medium,
    long_token_threshold: long,
    tier_groups: settings.tier_groups.map((group) => ({
      public_model: group.public_model.trim(),
      route_type: group.route_type === "redirect" || group.route_type === "public" ? group.route_type : "tier",
      aliases: group.aliases.map((item) => item.trim()).filter(Boolean),
      high: group.high.trim(),
      medium: group.medium.trim(),
      low: group.low.trim(),
      fallback_order: group.fallback_order.map((item) => item.trim()).filter(Boolean),
    })).filter((group) => group.public_model || group.high || group.medium || group.low || group.aliases.length > 0),
  });
}

function antigravityRouteModelIDs(settings: AntigravitySettings): string[] {
  return modelValues(settings.tier_groups.flatMap((group) => [
    group.public_model,
    group.high,
    group.medium,
    group.low,
    ...group.aliases,
  ]).join(","));
}

function antigravityThinkingModelIDs(raw?: string): string[] {
  return antigravityRouteModelIDs(antigravitySettings(raw));
}

function antigravityRedirectsText(settings: AntigravitySettings): string {
  return settings.tier_groups
    .filter((group) => isSimpleAntigravityRedirect(group) && group.public_model.trim() !== group.high.trim())
    .map((group) => `${group.public_model.trim()}=${group.high.trim()}`)
    .join("\n");
}

function isSimpleAntigravityRedirect(group: AntigravityTierGroup): boolean {
  return group.route_type === "redirect";
}

function isAntigravityPublicOnly(group: AntigravityTierGroup): boolean {
  return group.route_type === "public";
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
  const [expandedQuotaIds, setExpandedQuotaIds] = useState<Set<string>>(new Set());
  const [quotaSyncing, setQuotaSyncing] = useState(false);
  const [createKind, setCreateKind] = useState<"oauth" | "apikey">("oauth");

  const token = typeof window !== "undefined" ? window.localStorage.getItem("uapi.admin.token") : "";
  const selected = channels.find((item) => item.id === selectedID) || null;
  const selectedAccount = accounts.find((item) => item.id === selectedAccountID) || null;

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
    const group = createKind === "oauth" ? oauthChannelPresets : apiKeyChannelPresets;
    if (!group.some((preset) => preset.id === draft.preset)) {
      applyPreset(group[0]);
    }
  }, [createKind]);

  // Auto-refresh quota for OAuth channels on first view
  useEffect(() => {
    if (!selected || !isOAuthChannel(selected) || !token) return;
    const channelAccs = accounts.filter(a => a.channel_id === selected.id);
    if (channelAccs.length === 0) return;
    // Check if any account lacks quota data
    const needsRefresh = channelAccs.some(a => {
      const q = asRecord((a.metadata || {}).quota);
      return !q;
    });
    if (!needsRefresh) return;
    let cancelled = false;
    adminApi.refreshChannelQuota(token, selected.id).then(() => {
      if (cancelled) return;
      adminApi.accounts(token, 1, 1000).then(r => setAccounts(r.items)).catch(() => {});
    }).catch(() => {});
    return () => { cancelled = true; };
  }, [selected?.id, token]);

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

  const selectedAccounts = useMemo(() => {
    if (!selected) return [];
    return channelAccounts(selected.id)
      .slice()
      .sort((left, right) => accountLowestQuotaRemaining(left) - accountLowestQuotaRemaining(right));
  }, [selected?.id, accounts]);


  function applyPreset(preset: ChannelPreset) {
    setDraft((cur) => ({
      ...cur,
      preset: preset.id,
      type: preset.type,
      models: preset.models,
      modelAliases: preset.modelAliases || "",
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
        model_aliases: channelDraft.modelAliases.trim(),
        priority: 100,
        api_format: channelDraft.apiFormat,
        force_stream: false,
        affinity_ttl: 0,
        settings: channelDraft.apiFormat === "antigravity" ? antigravitySettingsJSON(defaultAntigravitySettings(true)) : "{}",
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
    setCreateKind("oauth");
    setDraft(createInitialDraft());
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
      const provider = oauthProviderForChannel(selected);
      const started = await adminApi.startChannelOAuth(token, { channel_id: selected.id, provider });
      setOauthChannel(selected);
      setOauthState(started.state);
      setOauthMode(started.mode);
      setOauthUserCode(started.user_code || "");
      setOauthAuthURL(started.auth_url);
      setOauthStatus({ state: started.state, provider, channel_id: selected.id, status: "pending", ready_to_bind: false, created_at: new Date().toISOString() });
      setCredNotice(started.mode === "manual_callback" ? "登录页已打开，完成后把本地回调 URL 或认证 JSON 粘贴回来。" : "认证页面已打开，完成后会自动绑定。");
      window.open(started.auth_url, "_blank", "noopener,noreferrer");
    } catch (err) {
      setCredError(err instanceof Error ? err.message : "发起认证失败");
    } finally {
      setCredLoading(false);
    }
  }

  async function startOAuthReauth(account: Account) {
    setOauthState("");
    setOauthStatus(null);
    setOauthAuthURL("");
    setOauthCallbackURL("");
    setOauthJSON("");
    setOauthUserCode("");
    setReauthAccountID(account.id);
    await startOAuth();
  }

  async function completeOAuth() {
    if (!token || !oauthState || (!oauthCallbackURL.trim() && !oauthJSON.trim())) return;
    const channel = oauthChannel || selected;
    setCredLoading(true);
    setCredError("");
    try {
      const status = await adminApi.completeChannelOAuth(token, {
        state: oauthState,
        callback_url: oauthCallbackURL.trim() || undefined,
        oauth_json: oauthJSON.trim() || undefined,
        channel_id: channel?.id,
        provider: channel?.type,
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
                  {isOAuthChannel(selected) ? (
                    <button className="btn" disabled={quotaSyncing || selectedAccounts.length === 0} onClick={async () => {
	                      if (!token || quotaSyncing) return;
	                      setQuotaSyncing(true);
	                      setError("");
	                      try {
	                        const result = await adminApi.refreshChannelQuota(token, selected.id);
	                        if (result.errors > 0) setError(result.error_messages?.[0] || "额度刷新失败");
	                      } catch (err) {
	                        setError(err instanceof Error ? err.message : "额度刷新失败");
	                      }
	                      adminApi.accounts(token, 1, 1000).then(r => setAccounts(r.items)).catch(() => {}).finally(() => setQuotaSyncing(false));
	                    }} type="button"><RefreshCw /> {quotaSyncing ? "刷新中" : "刷新额度"}</button>
                  ) : null}
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
                        {(() => {
                          const tier = accountTier(account);
                          return tier ? <span className={`badge ${tier.className}`}>{tier.label}</span> : null;
                        })()}
                        {(() => {
                          const reason = stringValue((account.metadata || {}).auto_disable_reason);
                          if (!reason) return null;
                          return <span className="badge auto-disable-badge">{reason === "quota_exhausted" ? "额度耗尽" : reason}</span>;
                        })()}
                        {(() => {
                          const alert = asRecord((account.metadata || {}).quota_alert);
                          if (!alert) return null;
                          return <span className="badge quota-alert-badge" title={stringValue(alert.message)}>⚠️</span>;
                        })()}
                      </div>
                      {accountMetaSummary(account) ? <p>{accountMetaSummary(account)}</p> : <p>{selected.type} · 权重 {account.weight || 0}</p>}
                      <div className="account-card-meta">
                        <span>{state.label}</span>
                        {account.token_expiry ? <span>访问令牌 {new Date(account.token_expiry).toLocaleString()}</span> : <span>长期凭证</span>}
                      </div>
                      {(() => {
                        const q = asRecord((account.metadata || {}).quota);
                        if (q?.is_forbidden) return <div className="account-card-quota quota-forbidden">账户被禁</div>;
                        if (q && !asArray(q.buckets).length && !stringValue(q.tier)) return <div className="account-card-quota quota-error">账户异常</div>;
                        return null;
                      })()}
                      {quotaItems.length > 0 ? (
                        <div className="account-card-quota">
                          {(expandedQuotaIds.has(account.id) ? quotaItems : quotaItems.slice(0, 3)).map((item) => (
                            <div className="quota-compact-item" key={item.key} title={item.detail || item.label}>
                              <div className="quota-compact-header">
                                <span className="quota-label">{item.label}</span>
                                <span className={`quota-percent ${quotaTone(item.remainingPercent)}`}>{item.remainingPercent}%</span>
                              </div>
                              <div className="quota-compact-bar-track">
                                <div className={`quota-compact-bar ${quotaTone(item.remainingPercent)}`} style={{ width: `${item.remainingPercent}%` }} />
                              </div>
                              {item.resetText ? <span className={`quota-compact-reset ${item.resetTone || ""}`}>{item.resetText}</span> : null}
                            </div>
                          ))}
                          {quotaItems.length > 3 && !expandedQuotaIds.has(account.id) ? (
                            <button className="quota-expand-btn" onClick={(e) => { e.stopPropagation(); setExpandedQuotaIds(prev => new Set(prev).add(account.id)); }}>
                              还有 {quotaItems.length - 3} 个模型
                            </button>
                          ) : null}
                          {quotaItems.length > 3 && expandedQuotaIds.has(account.id) ? (
                            <button className="quota-expand-btn" onClick={(e) => { e.stopPropagation(); setExpandedQuotaIds(prev => { const s = new Set(prev); s.delete(account.id); return s; }); }}>
                              收起
                            </button>
                          ) : null}
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
        <div className="channel-modal-backdrop" onClick={() => setAccountEditOpen(false)} role="presentation">
          <section aria-label="编辑账号" aria-modal="true" className="channel-modal channel-config-drawer account-edit-drawer" onClick={(event) => event.stopPropagation()} role="dialog">
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
                    <button className="btn primary" disabled={credLoading} onClick={() => startOAuthReauth(selectedAccount)} type="button">{credLoading ? "正在生成" : "重新认证"}</button>
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
          </section>
        </div>
      ) : null}

      {channelEditOpen && selected ? (
        <div className="channel-modal-backdrop" onClick={() => setChannelEditOpen(false)} role="presentation">
          <section aria-label="编辑渠道" aria-modal="true" className={`channel-modal channel-config-drawer${selected.api_format === "antigravity" ? " anti-channel-config-drawer" : ""}`} onClick={(event) => event.stopPropagation()} role="dialog">
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
                <ModelListEditor
                  channel={selected}
                  modelSyncing={modelSyncing}
                  onAliasesChange={(model_aliases) => patchChannel(selected.id, { model_aliases })}
                  onSettingsChange={(settings) => patchChannel(selected.id, { settings: antigravitySettingsJSON(settings) })}
                  selectedAccountCount={selectedAccounts.length}
                  onChange={(models) => patchChannel(selected.id, { models })}
                  onSync={() => syncChannelModels(selected)}
                />
                {selected.api_format === "antigravity" ? (
                  <AntigravitySettingsPanel
                    channel={selected}
                    saving={saving}
                    onChange={(settings) => patchChannel(selected.id, { settings: antigravitySettingsJSON(settings) })}
                    onClear={() => patchChannel(selected.id, { settings: antigravitySettingsJSON(defaultAntigravitySettings(false)) })}
                    onReset={() => patchChannel(selected.id, { settings: antigravitySettingsJSON(defaultAntigravitySettings(true)), model_aliases: "" })}
                    onModelsChange={(models) => patchChannel(selected.id, { models })}
                  />
                ) : null}
              </div>
            </div>
          </section>
        </div>
      ) : null}

      {detailOpen && selected ? (
        <div className="channel-modal-backdrop" onClick={() => setDetailOpen(false)} role="presentation">
          <section aria-label="新增账号" aria-modal="true" className="channel-modal channel-config-drawer" onClick={(event) => event.stopPropagation()} role="dialog">
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
          </section>
        </div>
      ) : null}

      {createOpen ? (
        <div className="channel-modal-backdrop" onClick={closeCreateDrawer} role="presentation">
          <section aria-label="新增渠道" aria-modal="true" className="channel-modal" onClick={(event) => event.stopPropagation()} role="dialog">
            <div className="drawer-head">
              <div>
                <p className="eyebrow">新增渠道</p>
                <h2>新增渠道</h2>
              </div>
              <button className="btn" onClick={closeCreateDrawer} title="关闭" type="button"><X /></button>
            </div>

            <div className="drawer-body">
              <div className="channel-create-tabs segmented">
                <button className={createKind === "oauth" ? "active" : ""} onClick={() => setCreateKind("oauth")} type="button">OAuth 渠道</button>
                <button className={createKind === "apikey" ? "active" : ""} onClick={() => setCreateKind("apikey")} type="button">API Key 渠道</button>
              </div>

              <div className="preset-grid provider-pick-grid">
                {(createKind === "oauth" ? oauthChannelPresets : apiKeyChannelPresets).map((preset) => (
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
          </section>
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
  resetTone?: string;
  detail?: string;
};

function buildQuotaDisplayItems(account: Account): QuotaDisplayItem[] {
  const meta = account.metadata || {};
  const items: QuotaDisplayItem[] = [];

  const quota = asRecord(meta.quota);
  if (quota) {
    const buckets = asArray(quota.buckets).map(asRecord).filter(Boolean) as Record<string, unknown>[];
    for (const [i, b] of buckets.entries()) {
      const pct = numberValue(b.remaining_percent);
      if (pct === null) continue;
      const remainingPercent = Math.round(clampPercent(pct));
      items.push({
        key: `quota-${i}`,
        label: stringValue(b.label) || `额度 ${i + 1}`,
        remainingPercent,
        resetText: formatResetTimeShort(stringValue(b.reset_time)),
        resetTone: resetTimeTone(stringValue(b.reset_time)),
        detail: [`剩余 ${remainingPercent}%`, stringValue(b.reset_time) ? `重置 ${stringValue(b.reset_time)}` : ""].filter(Boolean).join(" · "),
      });
    }
    const credits = asRecord(quota.credits);
    if (credits) {
      const balance = stringValue(credits.balance) || (credits.unlimited === true ? "unlimited" : "");
      if (balance) {
        items.push({
          key: "quota-credits",
          label: stringValue(credits.label) || "Credits",
          remainingPercent: credits.unlimited === true ? 100 : 0,
          detail: `Credits ${balance}`,
        });
      }
    }
    return sortQuotaDisplayItems(items);
  }

  return items;
}

function sortQuotaDisplayItems(items: QuotaDisplayItem[]): QuotaDisplayItem[] {
  return items.sort((left, right) => {
    const byRemaining = left.remainingPercent - right.remainingPercent;
    if (byRemaining !== 0) return byRemaining;
    return left.label.localeCompare(right.label, "zh-Hans-u-co-pinyin", { numeric: true });
  });
}

function accountLowestQuotaRemaining(account: Account): number {
  const quotaItems = buildQuotaDisplayItems(account);
  if (quotaItems.length === 0) return Number.POSITIVE_INFINITY;
  return Math.min(...quotaItems.map((item) => item.remainingPercent));
}

function accountTier(account: Account): { label: string; className: string } | null {
  const meta = account.metadata || {};
  const quota = asRecord(meta.quota);
  const rawTier = stringValue(meta.chatgpt_plan_type)
    || stringValue(meta.subscription_type)
    || stringValue(meta.plan_type)
    || stringValue(meta.account_plan)
    || stringValue(quota?.tier);
  const normalized = rawTier.trim().toLowerCase();
  if (!normalized) return null;
  if (normalized.includes("ultra") || normalized.includes("max")) return { label: "Ultra", className: "tier-ultra" };
  if (normalized.includes("plus")) return { label: "Plus", className: "tier-pro" };
  if (normalized.includes("team")) return { label: "Team", className: "tier-pro" };
  if (normalized.includes("enterprise")) return { label: "Enterprise", className: "tier-pro" };
  if (normalized.includes("pro")) return { label: "Pro", className: "tier-pro" };
  if (normalized.includes("free")) return { label: "Free", className: "tier-free" };
  return { label: rawTier.trim(), className: "tier-free" };
}

function quotaTone(percent: number): string {
  if (percent >= 50) return "high";
  if (percent >= 20) return "medium";
  return "low";
}

function clampPercent(value: number): number {
  return Math.max(0, Math.min(100, value));
}

function formatResetTimeShort(isoStr: string | null | undefined): string {
  if (!isoStr) return "";
  const t = new Date(isoStr).getTime();
  if (isNaN(t)) return isoStr;
  const diff = t - Date.now();
  if (diff <= 0) return "即将重置";
  const mins = Math.floor(diff / 60000);
  const hours = Math.floor(mins / 60);
  const days = Math.floor(hours / 24);
  if (days > 0) return `${days}d ${hours % 24}h`;
  if (hours > 0) return `${hours}h ${mins % 60}m`;
  return `${mins}m`;
}

function resetTimeTone(isoStr: string | null | undefined): string {
  if (!isoStr) return "";
  const t = new Date(isoStr).getTime();
  if (isNaN(t)) return "";
  const diff = t - Date.now();
  if (diff <= 3600000) return "reset-soon";
  if (diff <= 21600000) return "reset-waiting";
  return "reset-later";
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
  const quota = asRecord(meta.quota);
  if (!quota) return null;
  const buckets = asArray(quota.buckets).map(asRecord).filter(Boolean) as Record<string, unknown>[];
  const candidates: number[] = [];
  for (const b of buckets) {
    const pct = numberValue(b.remaining_percent);
    if (pct !== null) candidates.push(Math.max(0, Math.min(100, 100 - pct)));
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

function activeAntigravityTierKeys(group: AntigravityTierGroup): AntigravityTierKey[] {
  const fallbackTiers = new Set(group.fallback_order.map((item) => item.toLowerCase()));
  const keys = antigravityTierDefs
    .map((tier) => tier.key)
    .filter((key) => group[key].trim() !== "" || fallbackTiers.has(key));
  if (keys.length > 0) return keys;
  return ["high", "low"];
}

function nextAntigravityTierKey(group: AntigravityTierGroup): AntigravityTierKey | null {
  const active = new Set(activeAntigravityTierKeys(group));
  return antigravityTierDefs.find((tier) => !active.has(tier.key))?.key || null;
}

function normalizeAntigravityFallbackOrder(group: AntigravityTierGroup, keys: AntigravityTierKey[]): string[] {
  const active = new Set(keys);
  const tierKeys = new Set<AntigravityTierKey>(["high", "medium", "low"]);
  const cleaned = group.fallback_order.filter((item) => {
    const key = item.toLowerCase() as AntigravityTierKey;
    return !tierKeys.has(key) || active.has(key);
  });
  for (const key of keys) {
    if (!cleaned.some((item) => item.toLowerCase() === key)) cleaned.push(key);
  }
  return cleaned;
}

function ModelListEditor({
  channel,
  modelSyncing,
  onAliasesChange,
  onSettingsChange,
  selectedAccountCount,
  onChange,
  onSync,
}: {
  channel: Channel;
  modelSyncing: boolean;
  onAliasesChange: (modelAliases: string) => void;
  onSettingsChange?: (settings: AntigravitySettings) => void;
  selectedAccountCount: number;
  onChange: (models: string) => void;
  onSync: () => void;
}) {
  const [draft, setDraft] = useState("");
  const [redirectDraft, setRedirectDraft] = useState("");
  const [redirectFocused, setRedirectFocused] = useState(false);
  const preset = presetForChannel(channel);
  const isAntigravity = channel.api_format === "antigravity";
  const presetModels = modelValues(preset.models);
  const thinkingModels = isAntigravity ? antigravityThinkingModelIDs(channel.settings) : [];
  const presetInsertModels = modelValues([...presetModels, ...thinkingModels].join(","));
  const currentModels = modelValues(channel.models || "");
  const currentSet = new Set(currentModels);
  const missingPresetModels = presetInsertModels.filter((model) => !currentSet.has(model));
  const syncSupported = supportsModelSync(channel);
  const aliases = useMemo(() => parseModelAliases(channel.model_aliases || ""), [channel.model_aliases]);
  const antiSettings = useMemo(() => antigravitySettings(channel.settings), [channel.settings]);
  const antiPublicGroups = useMemo(() => {
    const groups = new Map<string, AntigravityTierGroup>();
    for (const group of antiSettings.tier_groups) {
      const publicID = group.public_model.trim();
      if (publicID) groups.set(publicID, group);
    }
    return groups;
  }, [antiSettings]);

  useEffect(() => {
    setDraft("");
  }, [channel.id]);

  useEffect(() => {
    if (!redirectFocused) {
      setRedirectDraft(modelRedirectsText(aliases));
    }
  }, [aliases, channel.id, redirectFocused]);

  const commitModels = (models: string[]) => {
    const cleaned = modelCSV(models);
    if (cleaned !== (channel.models || "")) onChange(cleaned);
  };
  const appendModels = (models: string[]) => {
    const next = models.filter((model) => model && !currentSet.has(model));
    if (next.length === 0) return;
    commitModels([...currentModels, ...next]);
  };
  const commitAliases = (nextAliases: ModelAliasMaps) => {
    const cleaned = modelAliasesText(nextAliases);
    if (cleaned !== (channel.model_aliases || "")) onAliasesChange(cleaned);
  };
  const commitRedirects = (raw: string) => {
    const redirects = parseModelAliases(raw);
    const next = parseModelAliases(channel.model_aliases || "");
    for (const [publicID, upstream] of [...next.publicToUpstream.entries()]) {
      if (publicID !== upstream) next.publicToUpstream.delete(publicID);
    }
    for (const [publicID, upstream] of redirects.publicToUpstream.entries()) {
      next.publicToUpstream.set(publicID, upstream);
    }
    commitAliases(next);
  };
  const addDraftModels = () => {
    const next = modelValues(draft);
    if (next.length === 0) return;
    appendModels(next);
    setDraft("");
  };
  const appendPreset = () => appendModels(missingPresetModels);
  const removeModel = (model: string) => {
    commitModels(currentModels.filter((item) => item !== model));
    if (aliases.publicToUpstream.has(model)) {
      const next = parseModelAliases(channel.model_aliases || "");
      next.publicToUpstream.delete(model);
      commitAliases(next);
    }
  };
  const togglePublic = (model: string) => {
    if (isAntigravity) {
      if (!onSettingsChange) return;
      const existing = antiPublicGroups.get(model);
      if (existing?.route_type === "redirect" || existing?.route_type === "public") {
        onSettingsChange({ ...antiSettings, tier_groups: antiSettings.tier_groups.filter((group) => group.public_model.trim() !== model) });
        return;
      }
      if (existing) return;
      onSettingsChange({
        ...antiSettings,
        tier_groups: [...antiSettings.tier_groups, { public_model: model, route_type: "public", aliases: [], high: model, medium: "", low: "", fallback_order: ["high"] }],
      });
      return;
    }
    const next = parseModelAliases(channel.model_aliases || "");
    if (next.publicToUpstream.has(model)) {
      next.publicToUpstream.delete(model);
    } else {
      next.publicToUpstream.set(model, model);
    }
    commitAliases(next);
  };
  const publicModels = [...aliases.publicToUpstream.entries()].filter(([publicID]) => currentSet.has(publicID));

  return (
    <div className="model-editor-panel">
      <div className="model-editor-head">
        <div className="field model-editor-field">
          <label>原始模型 ID</label>
          <input
            className="input wide"
            onChange={(event) => setDraft(event.target.value)}
            placeholder="输入原始 ID，逗号、空格或换行分隔"
            value={draft}
          />
        </div>
        <div className="model-editor-actions">
          <button className="btn" disabled={modelValues(draft).length === 0} onClick={addDraftModels} type="button"><Plus /> 填入</button>
          <button className="btn" disabled={modelSyncing || selectedAccountCount === 0 || !syncSupported} onClick={onSync} title={syncSupported ? "从上游同步模型" : "此 OAuth 渠道使用预置模型列表"} type="button"><RefreshCw /> {modelSyncing ? "获取中" : "从上游获取"}</button>
          <button className="btn subtle" disabled={missingPresetModels.length === 0} onClick={appendPreset} type="button"><Plus /> 填入预设</button>
        </div>
      </div>
      {currentModels.length > 0 ? (
        <div className="model-chip-cloud">
          {currentModels.map((model) => {
            const antiGroup = antiPublicGroups.get(model);
            const isPublic = isAntigravity ? Boolean(antiGroup) : aliases.publicToUpstream.has(model);
            const upstream = isAntigravity ? antiGroup?.high || model : aliases.publicToUpstream.get(model) || model;
            const canToggleAnti = isAntigravity && (!antiGroup || antiGroup.route_type === "redirect" || antiGroup.route_type === "public");
            return (
              <span className={`model-chip model-chip-editable ${isPublic ? "selected" : ""}`} key={model}>
                <button className="model-chip-main" disabled={isAntigravity && !canToggleAnti} onClick={() => togglePublic(model)} title={isAntigravity ? isPublic ? antiGroup?.route_type === "redirect" || antiGroup?.route_type === "public" ? `公开 ID：${model}` : "已在下方高级模型组公开" : "点击设为公开 ID" : isPublic ? `公开 ID：${model}` : "点击设为公开 ID"} type="button">
                  {isPublic ? <Eye /> : <EyeOff />}
                  <span>{model}</span>
                  {isPublic && upstream !== model ? <span className="muted">→ {upstream}</span> : null}
                </button>
                <button className="model-chip-remove" onClick={() => removeModel(model)} title="删除原始 ID" type="button"><X /></button>
              </span>
            );
          })}
        </div>
      ) : null}
      {publicModels.length > 0 ? (
        <div className="muted" style={{ fontSize: 12 }}>
          公开 ID：{publicModels.map(([publicID, upstream]) => publicID === upstream ? publicID : `${publicID} → ${upstream}`).join("，")}
        </div>
      ) : null}
      {!isAntigravity ? <div className="field">
        <label>模型重定向</label>
        <textarea
          className="input wide model-editor-textarea"
          onBlur={() => {
            setRedirectFocused(false);
            commitRedirects(redirectDraft);
          }}
          onChange={(event) => setRedirectDraft(event.target.value)}
          onFocus={() => setRedirectFocused(true)}
          placeholder="对外ID=实际上游ID"
          rows={4}
          value={redirectDraft}
        />
      </div> : null}
    </div>
  );
}

function AntigravitySettingsPanel({
  channel,
  saving,
  onChange,
  onClear,
  onReset,
  onModelsChange,
}: {
  channel: Channel;
  saving: boolean;
  onChange: (settings: AntigravitySettings) => void;
  onClear: () => void;
  onReset: () => void;
  onModelsChange: (models: string) => void;
}) {
  const [settingsDraft, setSettingsDraft] = useState(() => antigravitySettings(channel.settings));
  const [redirectDraft, setRedirectDraft] = useState(() => antigravityRedirectsText(antigravitySettings(channel.settings)));
  const [redirectFocused, setRedirectFocused] = useState(false);
  useEffect(() => {
    setSettingsDraft(antigravitySettings(channel.settings));
  }, [channel.id, channel.settings]);
  const settings = settingsDraft;
  useEffect(() => {
    if (!redirectFocused) setRedirectDraft(antigravityRedirectsText(settings));
  }, [settings, redirectFocused]);
  const appendThinkingModels = (models: string[]) => {
    const cleaned = modelCSV([...modelValues(channel.models || ""), ...models]);
    if (cleaned !== (channel.models || "")) onModelsChange(cleaned);
  };
  const commitSettings = (next: AntigravitySettings = settings) => {
    appendThinkingModels(antigravityRouteModelIDs(next));
    onChange(next);
  };
  const update = (next: Partial<AntigravitySettings>, commit = false) => {
    const merged = { ...settings, ...next };
    setSettingsDraft(merged);
    if (commit) commitSettings(merged);
  };
  const updateGroup = (index: number, next: Partial<AntigravityTierGroup>, commit = false) => {
    const groups = [...settings.tier_groups];
    groups[index] = { ...groups[index], ...next };
    update({ tier_groups: groups }, commit);
  };
  const setGroupTierKeys = (index: number, keys: AntigravityTierKey[]) => {
    const group = settings.tier_groups[index];
    const active = new Set(keys);
    updateGroup(index, {
      high: active.has("high") ? group.high : "",
      medium: active.has("medium") ? group.medium : "",
      low: active.has("low") ? group.low : "",
      fallback_order: normalizeAntigravityFallbackOrder(group, keys),
    }, true);
  };
  const commitRedirects = (raw: string) => {
    const redirects = parseModelAliases(raw);
    const redirectMap = redirects.publicToUpstream;
    const redirectPublics = new Set(redirectMap.keys());
    const nextGroups: AntigravityTierGroup[] = [];
    const seen = new Set<string>();

    for (const group of settings.tier_groups) {
      const publicModel = group.public_model.trim();
      if (!publicModel) {
        nextGroups.push(group);
        continue;
      }
      const upstream = redirectMap.get(publicModel);
      const simpleRoute = isSimpleAntigravityRedirect(group);
      if (!upstream && simpleRoute) {
        continue;
      }
      if (upstream) {
        if (simpleRoute) {
          nextGroups.push({ ...group, route_type: "redirect", high: upstream, fallback_order: ["high"] });
          seen.add(publicModel);
        }
      } else {
        nextGroups.push(group);
      }
    }

    for (const [publicModel, upstream] of redirectMap.entries()) {
      if (seen.has(publicModel)) continue;
      nextGroups.push({ public_model: publicModel, route_type: "redirect", aliases: [], high: upstream, medium: "", low: "", fallback_order: ["high"] });
    }

    const nextSettings = { ...settings, tier_groups: nextGroups };
    setSettingsDraft(nextSettings);
    appendThinkingModels([...redirectPublics, ...redirectMap.values()]);
    onChange(nextSettings);
  };
  return (
    <div className="anti-settings-panel">
      <div className="section-head">
        <div><h2>公开路由</h2><p className="muted">对外模型、实际模型、自动档位和回退都在这里配置。</p></div>
        <div className="inline-actions">
          <button className="btn" disabled={saving} onClick={onReset} type="button">恢复默认</button>
          <button className="btn" disabled={saving} onClick={onClear} type="button">取消适配</button>
        </div>
      </div>
      <div className="anti-settings-grid">
        <label className="toggle-line">
          <input checked={settings.thinking_routing} onChange={(event) => update({ thinking_routing: event.target.checked }, true)} type="checkbox" />
          <span>启用自动档位</span>
        </label>
        <label className="toggle-line">
          <input checked={settings.tier_fallback} disabled={!settings.thinking_routing} onChange={(event) => update({ tier_fallback: event.target.checked }, true)} type="checkbox" />
          <span>额度耗尽回退</span>
        </label>
        <div className="field compact-field anti-threshold-field">
          <label>中档阈值</label>
          <input className="threshold-slider" min={1000} max={64000} step={1000} type="range" value={settings.medium_token_threshold} onBlur={() => commitSettings()} onChange={(event) => update({ medium_token_threshold: Number(event.target.value || 8000) })} />
          <input className="input" min={1} type="number" value={settings.medium_token_threshold} onBlur={() => commitSettings()} onChange={(event) => update({ medium_token_threshold: Number(event.target.value || 8000) })} />
        </div>
        <div className="field compact-field anti-threshold-field">
          <label>长请求阈值</label>
          <input className="threshold-slider" min={2000} max={128000} step={1000} type="range" value={settings.long_token_threshold} onBlur={() => commitSettings()} onChange={(event) => update({ long_token_threshold: Number(event.target.value || 32000) })} />
          <input className="input" min={2} type="number" value={settings.long_token_threshold} onBlur={() => commitSettings()} onChange={(event) => update({ long_token_threshold: Number(event.target.value || 32000) })} />
        </div>
      </div>
      <div className="field anti-redirect-field">
        <label>模型重定向</label>
        <textarea
          className="input wide model-editor-textarea"
          onBlur={() => {
            setRedirectFocused(false);
            commitRedirects(redirectDraft);
          }}
          onChange={(event) => setRedirectDraft(event.target.value)}
          onFocus={() => setRedirectFocused(true)}
          placeholder="对外ID=实际上游ID"
          rows={4}
          value={redirectDraft}
        />
      </div>
      <div className="anti-tier-groups">
        <div className="model-ratio-head">
          <span>模型组</span>
          <button className="btn" onClick={() => update({ tier_groups: [...settings.tier_groups, { public_model: "", route_type: "tier", aliases: [], high: "", medium: "", low: "", fallback_order: ["high", "low"] }] })} type="button"><Plus /> 新增模型组</button>
        </div>
        {settings.tier_groups.map((group, index) => ({ group, index })).filter(({ group }) => !isSimpleAntigravityRedirect(group) && !isAntigravityPublicOnly(group)).map(({ group, index }) => {
          const tierKeys = activeAntigravityTierKeys(group);
          const nextTierKey = nextAntigravityTierKey(group);
          return (
            <div className="anti-tier-group" key={`anti-group-${index}`}>
              <div className="field compact-field anti-public-model-field">
                <label>对外模型</label>
                <input className="input" value={group.public_model} onBlur={(event) => { appendThinkingModels([event.target.value]); commitSettings(); }} onChange={(event) => updateGroup(index, { public_model: event.target.value })} placeholder="gemini-3.5-flash" />
              </div>
              <button className="btn danger anti-group-delete" onClick={() => update({ tier_groups: settings.tier_groups.filter((_, i) => i !== index) }, true)} type="button">删除</button>
              <div className="field compact-field anti-alias-field">
                <label>别名</label>
                <input className="input" value={group.aliases.join(",")} onBlur={() => commitSettings()} onChange={(event) => updateGroup(index, { aliases: csvValues(event.target.value) })} placeholder="gemini-3-flash" />
              </div>
              <div className="anti-tier-fields">
                {tierKeys.map((key) => {
                  const tier = antigravityTierDefs.find((item) => item.key === key)!;
                  return (
                    <div className="field compact-field anti-tier-field" key={key}>
                      <div className="anti-tier-label-row">
                        <label>{tier.label}</label>
                        {tierKeys.length > 1 ? <button className="link-button" onClick={() => setGroupTierKeys(index, tierKeys.filter((item) => item !== key))} type="button">移除</button> : null}
                      </div>
                      <input className="input" value={group[key]} onBlur={(event) => { appendThinkingModels([event.target.value]); commitSettings(); }} onChange={(event) => updateGroup(index, { [key]: event.target.value })} placeholder={tier.placeholder} />
                    </div>
                  );
                })}
              </div>
              <div className="anti-tier-add-row">
                {nextTierKey ? <button className="btn subtle" onClick={() => setGroupTierKeys(index, [...tierKeys, nextTierKey])} type="button"><Plus /> 添加 {antigravityTierDefs.find((tier) => tier.key === nextTierKey)?.label}</button> : null}
              </div>
              <div className="field compact-field anti-fallback-field">
                <label>回退顺序</label>
                <input className="input" value={group.fallback_order.join(",")} onBlur={() => commitSettings()} onChange={(event) => updateGroup(index, { fallback_order: csvValues(event.target.value) })} placeholder={`${tierKeys.join(",")} 或具体模型 ID`} />
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
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
  const oauthFormat = channel.api_format || channel.type;
  const callbackPlaceholder = oauthFormat === "codex"
    ? "http://localhost:1455/auth/callback?code=...&state=..."
    : oauthFormat === "gemini_code"
      ? "http://127.0.0.1:1456/oauth2callback?code=...&state=..."
      : oauthFormat === "antigravity"
        ? "http://localhost:51121/oauth-callback?code=...&state=..."
        : "https://platform.claude.com/oauth/code/callback?code=...&state=...";
  const jsonPlaceholder = oauthFormat === "codex"
    ? "{\"callback_url\":\"http://localhost:1455/auth/callback?code=...&state=...\"}"
    : oauthFormat === "gemini_code"
      ? "{\"access_token\":\"ya29...\",\"refresh_token\":\"1//...\",\"expiry_date\":1710000000000}"
      : oauthFormat === "antigravity"
        ? "{\"access_token\":\"ya29...\",\"refresh_token\":\"1//...\",\"expiry_date\":1710000000000}"
        : "{\"callback_url\":\"https://platform.claude.com/oauth/code/callback?code=...&state=...\"}";
  const providerHint = oauthFormat === "codex"
    ? "按 Codex OAuth 方式认证。"
    : oauthFormat === "gemini_code"
      ? "按 Gemini Code OAuth 方式认证。"
      : oauthFormat === "antigravity"
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
