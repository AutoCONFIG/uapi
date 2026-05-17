"use client";

import { useState } from "react";
import { Ban, KeyRound, Search, Trash2, Undo2 } from "lucide-react";
import { StatusBadge } from "@/components/shell";

type UserRow = {
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

  function toggleBan(email: string) {
    setUsers((current) =>
      current.map((user) =>
        user.email === email ? { ...user, status: user.status === "disabled" ? "active" : "disabled" } : user,
      ),
    );
  }

  function deleteUser(email: string) {
    setUsers((current) => current.filter((user) => user.email !== email));
    if (passwordResult?.email === email) {
      setPasswordResult(null);
    }
  }

  function resetPassword(email: string) {
    setPasswordResult({ email, password: generatePassword() });
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
                        <button className="btn" onClick={() => toggleBan(row.email)} title={disabled ? "解封" : "封禁"} type="button">
                          {disabled ? <Undo2 /> : <Ban />}
                        </button>
                        <button className="btn" onClick={() => resetPassword(row.email)} title="随机重置密码" type="button">
                          <KeyRound />
                        </button>
                        <button className="btn danger" onClick={() => deleteUser(row.email)} title="删除用户" type="button">
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
