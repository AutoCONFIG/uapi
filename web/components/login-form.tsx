"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";

export function LoginForm() {
  const router = useRouter();

  function handleSubmit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const data = new FormData(event.currentTarget);
    const email = String(data.get("email") ?? "").trim().toLowerCase();
    const password = String(data.get("password") ?? "");

    if ((email === "admin@example.com" || email === "admin") && password === "admin123") {
      router.push("/admin/dashboard");
      return;
    }

    router.push("/overview");
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
      <button className="btn primary" style={{ width: "100%", marginTop: 4 }} type="submit">登录</button>
      <div className="auth-links">
        <Link href="/forgot-password">找回密码</Link>
        <Link href="/register">注册账号</Link>
      </div>
    </form>
  );
}
