import { AppShell, PageHead } from "@/components/shell";
import { AdminChannelConsole } from "@/components/admin-channel-console";

export default function ChannelsPage() {
  return (
    <AppShell title="渠道管理" variant="admin">
      <PageHead
        title="渠道和接入"
        description="管理上游渠道，添加 OAuth 或密钥凭证。"
      />
      <AdminChannelConsole />
    </AppShell>
  );
}