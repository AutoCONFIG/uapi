import { KeyRound, Plus } from "lucide-react";
import { AppShell, PageHead, StatusBadge } from "@/components/shell";
import { keys } from "@/lib/mock";

export default function TokensPage() {
  return (
    <AppShell title="令牌管理" variant="admin">
      <PageHead
        eyebrow="Admin / Tokens"
        title="平台 API Key"
        description="管理员视角查看所有用户密钥、启停状态和最近使用时间。"
        action={<button className="btn primary"><Plus /> 创建令牌</button>}
      />
      <section className="card">
        <div className="table-wrap">
          <table>
            <thead><tr><th>名称</th><th>Key</th><th>状态</th><th>最近使用</th><th>创建时间</th><th>类型</th></tr></thead>
            <tbody>{keys.map((row) => (
              <tr key={row.key}><td>{row.name}</td><td><code>{row.key}</code></td><td><StatusBadge value={row.status} /></td><td>{row.lastUsed}</td><td>{row.created}</td><td><KeyRound size={16} /></td></tr>
            ))}</tbody>
          </table>
        </div>
      </section>
    </AppShell>
  );
}
