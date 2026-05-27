"use client";

import { useEffect, useMemo, useState } from "react";
import { Ban, Clipboard, KeyRound, Package, Search, Trash2, Undo2 } from "lucide-react";
import { StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import { isUUID } from "@/lib/format";
import type { Plan } from "@/types/api";

type UsageWindow = {
  type: string;
  limit: number;
  used: number;
  remaining: number;
  reset_at: string;
};

type UserRow = {
  id: string;
  email: string;
  username: string;
  status: string;
  plan_name: string;
  plan_type: string;
  plan_starts_at: string;
  plan_expires_at: string;
  usage_windows: UsageWindow[];
  created_at: string;
};

function generatePassword() {
  const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789";
  const symbols = "!@#$%";
  const bytes = new Uint32Array(16);
  crypto.getRandomValues(bytes);
  const body = Array.from(bytes, (value) => alphabet[value % alphabet.length]).join("");
  return `${body.slice(0, 8)}${symbols[bytes[0] % symbols.length]}${body.slice(8, 14)}`;
}

const windowLabels: Record<string, string> = {
  hour: "5h 窗口",
  week: "周窗口",
  month: "月窗口",
};

function formatResetShort(iso: string): string {
  const diff = new Date(iso).getTime() - Date.now();
  if (diff <= 0) return "已重置";
  const mins = Math.floor(diff / 60000);
  const hours = Math.floor(mins / 60);
  const days = Math.floor(hours / 24);
  if (days > 0) return `${days}d ${hours % 24}h`;
  if (hours > 0) return `${hours}h ${mins % 60}m`;
  return `${mins}m`;
}

function usageTone(remaining: number, limit: number): string {
  if (limit <= 0) return "high";
  const pct = remaining / limit * 100;
  if (pct >= 50) return "high";
  if (pct >= 20) return "medium";
  return "low";
}

export function AdminUserConsole({ initialUsers }: { initialUsers: UserRow[] }) {
  const [users, setUsers] = useState(initialUsers);
  const [passwordResult, setPasswordResult] = useState<{ email: string; password: string } | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [query, setQuery] = useState("");
  const [plans, setPlans] = useState<Plan[]>([]);
  const [assigning, setAssigning] = useState<UserRow | null>(null);
  const [planDraft, setPlanDraft] = useState({ plan_id: "", starts_at: "", expires_at: "" });

  const visibleUsers = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    if (!keyword) return users;
    return users.filter((user) => (
      user.email.toLowerCase().includes(keyword) ||
      user.username.toLowerCase().includes(keyword) ||
      user.status.toLowerCase().includes(keyword) ||
      user.plan_name.toLowerCase().includes(keyword)
    ));
  }, [query, users]);

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    let cancelled = false;

    adminApi.users(token)
      .then((response) => { if (!cancelled) setUsers((response.items ?? []) as unknown as UserRow[]); })
      .catch(() => {});
    adminApi.plans(token, 1, 100)
      .then((response) => { if (!cancelled) setPlans(response.items ?? []); })
      .catch(() => undefined);
    return () => { cancelled = true; };
  }, []);

  async function toggleBan(row: UserRow) {
    const nextStatus = row.status === "disabled" ? "active" : "disabled";
    const token = window.localStorage.getItem("uapi.admin.token");
    setError("");
    setNotice("");

    if (token && isUUID(row.id)) {
      try {
        await adminApi.updateUser(token, row.id, { status: nextStatus as "active" | "disabled" });
        setUsers((current) => current.map((user) => (user.id === row.id ? { ...user, status: nextStatus } : user)));
        setNotice(`${row.email} 已${nextStatus === "disabled" ? "封禁" : "解封"}。`);
        return;
      } catch (err) {
        setError(err instanceof Error ? err.message : "更新用户失败");
        return;
      }
    }
  }

  async function deleteUser(row: UserRow) {
    const token = window.localStorage.getItem("uapi.admin.token");
    setError("");
    setNotice("");
    if (!confirm(`确认删除用户 ${row.email}？此操作不可撤销。`)) return;

    if (token && isUUID(row.id)) {
      try {
        await adminApi.deleteUser(token, row.id);
        setUsers((current) => current.filter((user) => user.id !== row.id));
        if (passwordResult?.email === row.email) {
          setPasswordResult(null);
        }
        setNotice(`${row.email} 已删除。`);
      } catch (err) {
        setError(err instanceof Error ? err.message : "删除用户失败");
      }
    }
  }

  function openAssignPlan(row: UserRow) {
    const firstPlan = plans[0];
    setError("");
    setNotice("");
    setAssigning(row);
    setPlanDraft({ plan_id: firstPlan?.id || "", starts_at: "", expires_at: "" });
  }

  async function assignPlan() {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token || !assigning) return;
    setError("");
    setNotice("");
    try {
      await adminApi.updateUser(token, assigning.id, {
        plan_id: planDraft.plan_id,
        plan_starts_at: planDraft.starts_at ? new Date(planDraft.starts_at).toISOString() : undefined,
        plan_expires_at: planDraft.expires_at ? new Date(planDraft.expires_at).toISOString() : undefined,
      });
      setNotice(`${assigning.email} 的套餐已更新。`);
      // Reload users to get updated plan/usage
      adminApi.users(token).then(r => setUsers((r.items ?? []) as unknown as UserRow[])).catch(() => {});
      setAssigning(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "分配套餐失败");
    }
  }

  async function resetPassword(row: UserRow) {
    const token = window.localStorage.getItem("uapi.admin.token");
    const password = generatePassword();
    setError("");
    setNotice("");
    if (!confirm(`确认为 ${row.email} 随机重置密码？旧密码会立即失效。`)) return;

    if (!token || !isUUID(row.id)) {
      setError("认证无效");
      return;
    }
    try {
      await adminApi.updateUser(token, row.id, { new_password: password });
      setPasswordResult({ email: row.email, password });
      setNotice(`${row.email} 的密码已重置。`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "重置密码失败");
    }
  }

  async function copyPassword() {
    if (!passwordResult) return;
    await navigator.clipboard?.writeText(passwordResult.password);
    setNotice("新密码已复制。");
  }

  return (
    <>
      <section className="card card-pad channel-toolbar">
        <div>
          <h2>用户列表</h2>
          <p className="muted" style={{ margin: 0 }}>
            管理员可封禁、删除用户，分配套餐或生成一次性随机密码。
          </p>
        </div>
        <div className="toolbar-search">
          <Search />
          <input className="input" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索邮箱、用户名、套餐" />
        </div>
      </section>

      {passwordResult ? (
        <section className="card card-pad reset-result">
          <div>
            <h3>已为 {passwordResult.email} 生成新密码</h3>
            <p className="muted">前端仅展示一次。真实接入后，后端应保存哈希并写入系统审计。</p>
          </div>
          <div className="credential-pill">
            <KeyRound />
            <code>{passwordResult.password}</code>
            <button onClick={copyPassword} title="复制新密码" type="button"><Clipboard /></button>
          </div>
        </section>
      ) : null}
      {notice ? <p className="form-success" style={{ marginTop: 16 }}>{notice}</p> : null}
      {error ? <p className="form-error" style={{ marginTop: 16 }}>{error}</p> : null}

      {assigning ? (
        <div className="modal-backdrop" role="presentation" onClick={() => setAssigning(null)}>
          <section aria-modal="true" className="modal" role="dialog" onClick={(event) => event.stopPropagation()}>
            <div className="modal-head"><h2>分配套餐</h2><button className="btn" onClick={() => setAssigning(null)} type="button">关闭</button></div>
            <p className="muted">{assigning.email}</p>
            <div className="grid grid-3">
              <div className="field"><label>套餐</label><select className="input" value={planDraft.plan_id} onChange={(e) => setPlanDraft((cur) => ({ ...cur, plan_id: e.target.value }))}>{plans.map((plan) => <option key={plan.id} value={plan.id}>{plan.name} · {plan.duration_days || 30} 天</option>)}</select></div>
              <div className="field"><label>生效时间</label><input className="input" type="datetime-local" value={planDraft.starts_at} onChange={(e) => setPlanDraft((cur) => ({ ...cur, starts_at: e.target.value }))} /></div>
              <div className="field"><label>过期时间</label><input className="input" type="datetime-local" value={planDraft.expires_at} onChange={(e) => setPlanDraft((cur) => ({ ...cur, expires_at: e.target.value }))} /></div>
            </div>
            <div className="form-actions"><button className="btn" onClick={() => setAssigning(null)} type="button">取消</button><button className="btn primary" onClick={assignPlan} type="button">确认分配</button></div>
          </section>
        </div>
      ) : null}

      <section className="card" style={{ marginTop: 16 }}>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>用户</th>
                <th>状态</th>
                <th>套餐</th>
                <th>使用量</th>
                <th>到期</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {visibleUsers.map((row) => {
                const disabled = row.status === "disabled";
                return (
                  <tr key={row.id}>
                    <td>
                      <div>{row.email}</div>
                      <div className="muted" style={{ fontSize: 11 }}>{row.username}</div>
                    </td>
                    <td><StatusBadge value={row.status} /></td>
                    <td>
                      {row.plan_name ? (
                        <span className="badge" style={{ background: "var(--primary)", color: "#fff" }}>{row.plan_name}</span>
                      ) : (
                        <span className="muted">无</span>
                      )}
                    </td>
                    <td>
                      {row.usage_windows && row.usage_windows.length > 0 ? (
                        <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                          {row.usage_windows.map((w) => (
                            <div key={w.type} className="usage-window-cell" title={`${windowLabels[w.type] || w.type}: ${w.used}/${w.limit}`}>
                              <div style={{ fontSize: 10, color: "var(--muted)" }}>{windowLabels[w.type] || w.type}</div>
                              <div style={{ display: "flex", alignItems: "center", gap: 4 }}>
                                <div className="quota-compact-bar-track" style={{ width: 48, height: 4 }}>
                                  <div className={`quota-compact-bar ${usageTone(w.remaining, w.limit)}`} style={{ width: `${w.limit > 0 ? (w.remaining / w.limit * 100) : 100}%` }} />
                                </div>
                                <span style={{ fontSize: 11, fontFamily: "var(--font-mono)" }} className={`quota-percent ${usageTone(w.remaining, w.limit)}`}>{w.remaining}</span>
                              </div>
                            </div>
                          ))}
                        </div>
                      ) : (
                        <span className="muted">-</span>
                      )}
                    </td>
                    <td>
                      {row.plan_expires_at ? (
                        <span style={{ fontSize: 12 }}>{new Date(row.plan_expires_at).toLocaleDateString()}</span>
                      ) : (
                        <span className="muted">-</span>
                      )}
                    </td>
                    <td>
                      <div className="row-actions">
                        <button className="btn" onClick={() => toggleBan(row)} title={disabled ? "解封" : "封禁"} type="button">
                          {disabled ? <Undo2 /> : <Ban />}
                        </button>
                        <button className="btn" onClick={() => openAssignPlan(row)} title="分配套餐" type="button">
                          <Package />
                        </button>
                        <button className="btn" onClick={() => resetPassword(row)} title="随机重置密码" type="button">
                          <KeyRound />
                        </button>
                        <button className="btn danger" onClick={() => deleteUser(row)} title="删除用户" type="button">
                          <Trash2 />
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </section>
    </>
  );
}
