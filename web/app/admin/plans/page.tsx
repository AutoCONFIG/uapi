import { Plus } from "lucide-react";
import { AppShell, PageHead, StatusBadge } from "@/components/shell";
import { plans } from "@/lib/mock";

export default function AdminPlansPage() {
  return (
    <AppShell title="套餐管理" variant="admin">
      <PageHead
        eyebrow="Admin / Plans"
        title="套餐和限额"
        description="配置 token quota、模型倍率、窗口限额和启用状态。"
        action={<button className="btn primary"><Plus /> 新建套餐</button>}
      />
      <section className="card">
        <div className="table-wrap">
          <table>
            <thead><tr><th>名称</th><th>额度</th><th>价格</th><th>说明</th><th>状态</th></tr></thead>
            <tbody>{plans.map((row) => (
              <tr key={row.name}><td>{row.name}</td><td>{row.quota}</td><td>{row.price}</td><td>{row.detail}</td><td><StatusBadge value="active" /></td></tr>
            ))}</tbody>
          </table>
        </div>
      </section>
    </AppShell>
  );
}
