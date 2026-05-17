export const metrics = [
  { label: "可用余额", value: "8.42M", foot: "Token credits", tone: "green" },
  { label: "今日请求", value: "24,891", foot: "+18.4% vs yesterday", tone: "primary" },
  { label: "成功率", value: "99.82%", foot: "last 24 hours", tone: "green" },
  { label: "P95 延迟", value: "842ms", foot: "OpenAI compatible", tone: "amber" },
];

export const usageBars = [42, 58, 66, 44, 72, 84, 51, 63, 78, 90, 74, 62];

export const keys = [
  { name: "Production gateway", key: "sk-relay-prod-7f3a...c91d", status: "Enabled", lastUsed: "2 min ago", created: "2026-05-12" },
  { name: "Staging console", key: "sk-relay-stg-0d82...aa10", status: "Enabled", lastUsed: "1 hour ago", created: "2026-05-10" },
  { name: "Analytics job", key: "sk-relay-job-f73c...490e", status: "Paused", lastUsed: "3 days ago", created: "2026-05-03" },
];

export const requests = [
  { time: "09:22:18", model: "gpt-4.1", format: "OpenAI Chat", status: "200", tokens: "3,420", latency: "711ms" },
  { time: "09:21:44", model: "claude-3.7-sonnet", format: "Anthropic", status: "200", tokens: "8,104", latency: "1.2s" },
  { time: "09:20:09", model: "gemini-2.5-pro", format: "Gemini", status: "200", tokens: "1,908", latency: "638ms" },
  { time: "09:18:55", model: "gpt-4o-mini", format: "OpenAI Chat", status: "429", tokens: "0", latency: "44ms" },
];

export const plans = [
  { name: "Starter", quota: "2M tokens", price: "¥29", detail: "Small apps and tests", current: false },
  { name: "Pro", quota: "20M tokens", price: "¥199", detail: "Production workloads", current: true },
  { name: "Business", quota: "200M tokens", price: "¥999", detail: "Teams and high throughput", current: false },
];

export const channels = [
  { name: "OpenAI Primary", type: "openai", status: "Healthy", accounts: 8, weight: 100, error: "0.12%" },
  { name: "Anthropic Pool", type: "anthropic", status: "Healthy", accounts: 5, weight: 80, error: "0.20%" },
  { name: "Gemini Fallback", type: "gemini", status: "Cooling", accounts: 3, weight: 40, error: "1.91%" },
];

export const users = [
  { id: "usr_01", email: "team@northstar.dev", status: "active", balance: "8.42M", keys: 3, joined: "2026-05-12" },
  { id: "usr_02", email: "ops@acme.io", status: "active", balance: "31.0M", keys: 8, joined: "2026-05-09" },
  { id: "usr_03", email: "trial@example.com", status: "disabled", balance: "0", keys: 1, joined: "2026-05-02" },
];

export const accounts = [
  { name: "openai-oauth-01", channel: "OpenAI Primary", type: "oauth_token", status: "Ready", weight: 20, expiry: "7d" },
  { name: "openai-key-02", channel: "OpenAI Primary", type: "api_key", status: "Ready", weight: 10, expiry: "N/A" },
  { name: "gemini-oauth-03", channel: "Gemini Fallback", type: "oauth_token", status: "Cooling", weight: 15, expiry: "14m" },
];
