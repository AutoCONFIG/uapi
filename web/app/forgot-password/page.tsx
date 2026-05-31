import Link from "next/link";

export default function ForgotPasswordPage() {
  return (
    <main className="form-page">
      <section className="auth-card">
        <h1>找回密码</h1>
        <p className="lede" style={{ marginBottom: 22 }}>输入注册邮箱。后续接入邮件服务后，这里会发送重置链接。</p>
        <form method="post">
          <div className="field">
            <label htmlFor="email">邮箱</label>
            <input className="input" id="email" name="email" type="email" placeholder="you@example.com" />
          </div>
          <button className="btn primary" style={{ width: "100%", marginTop: 4 }} type="button">发送重置链接</button>
        </form>
        <p className="muted" style={{ margin: "18px 0 0", fontSize: 13 }}>
          想起密码了？ <Link href="/login" style={{ color: "var(--primary-dark)", fontWeight: 700 }}>返回登录</Link>
        </p>
      </section>
    </main>
  );
}
