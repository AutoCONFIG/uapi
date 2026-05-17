export type ApiEnvelope<T> = {
  code: number;
  message: string;
  data?: T;
};

export type LoginResponse = {
  token: string;
  expires_at: number;
};

export type Profile = {
  id: string;
  email: string;
  username: string;
  status: string;
  balance: number;
  created_at: string;
};

export type ApiKey = {
  id: string;
  name: string;
  key: string;
  enabled: boolean;
  created_at: string;
};

export type Plan = {
  id: string;
  name: string;
  type: string;
  token_quota: number;
  enabled: boolean;
};

export type Dashboard = {
  total_requests: number;
  total_tokens: number;
  active_channels: number;
  active_accounts: number;
};

export type PaginatedResponse<T> = {
  total: number;
  page: number;
  limit: number;
  items: T[];
};
