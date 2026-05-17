import { Copy, Plus, Trash2 } from "lucide-react";
import { AppShell, PageHead, StatusBadge } from "@/components/shell";
import { keys } from "@/lib/mock";

export default function KeysPage() {
  return (
    <AppShell title="API Keys">
      <PageHead
        eyebrow="Credentials"
        title="API Key 管理"
        description="为生产、测试和自动化任务拆分密钥，减少泄露后的影响面。"
        action={<button className="btn primary"><Plus /> 新建密钥</button>}
      />
      <section className="card">
        <div className="table-wrap">
          <table>
            <thead><tr><th>名称</th><th>Key</th><th>状态</th><th>最近使用</th><th>创建时间</th><th>操作</th></tr></thead>
            <tbody>
              {keys.map((key) => (
                <tr key={key.key}>
                  <td>{key.name}</td>
                  <td><code>{key.key}</code></td>
                  <td><StatusBadge value={key.status} /></td>
                  <td>{key.lastUsed}</td>
                  <td>{key.created}</td>
                  <td>
                    <button className="btn" title="复制"><Copy /></button>{" "}
                    <button className="btn danger" title="删除"><Trash2 /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
      <div className="empty-note" style={{ marginTop: 16 }}>
        后端当前 CreateKey 只接收 `name`。如果要支持 IP 白名单、过期时间、按模型限制，需要后端新增字段。
      </div>
    </AppShell>
  );
}
