import { AdminUserConsole } from "@/components/admin-user-console";
import { AppShell, PageHead } from "@/components/shell";
import { users } from "@/lib/mock";

export default function UsersPage() {
  return (
    <AppShell title="用户管理" variant="admin">
      <PageHead
        eyebrow="Admin / Users"
        title="用户管理"
        description="封禁、删除用户，或为用户随机生成新密码。管理员自用应创建普通账号。"
      />
      <AdminUserConsole initialUsers={users} />
    </AppShell>
  );
}
