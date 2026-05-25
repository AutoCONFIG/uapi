"use client";

import { useEffect, useMemo, useState } from "react";
import { Link2, Plus, RefreshCw, Shuffle, Trash2 } from "lucide-react";
import { AppShell, EmptyState, PageHead, StatusBadge } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { Channel, NodeChannel, RelayNode } from "@/types/api";

export default function RelayNodesPage() {
  const [nodes, setNodes] = useState<RelayNode[]>([]);
  const [channels, setChannels] = useState<Channel[]>([]);
  const [bindings, setBindings] = useState<NodeChannel[]>([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [expandedNodeID, setExpandedNodeID] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [form, setForm] = useState({ name: "local-relay", base_url: "http://relay:8081", region: "local", egress_ip: "", weight: "0", max_concurrency: "0" });
  const [quickBind, setQuickBind] = useState<Record<string, { channel_id: string; weight: string }>>({});

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
      adminApi.nodeChannels(adminToken, 1, 200).then((data) => data.items),
    ])
      .then(([nodeItems, channelItems, bindingItems]) => {
        setNodes(nodeItems);
        setChannels(channelItems);
        setBindings(bindingItems);
        setQuickBind((current) => {
          const next = { ...current };
          for (const node of nodeItems) {
            if (!next[node.id]) next[node.id] = { channel_id: channelItems[0]?.id || "", weight: "0" };
          }
          return next;
        });
      })
      .catch((err) => setError(err instanceof Error ? err.message : "加载节点失败"))
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
    setNotice("");
    try {
      const created = await adminApi.createRelayNode(adminToken, {
        name: form.name.trim() || "local-relay",
        base_url: form.base_url.trim() || "http://relay:8081",
        region: form.region.trim() || "local",
        egress_ip: form.egress_ip.trim(),
        weight: Number(form.weight || 0),
        max_concurrency: Number(form.max_concurrency || 0),
        status: "active",
        health_status: "healthy",
      });
      setNodes((cur) => [created, ...cur]);
      setQuickBind((cur) => ({ ...cur, [created.id]: { channel_id: "", weight: "0" } }));
      setForm({ name: "", base_url: "", region: "", egress_ip: "", weight: "0", max_concurrency: "0" });
      setCreateOpen(false);
      setNotice(`节点 ${created.name} 已创建。`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "创建失败");
    } finally {
      setSaving(false);
    }
  }

  async function patchNode(id: string, body: Partial<RelayNode>) {
    const adminToken = token();
    if (!adminToken) return;
    setError("");
    setNotice("");
    try {
      const updated = await adminApi.updateRelayNode(adminToken, id, body);
      setNodes((cur) => cur.map((node) => node.id === id ? updated : node));
      setNotice(`节点 ${updated.name} 已更新。`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "更新节点失败");
    }
  }

  async function deleteNode(id: string) {
    const adminToken = token();
    if (!adminToken) return;
    if (!confirm("确认删除该节点？关联绑定也会失效。")) return;
    setError("");
    setNotice("");
    try {
      const nodeName = nodes.find((node) => node.id === id)?.name || id.slice(0, 8);
      await adminApi.deleteRelayNode(adminToken, id);
      setNodes((cur) => cur.filter((node) => node.id !== id));
      setBindings((cur) => cur.filter((item) => item.relay_node_id !== id));
      setNotice(`节点 ${nodeName} 已删除。`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "删除节点失败");
    }
  }

  async function createBinding(nodeID: string) {
    const adminToken = token();
    const draft = quickBind[nodeID];
    if (!adminToken || !draft?.channel_id) return;
    setError("");
    setNotice("");
    if (nodeBindings(nodeID).some((item) => item.channel_id === draft.channel_id)) {
      setError("该节点已绑定此渠道");
      return;
    }
    try {
      const created = await adminApi.createNodeChannel(adminToken, {
        relay_node_id: nodeID,
        channel_id: draft.channel_id,
        weight: Number(draft.weight || 0),
        enabled: true,
      });
      setBindings((cur) => [created, ...cur]);
      setQuickBind((cur) => ({ ...cur, [nodeID]: { channel_id: "", weight: "0" } }));
      setNotice(`${channelName(created.channel_id)} 已绑定到节点。`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "新增绑定失败");
    }
  }

  async function autoBalanceBindings() {
    const adminToken = token();
    if (!adminToken || nodes.length === 0 || channels.length === 0) return;
    if (!confirm("确认自动均分绑定？现有节点绑定会被替换。")) return;
    setSaving(true);
    setError("");
    setNotice("");
    try {
      await Promise.all(bindings.map((binding) => adminApi.deleteNodeChannel(adminToken, binding.id)));
      const created = await Promise.all(channels.map((channel, index) => adminApi.createNodeChannel(adminToken, {
        relay_node_id: nodes[index % nodes.length].id,
        channel_id: channel.id,
        weight: 0,
        enabled: true,
      })));
      setBindings(created);
      setNotice(`已为 ${created.length} 个渠道重新均分绑定。`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "自动均分失败");
    } finally {
      setSaving(false);
    }
  }

  async function patchBinding(id: string, body: Partial<NodeChannel>) {
    const adminToken = token();
    if (!adminToken) return;
    setError("");
    setNotice("");
    try {
      const updated = await adminApi.updateNodeChannel(adminToken, id, body);
      setBindings((cur) => cur.map((item) => item.id === id ? updated : item));
      setNotice(`${channelName(updated.channel_id)} 绑定已更新。`);
    } catch (err) {
      setError(err instanceof Error ? err.message : "更新绑定失败");
    }
  }

  const channelName = (id: string) => channels.find((channel) => channel.id === id)?.name || id.slice(0, 8);
  const nodeBindings = (nodeID: string) => bindings.filter((binding) => binding.relay_node_id === nodeID);
  const nodeChannelGroups = (nodeID: string) => nodeBindings(nodeID).map((binding) => ({
    channelID: binding.channel_id,
    weight: binding.weight,
    bindingID: binding.id,
  }));
  const unboundChannels = (nodeID: string) => {
    const boundChannelIDs = new Set(nodeChannelGroups(nodeID).map((group) => group.channelID));
    return channels.filter((channel) => !boundChannelIDs.has(channel.id));
  };
  async function deleteChannelBinding(bindingIDs: string[]) {
    const adminToken = token();
    if (!adminToken) return;
    if (!confirm(`确认删除 ${bindingIDs.length} 个渠道绑定？`)) return;
    setError("");
    setNotice("");
    try {
      await Promise.all(bindingIDs.map((id) => adminApi.deleteNodeChannel(adminToken, id)));
      setBindings((cur) => cur.filter((item) => !bindingIDs.includes(item.id)));
      setNotice("渠道绑定已删除。");
    } catch (err) {
      setError(err instanceof Error ? err.message : "删除绑定失败");
    }
  }

  return (
    <AppShell title="节点" variant="admin">
      <PageHead
        title="节点"
        description="配置转发节点，并将渠道批量绑定到可用节点。"
        action={<><button className="btn" onClick={loadAll} title="刷新" type="button"><RefreshCw /> 刷新</button><button className="btn" onClick={autoBalanceBindings} disabled={saving || nodes.length === 0 || channels.length === 0} type="button"><Shuffle /> 自动均分绑定</button><button className="btn primary" onClick={() => setCreateOpen(true)} type="button"><Plus /> 新增节点</button></>}
      />

      <section className="ops-summary">
        <div className="ops-stat"><span>活跃节点</span><strong>{stats.active} / {nodes.length}</strong></div>
        <div className="ops-stat"><span>可用绑定</span><strong>{stats.enabledBindings} / {bindings.length}</strong></div>
        <div className="ops-stat"><span>活跃权重</span><strong>{stats.totalWeight}</strong></div>
      </section>
      {notice ? <p className="form-success" style={{ marginTop: 16 }}>{notice}</p> : null}
      {error && !createOpen ? <p className="form-error" style={{ marginTop: 16 }}>{error}</p> : null}

      {createOpen ? (
        <div className="drawer-backdrop" onClick={() => setCreateOpen(false)} role="presentation">
          <aside aria-label="新增节点" className="side-drawer" onClick={(event) => event.stopPropagation()}>
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

      <section className="resource-list node-grid">
        {nodes.map((node) => {
          const draft = quickBind[node.id] || { channel_id: "", weight: "0" };
          const bindableChannels = unboundChannels(node.id);
          const draftChannelID = bindableChannels.some((channel) => channel.id === draft.channel_id) ? draft.channel_id : "";
          return (
            <article className={`resource-card node-card${expandedNodeID === node.id ? " expanded" : ""}`} key={node.id}>
              <div className="resource-main node-card-head">
                <div>
                  <div className="resource-title">
                    <strong>{node.name}</strong>
                    <StatusBadge value={node.health_status} />
                  </div>
                  <p className="muted node-meta"><code className="resource-code">{node.base_url}</code><span>{node.current_concurrency || 0} 并发</span><span>{nodeChannelGroups(node.id).length} 渠道</span></p>
                </div>
                <div className="resource-actions">
                  <button className="btn danger icon-only" onClick={() => deleteNode(node.id)} title="删除节点" type="button"><Trash2 /></button>
                </div>
              </div>

              <div className="node-fast-controls">
                <div className="field compact"><label>权重</label><input className="input" defaultValue={node.weight} onBlur={(e) => { const value = Number(e.currentTarget.value || 0); if (value !== node.weight) patchNode(node.id, { weight: value }); }} type="number" /></div>
                <div className="field compact"><label>并发</label><input className="input" defaultValue={node.max_concurrency} onBlur={(e) => { const value = Number(e.currentTarget.value || 0); if (value !== node.max_concurrency) patchNode(node.id, { max_concurrency: value }); }} type="number" /></div>
                <button className="btn primary" onClick={() => setExpandedNodeID(expandedNodeID === node.id ? null : node.id)} type="button"><Link2 /> 绑定管理</button>
              </div>

              {expandedNodeID === node.id ? (
                <div className="node-quick-drawer">
                  <div className="node-bind-row">
                    <div className="field wide"><label>绑定渠道</label><select className="input" value={draftChannelID} onChange={(e) => setQuickBind((cur) => ({ ...cur, [node.id]: { ...draft, channel_id: e.target.value } }))}><option value="">选择渠道</option>{bindableChannels.map((channel) => <option value={channel.id} key={channel.id}>{channel.name}</option>)}</select></div>
                    <div className="field compact"><label>权重</label><input className="input" type="number" value={draft.weight} onChange={(e) => setQuickBind((cur) => ({ ...cur, [node.id]: { ...draft, weight: e.target.value } }))} /></div>
                    <button className="btn primary form-row-action" disabled={!draftChannelID || bindableChannels.length === 0} onClick={() => createBinding(node.id)} type="button"><Link2 /> 新增绑定</button>
                  </div>

                  <div className="node-bindings-list">
                    {nodeChannelGroups(node.id).length > 0 ? nodeChannelGroups(node.id).map((group) => (
                  <div className="credential-pill" key={group.channelID}>
                    <Link2 />
                    <span>{channelName(group.channelID)}</span>
                    <small>渠道</small>
                    <input className="mini-input" defaultValue={group.weight} onBlur={(e) => { const value = Number(e.currentTarget.value || 0); if (value !== group.weight) patchBinding(group.bindingID, { weight: value }); }} type="number" />
                    <button onClick={() => deleteChannelBinding([group.bindingID])} title="删除绑定" type="button"><Trash2 /></button>
                  </div>
                    )) : (
                      <EmptyState title="暂无渠道绑定" description={bindableChannels.length ? "选择渠道后点击新增绑定。" : "没有可绑定渠道。"} />
                    )}
                  </div>
                </div>
              ) : null}
            </article>
          );
        })}
        {nodes.length === 0 && !loading ? (
          <section className="card"><EmptyState title="暂无节点" description="单机模式可添加 local-relay；没有节点时系统会尝试本机 fallback。" /></section>
        ) : null}
      </section>
    </AppShell>
  );
}
