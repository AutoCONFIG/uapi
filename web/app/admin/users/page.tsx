import { AdminUserConsole } from "@/components/admin-user-console";
import { AppShell, PageHead } from "@/components/shell";

export default function UsersPage() {
  return (
    <AppShell title="用户管理" variant="admin">
      <PageHead
        title="用户管理"
        description="管理用户状态、余额和密码重置。"
      />
      <AdminUserConsole initialUsers={[]} />
    </AppShell>
  );
}