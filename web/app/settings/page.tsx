import { Save } from "lucide-react";
import { AppShell, PageHead } from "@/components/shell";

export default function SettingsPage() {
  return (
    <AppShell title="设置">
      <PageHead eyebrow="Settings" title="账号设置" description="管理登录邮箱和密码。安全相关设置保持简洁，后续可加入 2FA 和登录审计。" />
      <div className="grid grid-2">
        <section className="card card-pad">
          <h2>修改邮箱</h2>
          <div className="field"><label htmlFor="email">新邮箱</label><input className="input" id="email" placeholder="new@example.com" /></div>
          <div className="field"><label htmlFor="password">当前密码</label><input className="input" id="password" type="password" /></div>
          <button className="btn primary"><Save /> 保存</button>
        </section>
        <section className="card card-pad">
          <h2>修改密码</h2>
          <div className="field"><label htmlFor="old">当前密码</label><input className="input" id="old" type="password" /></div>
          <div className="field"><label htmlFor="next">新密码</label><input className="input" id="next" type="password" /></div>
          <button className="btn primary"><Save /> 更新</button>
        </section>
      </div>
    </AppShell>
  );
}
