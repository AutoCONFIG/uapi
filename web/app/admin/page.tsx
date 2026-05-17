import Link from "next/link";

export default function AdminPage() {
  return (
    <main className="form-page">
      <section className="auth-card">
        <p className="eyebrow">Admin</p>
        <h1>管理员后台</h1>
        <p className="lede" style={{ marginBottom: 22 }}>查看平台运营、渠道、账号池、用户和日志。</p>
        <Link className="btn primary" href="/admin/dashboard">进入管理总览</Link>
      </section>
    </main>
  );
}
