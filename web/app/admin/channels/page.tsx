import { AppShell, PageHead } from "@/components/shell";
import { AdminChannelConsole } from "@/components/admin-channel-console";
import { channels } from "@/lib/mock";

export default function ChannelsPage() {
  return (
    <AppShell title="渠道管理" variant="admin">
      <PageHead
        eyebrow="Admin / Channels"
        title="渠道和接入"
        description="渠道统一承载上游 endpoint、凭证池、OAuth 接入、权重、冷却和模型配置。"
      />
      <AdminChannelConsole initialChannels={channels} />
    </AppShell>
  );
}
