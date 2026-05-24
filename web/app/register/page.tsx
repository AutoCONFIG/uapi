"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { authStorage, userApi } from "@/lib/api";

export default function RegisterPage() {
  const router = useRouter();
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: React.FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError("");
    setLoading(true);

    const data = new FormData(e.currentTarget);
    const email = String(data.get("email") ?? "").trim();
    const password = String(data.get("password") ?? "");

    if (!email || !password) {
      setError("邮箱和密码不能为空");
      setLoading(false);
      return;
    }
    if (password.length < 8) {
      setError("密码至少 8 位");
      setLoading(false);
      return;
    }

    try {
      const res = await userApi.register({ email, username: email, password });
      authStorage.storeAuth("user", res);
      router.replace("/keys");
    } catch (err) {
      setError(err instanceof Error ? err.message : "注册失败");
    } finally {
      setLoading(false);
    }
  }

  return (
    <main className="form-page">
      <section className="auth-card">
        <p className="eyebrow">开始使用</p>
        <h1>创建账号</h1>
        <p className="lede" style={{ marginBottom: 22 }}>获取一个 OpenAI 兼容密钥，开始接入多上游中转。</p>
        <form onSubmit={handleSubmit}>
          <div className="field">
            <label htmlFor="email">邮箱</label>
            <input className="input" id="email" name="email" type="email" placeholder="you@example.com" required />
          </div>
          <div className="field">
            <label htmlFor="password">密码</label>
            <input className="input" id="password" name="password" type="password" placeholder="至少 8 位" required minLength={8} />
          </div>
          {error ? <p className="form-error">{error}</p> : null}
          <button className="btn primary" style={{ width: "100%", marginTop: 4 }} type="submit" disabled={loading}>
            {loading ? "注册中" : "注册"}
          </button>
        </form>
        <p className="muted" style={{ margin: "18px 0 0", fontSize: 13 }}>
          已有账号？ <a href="/login" style={{ color: "var(--primary-dark)", fontWeight: 700 }}>登录</a>
        </p>
      </section>
    </main>
  );
}