"use client";

import { useEffect, useState } from "react";
import { Download, Save } from "lucide-react";
import { AppShell, PageHead } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { AdminSettings } from "@/types/api";

const backgroundOptions: Array<{ value: AdminSettings["background"]; label: string; description: string }> = [
  { value: "mesh", label: "光网", description: "多层渐变网格，科技感更强。" },
];

function applyBackground(settings: AdminSettings) {
  document.body.dataset.background = "mesh";
  if (settings.wallpaper_url) {
    document.body.style.setProperty("--wallpaper-image", `url("${settings.wallpaper_url}")`);
  } else {
    document.body.style.removeProperty("--wallpaper-image");
  }
  window.localStorage.setItem("uapi.ui.settings", JSON.stringify({
    background: settings.background,
    wallpaper_url: settings.wallpaper_url,
  }));
}

export default function AdminSettingsPage() {
  const [logRetention, setLogRetention] = useState(180);
  const [redeemRetention, setRedeemRetention] = useState(180);
  const [modelRatios, setModelRatios] = useState("{}");
  const [adminUsername, setAdminUsername] = useState("admin");
  const [adminPassword, setAdminPassword] = useState("");
  const [maxKeysPerUser, setMaxKeysPerUser] = useState(1);
  const [background, setBackground] = useState<AdminSettings["background"]>("mesh");
  const [publicBaseURL, setPublicBaseURL] = useState("");
  const [wallpaperURL, setWallpaperURL] = useState("");
  const [largePayloadThreshold, setLargePayloadThreshold] = useState(256);
  const [maxBodySize, setMaxBodySize] = useState(256);
  const [message, setMessage] = useState("");
  const [saving, setSaving] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [exportingUsers, setExportingUsers] = useState(false);
  const [importing, setImporting] = useState(false);
  const [importingUsers, setImportingUsers] = useState(false);

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    adminApi.settings(token).then((settings) => {
      setLogRetention(settings.log_retention_days);
      setRedeemRetention(settings.redeem_code_retention_days);
      setModelRatios(settings.model_ratios || "{}");
      setAdminUsername(settings.admin_username || "admin");
      setMaxKeysPerUser(settings.max_keys_per_user ?? 1);
      setBackground("mesh");
      setPublicBaseURL(settings.public_base_url || "");
      setWallpaperURL(settings.wallpaper_url || "");
      setLargePayloadThreshold(settings.large_payload_threshold_mb ?? 256);
      setMaxBodySize(settings.max_body_size_mb ?? 256);
      applyBackground(settings);
    }).catch(() => undefined);
  }, []);

  async function save() {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    setSaving(true);
    setMessage("");
    try {
      const updated = await adminApi.updateSettings(token, {
        log_retention_days: logRetention,
        redeem_code_retention_days: redeemRetention,
        model_ratios: modelRatios.trim() || "{}",
        admin_username: adminUsername.trim(),
        ...(adminPassword.trim() ? { admin_password: adminPassword.trim() } : {}),
        max_keys_per_user: maxKeysPerUser,
        background: "mesh",
        public_base_url: publicBaseURL.trim(),
        large_payload_threshold_mb: largePayloadThreshold,
      });
      setLogRetention(updated.log_retention_days);
      setRedeemRetention(updated.redeem_code_retention_days);
      setModelRatios(updated.model_ratios || "{}");
      setAdminUsername(updated.admin_username || "admin");
      setAdminPassword("");
      setMaxKeysPerUser(updated.max_keys_per_user ?? 1);
      setBackground("mesh");
      setPublicBaseURL(updated.public_base_url || "");
      setWallpaperURL(updated.wallpaper_url || "");
      setLargePayloadThreshold(updated.large_payload_threshold_mb ?? 256);
      setMaxBodySize(updated.max_body_size_mb ?? 256);
      applyBackground(updated);
      setMessage("设置已保存");
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }

  async function exportSettings() {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    const password = window.prompt("请输入管理员密码以导出配置");
    if (!password) return;
    setExporting(true);
    setMessage("");
    try {
      const blob = await adminApi.exportSettings(token, password);
      const url = window.URL.createObjectURL(blob);
      const link = document.createElement("a");
      const stamp = new Date().toISOString().replace(/[:.]/g, "-").slice(0, 19);
      link.href = url;
      link.download = `uapi-settings-${stamp}.yaml`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);
      setMessage("配置快照已导出");
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "导出失败");
    } finally {
      setExporting(false);
    }
  }

  async function exportUsers() {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    const password = window.prompt("请输入管理员密码以导出用户数据");
    if (!password) return;
    setExportingUsers(true);
    setMessage("");
    try {
      const blob = await adminApi.exportUsers(token, password);
      const url = window.URL.createObjectURL(blob);
      const link = document.createElement("a");
      const stamp = new Date().toISOString().replace(/[:.]/g, "-").slice(0, 19);
      link.href = url;
      link.download = `uapi-users-${stamp}.yaml`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.URL.revokeObjectURL(url);
      setMessage("用户数据已导出");
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "导出失败");
    } finally {
      setExportingUsers(false);
    }
  }

  function restoreMessage(prefix: string, result: Record<string, number>) {
    const total = Object.values(result).reduce((sum, value) => sum + value, 0);
    return `${prefix}完成，写入 ${total} 项`;
  }

  async function importSettings(file?: File) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token || !file) return;
    if (!window.confirm("恢复配置会覆盖同 ID 的渠道、账号、套餐、倍率等运行设置。确认继续？")) return;
    const password = window.prompt("请输入管理员密码以恢复配置");
    if (!password) return;
    setImporting(true);
    setMessage("");
    try {
      const result = await adminApi.importSettings(token, password, file);
      setMessage(restoreMessage("配置恢复", result));
      window.location.reload();
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "恢复失败");
    } finally {
      setImporting(false);
    }
  }

  async function importUsers(file?: File) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token || !file) return;
    if (!window.confirm("恢复用户数据会覆盖同 ID 的用户、API Key、套餐绑定和配额窗口。确认继续？")) return;
    const password = window.prompt("请输入管理员密码以恢复用户数据");
    if (!password) return;
    setImportingUsers(true);
    setMessage("");
    try {
      const result = await adminApi.importUsers(token, password, file);
      setMessage(restoreMessage("用户恢复", result));
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "恢复失败");
    } finally {
      setImportingUsers(false);
    }
  }

  return (
    <AppShell title="系统设置" variant="admin">
      <PageHead title="系统设置" description="配置日志保留、兑换码清理、壁纸等全局运行参数。" />
      <section className="settings-layout">
        <div className="settings-stack">
          <section className="card card-pad settings-section">
            <div>
              <h2>站点参数</h2>
              <p className="muted">配置公开访问地址、管理员账号和用户侧策略。</p>
            </div>
            <div className="field">
              <label>公开访问地址</label>
              <input className="input" value={publicBaseURL} onChange={(e) => setPublicBaseURL(e.target.value)} />
              <span className="muted" style={{ fontSize: 12 }}>用于用户侧快速接入等需要展示真实域名的地方，留空则使用当前浏览器地址。</span>
            </div>
            <div className="field">
              <label>大请求体阈值 (MB)</label>
              <input className="input" type="number" min={1} max={maxBodySize} value={largePayloadThreshold} onChange={(e) => setLargePayloadThreshold(Number(e.target.value))} />
              <span className="muted" style={{ fontSize: 12 }}>超过此大小的请求将跳过JSON清理，避免请求体大小变化。不能超过 Nginx/UAPI 请求体硬上限 {maxBodySize}MB。</span>
            </div>
            <div className="grid grid-2">
              <div className="field">
                <label>管理员账号</label>
                <input className="input" value={adminUsername} onChange={(e) => setAdminUsername(e.target.value)} />
              </div>
              <div className="field">
                <label>管理员新密码</label>
                <input className="input" value={adminPassword} onChange={(e) => setAdminPassword(e.target.value)} type="password" />
                <span className="muted" style={{ fontSize: 12 }}>留空则不修改密码。</span>
              </div>
              <div className="field">
                <label>每个用户最多密钥数</label>
                <input className="input" type="number" min={0} value={maxKeysPerUser} onChange={(e) => setMaxKeysPerUser(Number(e.target.value))} />
              </div>
              <div className="field">
                <label>调用日志保留天数</label>
                <input className="input" type="number" min={1} value={logRetention} onChange={(e) => setLogRetention(Number(e.target.value))} />
              </div>
              <div className="field">
                <label>已用兑换码保留天数</label>
                <input className="input" type="number" min={1} value={redeemRetention} onChange={(e) => setRedeemRetention(Number(e.target.value))} />
              </div>
            </div>
            <div className="field">
              <label>模型倍率 JSON</label>
              <textarea className="input" rows={6} value={modelRatios} onChange={(e) => setModelRatios(e.target.value)} />
              <span className="muted" style={{ fontSize: 12 }}>空值会保存为 {"{}"}。</span>
            </div>
          </section>

          <section className="card card-pad settings-section">
            <div>
              <h2>系统壁纸</h2>
              <p className="muted">当前仅保留光网主题，保存后对所有用户生效。</p>
            </div>
            <div className="wallpaper-grid">
              {backgroundOptions.map((option) => (
                <button className={`wallpaper-option wallpaper-${option.value}${background === option.value ? " active" : ""}`} key={option.value} onClick={() => setBackground("mesh")} type="button">
                  <span className="wallpaper-preview" />
                  <strong>{option.label}</strong>
                  <small>{option.description}</small>
                </button>
              ))}
            </div>
          </section>
        </div>

        <aside className="card card-pad wallpaper-stage">
          <div>
            <h2>外观预览</h2>
            <p className="muted">当前选择：{backgroundOptions.find((item) => item.value === background)?.label || background}</p>
          </div>
          <div className={`wallpaper-live-preview wallpaper-${background}`}>
            <span />
            <div>
              <strong>UAPI</strong>
              <small>玻璃面板会叠加在壁纸上，保持内容可读。</small>
            </div>
          </div>
          <div className="wallpaper-stage-actions">
            {message ? <p className={message.includes("失败") || message.includes("must") ? "form-error" : "form-success"}>{message}</p> : <span />}
            <div className="settings-action-row">
              <button className="btn" disabled={exporting} onClick={exportSettings} type="button"><Download /> {exporting ? "导出中" : "导出配置"}</button>
              <button className="btn" disabled={exportingUsers} onClick={exportUsers} type="button"><Download /> {exportingUsers ? "导出中" : "导出用户"}</button>
              <label className="btn" aria-disabled={importing}>
                <Download /> {importing ? "恢复中" : "恢复配置"}
                <input accept=".yaml,.yml,application/x-yaml,text/yaml,text/plain" disabled={importing} hidden type="file" onChange={(e) => importSettings(e.target.files?.[0])} />
              </label>
              <label className="btn" aria-disabled={importingUsers}>
                <Download /> {importingUsers ? "恢复中" : "恢复用户"}
                <input accept=".yaml,.yml,application/x-yaml,text/yaml,text/plain" disabled={importingUsers} hidden type="file" onChange={(e) => importUsers(e.target.files?.[0])} />
              </label>
              <button className="btn primary" disabled={saving} onClick={save} type="button"><Save /> {saving ? "保存中" : "保存设置"}</button>
            </div>
          </div>
        </aside>
      </section>
    </AppShell>
  );
}
