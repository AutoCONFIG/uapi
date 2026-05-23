"use client";

import { useEffect, useState } from "react";
import { Trash2 } from "lucide-react";
import { AppShell, EmptyState, PageHead, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { ApiKey } from "@/types/api";

export default function TokensPage() {
  const [tokens, setTokens] = useState<ApiKey[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) { setLoading(false); return; }
    adminApi.tokens(token)
      .then((data) => setTokens(data.items))
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  async function deleteToken(id: string) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    try {
      await adminApi.deleteToken(token, id);
      setTokens((cur) => cur.filter((t) => t.id !== id));
    } catch { /* ignore */ }
  }

  return (
    <AppShell title="令牌管理" variant="admin">
      <PageHead
        eyebrow="Admin / Tokens"
        title="用户 API Key"
        description="管理员仅查看用户密钥状态和归属，不生成或持有可用 Key。"
      />
      <section className="card">
        <div className="table-wrap">
          <table>
            <thead><tr><th>名称</th><th>用户</th><th>状态</th><th>模型</th><th>创建时间</th><th></th></tr></thead>
            <tbody>
              {tokens.map((t) => (
                <tr key={t.id}>
                  <td>{t.name}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{t.user_id || "—"}</td>
                  <td><StatusBadge value={t.enabled ? "enabled" : "disabled"} /></td>
                  <td className="muted" style={{ fontSize: 12 }}>{t.models || "—"}</td>
                  <td className="muted" style={{ fontSize: 12 }}>{new Date(t.created_at).toLocaleDateString()}</td>
                  <td><button className="btn" style={{ padding: "2px 8px" }} onClick={() => deleteToken(t.id)} title="删除"><Trash2 size={14} /></button></td>
                </tr>
              ))}
              {tokens.length === 0 && !loading && (
                <tr><td colSpan={6}><EmptyState title="暂无用户 Key" description="用户在控制台创建 API Key 后，管理员可在这里查看状态和归属。" /></td></tr>
              )}
            </tbody>
          </table>
        </div>
      </section>
    </AppShell>
  );
}
