"use client";

import { useEffect, useMemo, useState } from "react";
import { Link2, Plus, RefreshCw, Trash2 } from "lucide-react";
import { AppShell, EmptyState, PageHead, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { Account, Channel, NodeAccount, RelayNode } from "@/types/api";

export default function RelayNodesPage() {
  const [nodes, setNodes] = useState<RelayNode[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [bindings, setBindings] = useState<NodeAccount[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [error, setError] = useState("");
  const [form, setForm] = useState({ name: "local-relay", base_url: "http://relay:8081", region: "local", egress_ip: "", weight: "100", max_concurrency: "100" });
  const [quickBind, setQuickBind] = useState<Record<string, { account_id: string; weight: string }>>({});

  useEffect(() => { loadAll(); }, []);

  function token() {
    return window.localStorage.getItem("uapi.admin.token");
  }

  function loadAll() {
    const adminToken = token();
    if (!adminToken) { setLoading(false); return; }
    setLoading(true);
    Promise.all([
      adminApi.relayNodes(adminToken, 1, 100).then((data) => data.items),
      adminApi.channels(adminToken, 1, 100).then((data) => data.items),
      adminApi.accounts(adminToken, 1, 200).then((data) => data.items),
      adminApi.nodeAccounts(adminToken, 1, 200).then((data) => data.items),
    ])
      .then(([nodeItems, channelItems, accountItems, bindingItems]) => {
        const channelIDs = new Set(channelItems.map((item) => item.id));
        const validAccounts = accountItems.filter((item) => channelIDs.has(item.channel_id));
        setNodes(nodeItems);
        setChannels(channelItems);
        setAccounts(validAccounts);
        setBindings(bindingItems);
        setQuickBind((current) => {
          const next = { ...current };
          for (const node of nodeItems) {
            if (!next[node.id]) next[node.id] = { account_id: validAccounts[0]?.id || "", weight: "100" };
          }
          return next;
        });
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }

  const stats = useMemo(() => {
    const active = nodes.filter((node) => node.status === "active").length;
    const enabledBindings = bindings.filter((item) => item.enabled).length;
    const totalWeight = nodes.filter((node) => node.status === "active").reduce((sum, node) => sum + Math.max(0, node.weight), 0);
    return { active, enabledBindings, totalWeight };
  }, [nodes, bindings]);

  async function createNode() {
    const adminToken = token();
    if (!adminToken) return;
    setSaving(true);
    setError("");
    try {
      const created = await adminApi.createRelayNode(adminToken, {
        name: form.name.trim() || "local-relay",
        base_url: form.base_url.trim() || "http://relay:8081",
        region: form.region.trim() || "local",
        egress_ip: form.egress_ip.trim(),
        weight: Number(form.weight || 100),
        max_concurrency: Number(form.max_concurrency || 100),
        status: "active",
        health_status: "healthy",
      });
      setNodes((cur) => [created, ...cur]);
      setQuickBind((cur) => ({ ...cur, [created.id]: { account_id: accounts[0]?.id || "", weight: "100" } }));
      setForm({ name: "", base_url: "", region: "", egress_ip: "", weight: "100", max_concurrency: "100" });
      setCreateOpen(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建失败");
    } finally {
      setSaving(false);
    }
  }

  async function patchNode(id: string, body: Partial<RelayNode>) {
    const adminToken = token();
    if (!adminToken) return;
    try {
      const updated = await adminApi.updateRelayNode(adminToken, id, body);
      setNodes((cur) => cur.map((node) => node.id === id ? updated : node));
    } catch { /* keep current state */ }
  }

  async function deleteNode(id: string) {
    const adminToken = token();
    if (!adminToken) return;
    if (!confirm("确认删除该转发节点？关联绑定也会失效。")) return;
    try {
      await adminApi.deleteRelayNode(adminToken, id);
      setNodes((cur) => cur.filter((node) => node.id !== id));
      setBindings((cur) => cur.filter((item) => item.relay_node_id !== id));
    } catch { /* keep current state */ }
  }

  async function createBinding(nodeID: string) {
    const adminToken = token();
    const draft = quickBind[nodeID];
    if (!adminToken || !draft?.account_id) return;
    try {
      const created = await adminApi.createNodeAccount(adminToken, {
        relay_node_id: nodeID,
        account_id: draft.account_id,
        weight: Number(draft.weight || 100),
        enabled: true,
      });
      setBindings((cur) => [created, ...cur]);
      setQuickBind((cur) => ({ ...cur, [nodeID]: { account_id: accounts[0]?.id || "", weight: "100" } }));
    } catch { /* keep current state */ }
  }

  async function patchBinding(id: string, body: Partial<NodeAccount>) {
    const adminToken = token();
    if (!adminToken) return;
    try {
      const updated = await adminApi.updateNodeAccount(adminToken, id, body);
      setBindings((cur) => cur.map((item) => item.id === id ? updated : item));
    } catch { /* keep current state */ }
  }

  async function deleteBinding(id: string) {
    const adminToken = token();
    if (!adminToken) return;
    try {
      await adminApi.deleteNodeAccount(adminToken, id);
      setBindings((cur) => cur.filter((item) => item.id !== id));
    } catch { /* keep current state */ }
  }

  const accountName = (id: string) => accounts.find((account) => account.id === id)?.name || id.slice(0, 8);
  const accountChannel = (id: string) => {
    const account = accounts.find((item) => item.id === id);
    if (!account) return "-";
    return channels.find((channel) => channel.id === account.channel_id)?.name || account.channel_id.slice(0, 8);
  };
  const nodeBindings = (nodeID: string) => bindings.filter((binding) => binding.relay_node_id === nodeID);

  return (
    <AppShell title="转发节点" variant="admin">
      <PageHead
        eyebrow="Admin / Relay Nodes"
        title="Gateway 调度"
        description="Gateway 统一选择节点、渠道和账号；Relay 只执行转发。节点、权重、并发和账号绑定在这里集中维护。"
        action={<><button className="btn" onClick={loadAll} title="刷新" type="button"><RefreshCw /> 刷新</button><button className="btn primary" onClick={() => setCreateOpen(true)} type="button"><Plus /> 新增节点</button></>}
      />

      <section className="ops-summary">
        <div className="ops-stat"><span>活跃节点</span><strong>{stats.active} / {nodes.length}</strong></div>
        <div className="ops-stat"><span>可用绑定</span><strong>{stats.enabledBindings} / {bindings.length}</strong></div>
        <div className="ops-stat"><span>活跃权重</span><strong>{stats.totalWeight}</strong></div>
      </section>

      {createOpen ? (
        <div className="drawer-backdrop" role="presentation">
          <aside aria-label="新增节点" className="side-drawer">
            <div className="drawer-head">
              <div>
                <p className="eyebrow">New Relay</p>
                <h2>新增节点</h2>
              </div>
              <button className="btn" onClick={() => setCreateOpen(false)} type="button">关闭</button>
            </div>
            <div className="drawer-body">
              <p className="muted">单机部署默认使用 <code>http://relay:8081</code>；远端节点填写 Gateway 可访问的内网或公网地址。</p>
              <div className="grid grid-2">
                <div className="field"><label>名称</label><input className="input" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="local-relay" /></div>
                <div className="field"><label>区域</label><input className="input" value={form.region} onChange={(e) => setForm({ ...form, region: e.target.value })} placeholder="local" /></div>
              </div>
              <div className="field"><label>节点地址</label><input className="input" value={form.base_url} onChange={(e) => setForm({ ...form, base_url: e.target.value })} placeholder="http://relay:8081" /></div>
              <div className="field"><label>出口 IP</label><input className="input" value={form.egress_ip} onChange={(e) => setForm({ ...form, egress_ip: e.target.value })} placeholder="可选" /></div>
              <div className="grid grid-2">
                <div className="field"><label>权重</label><input className="input" type="number" value={form.weight} onChange={(e) => setForm({ ...form, weight: e.target.value })} /></div>
                <div className="field"><label>并发</label><input className="input" type="number" value={form.max_concurrency} onChange={(e) => setForm({ ...form, max_concurrency: e.target.value })} /></div>
              </div>
              {error ? <p className="form-error">{error}</p> : null}
              <div className="form-actions">
                <button className="btn" onClick={() => setCreateOpen(false)} disabled={saving} type="button">取消</button>
                <button className="btn primary" onClick={createNode} disabled={saving} type="button"><Plus /> 添加节点</button>
              </div>
            </div>
          </aside>
        </div>
      ) : null}

      <section className="resource-list">
        {nodes.map((node) => {
          const draft = quickBind[node.id] || { account_id: accounts[0]?.id || "", weight: "100" };
          const related = nodeBindings(node.id);
          return (
            <article className="resource-card" key={node.id}>
              <div className="resource-main">
                <div>
                  <div className="resource-title">
                    <strong>{node.name}</strong>
                    <StatusBadge value={node.status} />
                    <StatusBadge value={node.health_status} />
                  </div>
                  <code className="resource-code">{node.base_url}</code>
                  <p className="muted">{node.region || "-"} / {node.egress_ip || "-"} · 当前 {node.current_concurrency || 0} 并发 · {related.length} 个账号绑定</p>
                </div>
                <div className="resource-actions">
                  <div className="segmented compact-segment">
                    {(["active", "draining", "disabled"] as RelayNode["status"][]).map((status) => (
                      <button className={node.status === status ? "active" : ""} key={status} onClick={() => patchNode(node.id, { status })} type="button">{status}</button>
                    ))}
                  </div>
                  <button className="btn danger" onClick={() => deleteNode(node.id)} title="删除节点" type="button"><Trash2 /></button>
                </div>
              </div>

              <div className="node-controls">
                <div className="field compact"><label>节点权重</label><input className="input" defaultValue={node.weight} onBlur={(e) => { const value = Number(e.currentTarget.value || 0); if (value !== node.weight) patchNode(node.id, { weight: value }); }} type="number" /></div>
                <div className="field compact"><label>最大并发</label><input className="input" defaultValue={node.max_concurrency} onBlur={(e) => { const value = Number(e.currentTarget.value || 0); if (value !== node.max_concurrency) patchNode(node.id, { max_concurrency: value }); }} type="number" /></div>
                <div className="field wide"><label>绑定账号</label><select className="input" value={draft.account_id} onChange={(e) => setQuickBind((cur) => ({ ...cur, [node.id]: { ...draft, account_id: e.target.value } }))}><option value="">选择账号</option>{accounts.map((account) => <option value={account.id} key={account.id}>{accountName(account.id)} · {accountChannel(account.id)}</option>)}</select></div>
                <div className="field compact"><label>绑定权重</label><input className="input" type="number" value={draft.weight} onChange={(e) => setQuickBind((cur) => ({ ...cur, [node.id]: { ...draft, weight: e.target.value } }))} /></div>
                <button className="btn primary form-row-action" disabled={!draft.account_id} onClick={() => createBinding(node.id)} type="button"><Link2 /> 绑定</button>
              </div>

              <div className="credential-strip">
                {related.length > 0 ? related.map((binding) => (
                  <div className="credential-pill" key={binding.id}>
                    <Link2 />
                    <span>{accountName(binding.account_id)}</span>
                    <small>渠道 {accountChannel(binding.account_id)}</small>
                    <input className="mini-input" defaultValue={binding.weight} onBlur={(e) => { const value = Number(e.currentTarget.value || 0); if (value !== binding.weight) patchBinding(binding.id, { weight: value }); }} type="number" />
                    <button onClick={() => patchBinding(binding.id, { enabled: !binding.enabled })} type="button"><StatusBadge value={binding.enabled ? "enabled" : "disabled"} /></button>
                    <button onClick={() => deleteBinding(binding.id)} title="删除绑定" type="button"><Trash2 /></button>
                  </div>
                )) : (
                  <EmptyState title="暂无账号绑定" description={accounts.length ? "选择账号后点击绑定，Gateway 才会把请求调度到这个节点。" : "先在渠道页面添加凭证，再回到这里绑定节点。"} />
                )}
              </div>
            </article>
          );
        })}
        {nodes.length === 0 && !loading ? (
          <section className="card"><EmptyState title="暂无转发节点" description="单机模式可添加 local-relay；没有节点时系统会尝试本机 fallback。" /></section>
        ) : null}
      </section>
    </AppShell>
  );
}
