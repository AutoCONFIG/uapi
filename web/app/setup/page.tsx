"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

export default function SetupPage() {
  const router = useRouter();
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError("");
    setLoading(true);

    const data = new FormData(e.currentTarget);
    const email = String(data.get("email") ?? "").trim().toLowerCase();
    const password = String(data.get("password") ?? "");
    const confirmedPassword = String(data.get("confirm") ?? "");

    if (password.length < 8) {
      setError("密码至少 8 位");
      setLoading(false);
      return;
    }
    if (password !== confirmedPassword) {
      setError("两次密码不一致");
      setLoading(false);
      return;
    }

    try {
      const res = await fetch("/api/admin/setup", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      const json = await res.json();
      if (json.code !== 0) {
        setError(json.message || "设置失败");
        setLoading(false);
        return;
      }
      // Setup success — auto login
      const loginRes = await fetch("/api/admin/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      const loginJson = await loginRes.json();
      if (loginJson.code === 0 && loginJson.data?.token) {
        window.localStorage.setItem("uapi.admin.token", loginJson.data.token);
        router.replace("/admin/dashboard");
      } else {
        router.replace("/");
      }
    } catch {
      setError("网络错误，请检查后端服务");
    } finally {
      setLoading(false);
    }
  }

  return (
    <main className="form-page">
      <section className="auth-card">
        <form onSubmit={handleSubmit}>
          <h1 className="auth-title">初始化设置</h1>
          <p style={{ color: "var(--muted)", marginBottom: 16, fontSize: 14 }}>
            首次使用，请创建管理员账号
          </p>
          <div className="field">
            <label htmlFor="email">管理员邮箱</label>
            <input className="input" id="email" name="email" type="email" required autoFocus />
          </div>
          <div className="field">
            <label htmlFor="password">密码</label>
            <input className="input" id="password" name="password" type="password" required minLength={8} />
          </div>
          <div className="field">
            <label htmlFor="confirm">确认密码</label>
            <input className="input" id="confirm" name="confirm" type="password" required minLength={8} />
          </div>
          {error ? <p className="form-error">{error}</p> : null}
          <button className="btn primary" disabled={loading} style={{ width: "100%", marginTop: 4 }} type="submit">
            {loading ? "设置中" : "创建管理员"}
          </button>
        </form>
      </section>
    </main>
  );
}
