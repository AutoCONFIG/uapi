import type { Channel, OAuthStatus } from "@/types/api";

export type ChannelPreset = {
  id: string;
  label: string;
  type: string;
  apiFormat: string;
  auth: "oauth" | "apikey" | "reverse";
  endpoint: string;
  models: string;
  modelAliases?: string;
  forceStreamModels?: string;
  note: string;
};

export const channelDefaults: Record<string, string> = {
  openai: "https://api.openai.com/v1",
  anthropic: "https://api.anthropic.com/v1",
  gemini: "https://generativelanguage.googleapis.com/v1beta",
};

export const oauthChannelDefaults: Record<string, string> = {
  codex: "https://chatgpt.com/backend-api/codex",
  anthropic: "https://api.anthropic.com/v1",
  gemini: "https://generativelanguage.googleapis.com",
  antigravity: "https://cloudcode-pa.googleapis.com",
};

const codexModels = "gpt-5.5,gpt-5.5-openai-compact,gpt-5.4,gpt-5.4-mini,gpt-5.3-codex,gpt-5.3-codex-spark,gpt-5.2,gpt-image-2,codex-auto-review";
const geminiCodeModels = "auto,flash,flash-lite,gemini-2.5-flash,gemini-2.5-flash-lite,gemini-2.5-pro,gemini-3-flash,gemini-3-flash-preview,gemini-3-pro,gemini-3-pro-preview,gemini-3.1-flash-lite,gemini-3.1-flash-lite-preview,gemini-3.1-pro,gemini-3.1-pro-preview,gemini-3.1-pro-preview-customtools,gemma-4-26b-a4b-it,gemma-4-31b-it,pro";
const geminiCodeModelAliases = [
  "gemini-2.5-pro=gemini-2.5-pro",
  "gemini-2.5-flash=gemini-2.5-flash",
  "gemini-2.5-flash-lite=gemini-2.5-flash-lite",
  "gemini-3.1-flash-lite=gemini-3.1-flash-lite-preview",
  "gemini-3-pro=gemini-3-pro-preview",
  "gemini-3.1-pro=gemini-3.1-pro-preview",
  "gemini-3-flash=gemini-3-flash-preview",
].join("\n");
const claudeCodeModels = "sonnet,opus,haiku,best,sonnet[1m],opus[1m],opusplan,claude-opus-4-6,claude-sonnet-4-6,claude-haiku-4-5-20251001,claude-opus-4-5-20251101,claude-sonnet-4-5-20250929,claude-opus-4-1-20250805,claude-opus-4-20250514,claude-sonnet-4-20250514,claude-3-7-sonnet-20250219,claude-3-5-sonnet-20241022,claude-3-5-haiku-20241022";
const antigravityModels = "claude-opus-4-6,claude-opus-4-6-thinking,claude-sonnet-4-6,claude-sonnet-4-6-thinking,gemini-3-flash,gemini-3-pro-high,gemini-3-pro-image,gemini-3-pro-image-preview,gemini-3-pro-low,gemini-3.1-flash-image,gemini-3.1-pro,gemini-3.1-pro-high,gemini-3.1-pro-low,gemini-3.5-flash,gemini-3.5-flash-high,gemini-3.5-flash-low,gemini-3.5-flash-medium,gemini-pro-agent,gpt-oss-120b,gpt-oss-120b-medium,nano-banana-2,gemini-3-pro";
const chatgptReverseModels = "auto,gpt-5.5,gpt-5.5-thinking,gpt-5.4,gpt-5.4-mini,gpt-5.3,gpt-5.3-mini,gpt-5-mini";

export const oauthChannelPresets: ChannelPreset[] = [
  { id: "antigravity", label: "Antigravity", type: "antigravity", apiFormat: "antigravity", auth: "oauth", endpoint: oauthChannelDefaults.antigravity, models: antigravityModels, note: "Google Antigravity OAuth" },
  { id: "codex", label: "Codex", type: "openai", apiFormat: "codex", auth: "oauth", endpoint: oauthChannelDefaults.codex, models: codexModels, forceStreamModels: codexModels, note: "Codex OAuth / ChatGPT backend" },
  { id: "gemini_code", label: "Gemini Code", type: "gemini", apiFormat: "gemini_code", auth: "oauth", endpoint: oauthChannelDefaults.gemini, models: geminiCodeModels, modelAliases: geminiCodeModelAliases, note: "Gemini API / OAuth" },
  { id: "claude_code", label: "Claude Code", type: "anthropic", apiFormat: "claude_code", auth: "oauth", endpoint: oauthChannelDefaults.anthropic, models: claudeCodeModels, note: "Claude Code OAuth / Anthropic Messages API" },
];

