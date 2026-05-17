import Link from "next/link";

export default function RegisterPage() {
  return (
    <main className="form-page">
      <section className="auth-card">
        <p className="eyebrow">Get started</p>
        <h1>创建账号</h1>
        <p className="lede" style={{ marginBottom: 22 }}>拿到一个 OpenAI-compatible API Key，开始接入多上游中转。</p>
        <form>
          <div className="field">
            <label htmlFor="email">邮箱</label>
            <input className="input" id="email" name="email" type="email" placeholder="you@example.com" />
          </div>
          <div className="field">
            <label htmlFor="username">用户名</label>
            <input className="input" id="username" name="username" placeholder="northstar" />
          </div>
          <div className="field">
            <label htmlFor="password">密码</label>
            <input className="input" id="password" name="password" type="password" placeholder="至少 8 位" />
          </div>
          <button className="btn primary" style={{ width: "100%", marginTop: 4 }} type="button">注册</button>
        </form>
        <p className="muted" style={{ margin: "18px 0 0", fontSize: 13 }}>
          已有账号？ <Link href="/login" style={{ color: "var(--primary-dark)", fontWeight: 700 }}>登录</Link>
        </p>
      </section>
    </main>
  );
}
