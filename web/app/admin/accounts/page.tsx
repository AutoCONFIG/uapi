import Link from "next/link";
import { AppShell, PageHead } from "@/components/shell";

export default function AccountsPage() {
  return (
    <AppShell title="账号池" variant="admin">
      <PageHead
        eyebrow="Admin / Channels"
        title="账号池已归入渠道"
        description="账号、API Key 和 OAuth token 都作为渠道内凭证管理，不再提供独立一级页面。"
        action={<Link className="btn primary" href="/admin/channels">前往渠道</Link>}
      />
      <section className="empty-note">这个兼容页面保留给旧链接使用，导航中已经移除。后续可以删除该路由或做永久跳转。</section>
    </AppShell>
  );
}