export const apiKeyChannelPresets: ChannelPreset[] = [
  { id: "openai_responses_api", label: "OpenAI Responses API", type: "openai", apiFormat: "responses", auth: "apikey", endpoint: channelDefaults.openai, models: "", note: "OpenAI Responses API" },
  { id: "openai_chat_completions", label: "OpenAI Chat Completions API", type: "openai", apiFormat: "standard", auth: "apikey", endpoint: channelDefaults.openai, models: "", note: "OpenAI Chat Completions API" },
  { id: "gemini_api", label: "Gemini API", type: "gemini", apiFormat: "standard", auth: "apikey", endpoint: channelDefaults.gemini, models: "", note: "Gemini generateContent API" },
  { id: "anthropic_messages", label: "Anthropic Messages API", type: "anthropic", apiFormat: "standard", auth: "apikey", endpoint: channelDefaults.anthropic, models: "", note: "Anthropic Messages API" },
];

export const reverseChannelPresets: ChannelPreset[] = [
  { id: "chatgpt_reverse", label: "ChatGPT Reverse", type: "openai", apiFormat: "chatgpt_reverse", auth: "reverse", endpoint: "https://chatgpt.com", models: chatgptReverseModels, note: "ChatGPT web reverse" },
];

export const channelPresets = [...oauthChannelPresets, ...reverseChannelPresets, ...apiKeyChannelPresets];
export const defaultChannelPreset = oauthChannelPresets[0];

export function isOAuthAPIFormat(apiFormat: string): boolean {
  return oauthChannelPresets.some((preset) => preset.apiFormat === apiFormat);
}

export function isReverseAPIFormat(apiFormat: string): boolean {
  return reverseChannelPresets.some((preset) => preset.apiFormat === apiFormat);
}

export function isAPIKeyAPIFormat(apiFormat: string): boolean {
  if (!apiFormat) return true;
  return apiKeyChannelPresets.some((preset) => preset.apiFormat === apiFormat);
}

export function apiKeyPresetForType(channelType: string): ChannelPreset | undefined {
  const basePresetIDs: Record<string, string> = {
    openai: "openai_chat_completions",
    gemini: "gemini_api",
    anthropic: "anthropic_messages",
  };
  const basePresetID = basePresetIDs[channelType];
  return apiKeyChannelPresets.find((preset) => preset.id === basePresetID) ||
    apiKeyChannelPresets.find((preset) => preset.type === channelType);
}

export function oauthProviderForChannel(channel: Pick<Channel, "type" | "api_format">): OAuthStatus["provider"] {
  const preset = oauthChannelPresets.find((item) => item.apiFormat === channel.api_format);
  if (preset?.apiFormat === "codex") return "codex";
  if (preset?.apiFormat === "gemini_code") return "gemini";
  if (preset?.apiFormat === "claude_code") return "anthropic";
  if (preset?.apiFormat === "antigravity") return "antigravity";
  return channel.type as OAuthStatus["provider"];
}

export function presetForChannel(channel: Channel): ChannelPreset {
  return channelPresets.find((preset) => preset.type === channel.type && preset.apiFormat === channel.api_format) ||
    channelPresets.find((preset) => preset.type === channel.type && !isOAuthAPIFormat(channel.api_format) && !isReverseAPIFormat(channel.api_format)) ||
    { id: channel.type, label: channel.type.toUpperCase(), type: channel.type, apiFormat: channel.api_format || "standard", auth: "apikey", endpoint: channel.endpoint, models: "", note: channel.type };
}

export function presetTitleLines(preset: ChannelPreset): [string, string] {
  const map: Record<string, [string, string]> = {
    antigravity: ["Google", "Antigravity"],
    codex: ["OpenAI", "Codex"],
    gemini_code: ["Google", "Gemini Code"],
    claude_code: ["Anthropic", "Claude Code"],
    chatgpt_reverse: ["OpenAI", "ChatGPT Reverse"],
    openai_responses_api: ["OpenAI", "Responses API"],
    openai_chat_completions: ["OpenAI", "Chat Completions"],
    gemini_api: ["Google", "Gemini API"],
    anthropic_messages: ["Anthropic", "Messages API"],
  };
  return map[preset.id] || [preset.type.toUpperCase(), preset.label];
}
