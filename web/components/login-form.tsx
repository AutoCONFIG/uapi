"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { adminApi, userApi } from "@/lib/api";

export function LoginForm() {
  const router = useRouter();
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  async function handleSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    setLoading(true);

    const data = new FormData(event.currentTarget);
    const email = String(data.get("email") ?? "").trim().toLowerCase();
    const password = String(data.get("password") ?? "");

    const adminLike = email.startsWith("admin@");

    if (adminLike) {
      try {
        const adminLogin = await adminApi.login({ email, password });
        window.localStorage.setItem("uapi.admin.token", adminLogin.token);
        router.push("/admin/dashboard");
        return;
      } catch {
        // Fall through to user login for non-standard admin emails.
      }
    }

    try {
      const userLogin = await userApi.login({ email, password });
      window.localStorage.setItem("uapi.user.token", userLogin.token);
      router.push("/overview");
      return;
    } catch {
      // Not a user account.
    }

    if (!adminLike) {
      try {
        const adminLogin = await adminApi.login({ email, password });
        window.localStorage.setItem("uapi.admin.token", adminLogin.token);
        router.push("/admin/dashboard");
        return;
      } catch {
        // Both user and admin login failed.
      }
    }

    setError("邮箱或密码不正确");
    setLoading(false);
  }

  return (
    <form onSubmit={handleSubmit}>
      <h1 className="auth-title">登录</h1>
      <div className="field">
        <label htmlFor="email">邮箱</label>
        <input className="input" id="email" name="email" type="email" />
      </div>
      <div className="field">
        <label htmlFor="password">密码</label>
        <input className="input" id="password" name="password" type="password" />
      </div>
      {error ? <p className="form-error">{error}</p> : null}
      <button className="btn primary" disabled={loading} style={{ width: "100%", marginTop: 4 }} type="submit">
        {loading ? "登录中" : "登录"}
      </button>
      <div className="auth-links">
        <Link href="/forgot-password">找回密码</Link>
        <Link href="/register">注册账号</Link>
      </div>
    </form>
  );
}
