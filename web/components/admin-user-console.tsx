"use client";

import { useEffect, useState } from "react";
import { Ban, KeyRound, Search, Trash2, Undo2 } from "lucide-react";
import { StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { User } from "@/types/api";

type UserRow = {
  id: string;
  email: string;
  status: string;
  balance: string;
  keys: number;
  joined: string;
};

function generatePassword() {
  const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789";
  const symbols = "!@#$%";
  const bytes = new Uint32Array(16);
  crypto.getRandomValues(bytes);
  const body = Array.from(bytes, (value) => alphabet[value % alphabet.length]).join("");
  return `${body.slice(0, 8)}${symbols[bytes[0] % symbols.length]}${body.slice(8, 14)}`;
}

export function AdminUserConsole({ initialUsers }: { initialUsers: UserRow[] }) {
  const [users, setUsers] = useState(initialUsers);
  const [passwordResult, setPasswordResult] = useState<{ email: string; password: string } | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;

    adminApi.users(token)
      .then((response) => setUsers(response.items.map(fromApiUser)))
      .catch(() => {
        // Keep static preview data when the Go API is not available.
      });
  }, []);

  async function toggleBan(row: UserRow) {
    const nextStatus = row.status === "disabled" ? "active" : "disabled";
    const token = window.localStorage.getItem("uapi.admin.token");
    setError("");

    if (token && isUUID(row.id)) {
      try {
        const updated = await adminApi.updateUser(token, row.id, { status: nextStatus as User["status"] });
        setUsers((current) => current.map((user) => (user.id === row.id ? fromApiUser(updated) : user)));
        return;
      } catch (err) {
        setError(err instanceof Error ? err.message : "更新用户失败");
        return;
      }
    }

    setUsers((current) =>
      current.map((user) =>
        user.id === row.id ? { ...user, status: nextStatus } : user,
      ),
    );
  }

  async function deleteUser(row: UserRow) {
    const token = window.localStorage.getItem("uapi.admin.token");
    setError("");

    if (token && isUUID(row.id)) {
      try {
        await adminApi.deleteUser(token, row.id);
        setUsers((current) => current.filter((user) => user.id !== row.id));
        if (passwordResult?.email === row.email) {
          setPasswordResult(null);
        }
      } catch (err) {
        setError(err instanceof Error ? err.message : "删除用户失败");
      }
    }
  }

  async function resetPassword(row: UserRow) {
    const token = window.localStorage.getItem("uapi.admin.token");
    const password = generatePassword();
    setError("");

    if (!token || !isUUID(row.id)) {
      setError("认证无效");
      return;
    }
    try {
      await adminApi.updateUser(token, row.id, { new_password: password });
      setPasswordResult({ email: row.email, password });
    } catch (err) {
      setError(err instanceof Error ? err.message : "重置密码失败");
    }
  }

  return (
    <>
      <section className="card card-pad channel-toolbar">
        <div>
          <h2>用户列表</h2>
          <p className="muted" style={{ margin: 0 }}>
            管理员可封禁、删除用户，或生成一次性随机密码交给用户重新登录。
          </p>
        </div>
        <button className="btn" type="button"><Search /> 搜索</button>
      </section>

      {passwordResult ? (
        <section className="card card-pad reset-result">
          <div>
            <h3>已为 {passwordResult.email} 生成新密码</h3>
            <p className="muted">前端仅展示一次。真实接入后，后端应保存哈希并写入审计日志。</p>
          </div>
          <code>{passwordResult.password}</code>
        </section>
      ) : null}
      {error ? <p className="form-error" style={{ marginTop: 16 }}>{error}</p> : null}

      <section className="card" style={{ marginTop: 16 }}>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>邮箱</th>
                <th>状态</th>
                <th>余额</th>
                <th>Key 数</th>
                <th>注册时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {users.map((row) => {
                const disabled = row.status === "disabled";
                return (
                  <tr key={row.email}>
                    <td>{row.email}</td>
                    <td><StatusBadge value={row.status} /></td>
                    <td>{row.balance}</td>
                    <td>{row.keys}</td>
                    <td>{row.joined}</td>
                    <td>
                      <div className="row-actions">
                        <button className="btn" onClick={() => toggleBan(row)} title={disabled ? "解封" : "封禁"} type="button">
                          {disabled ? <Undo2 /> : <Ban />}
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

function fromApiUser(user: User): UserRow {
  return {
    id: user.id,
    email: user.email,
    status: user.status,
    balance: formatBalance(user.balance),
    keys: 0,
    joined: user.created_at ? new Date(user.created_at).toISOString().slice(0, 10) : "-",
  };
}

function formatBalance(value: number) {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return String(value);
}

function isUUID(value: string) {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value);
}
