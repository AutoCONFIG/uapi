"use client";

import { useEffect, useState } from "react";
import { Save } from "lucide-react";
import { AppShell, PageHead } from "@/components/shell";
import { adminApi } from "@/lib/api";
import type { AdminSettings } from "@/types/api";

const backgroundOptions: Array<{ value: AdminSettings["background"]; label: string; description: string }> = [
  { value: "aurora", label: "极光", description: "冷暖渐变与细网格，适合日常控制台。" },
  { value: "silk", label: "丝绸", description: "柔和织物光泽，页面更轻盈。" },
  { value: "mesh", label: "光网", description: "多层渐变网格，科技感更强。" },
  { value: "topography", label: "等高线", description: "低对比纹理，信息密集页更稳。" },
  { value: "noir", label: "暗夜", description: "深色壁纸质感，突出玻璃面板。" },
  { value: "custom", label: "自定义", description: "使用上传的本地图片作为系统壁纸。" },
];

function applyBackground(settings: AdminSettings) {
  document.body.dataset.background = settings.background || "aurora";
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
  const [background, setBackground] = useState<AdminSettings["background"]>("aurora");
  const [wallpaperURL, setWallpaperURL] = useState("");
  const [message, setMessage] = useState("");
  const [saving, setSaving] = useState(false);
  const [uploading, setUploading] = useState(false);

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    adminApi.settings(token).then((settings) => {
      setLogRetention(settings.log_retention_days);
      setRedeemRetention(settings.redeem_code_retention_days);
      setBackground(settings.background);
      setWallpaperURL(settings.wallpaper_url || "");
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
        background,
      });
      setLogRetention(updated.log_retention_days);
      setRedeemRetention(updated.redeem_code_retention_days);
      setBackground(updated.background);
      setWallpaperURL(updated.wallpaper_url || "");
      applyBackground(updated);
      setMessage("设置已保存");
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }

  async function uploadWallpaper(file?: File) {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token || !file) return;
    setUploading(true);
    setMessage("");
    try {
      const updated = await adminApi.uploadWallpaper(token, file);
      setBackground(updated.background);
      setWallpaperURL(updated.wallpaper_url || "");
      applyBackground(updated);
      setMessage("壁纸已上传并启用");
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "上传失败");
    } finally {
      setUploading(false);
    }
  }

  return (
    <AppShell title="系统设置" variant="admin">
      <PageHead title="系统设置" description="配置日志保留、兑换码清理、壁纸等全局运行参数。" />
      <section className="settings-layout">
        <div className="settings-stack">
          <section className="card card-pad settings-section">
            <div>
              <h2>运行参数</h2>
              <p className="muted">控制后台清理周期和数据保留策略。</p>
            </div>
            <div className="grid grid-2">
              <div className="field">
                <label>调用日志保留天数</label>
                <input className="input" type="number" min={1} value={logRetention} onChange={(e) => setLogRetention(Number(e.target.value))} />
              </div>
              <div className="field">
                <label>已用兑换码保留天数</label>
                <input className="input" type="number" min={1} value={redeemRetention} onChange={(e) => setRedeemRetention(Number(e.target.value))} />
              </div>
            </div>
          </section>

          <section className="card card-pad settings-section">
            <div>
              <h2>系统壁纸</h2>
              <p className="muted">预设壁纸会立即预览，保存后对所有用户生效。</p>
            </div>
            <div className="wallpaper-grid">
              {backgroundOptions.map((option) => (
                <button className={`wallpaper-option wallpaper-${option.value}${background === option.value ? " active" : ""}`} key={option.value} onClick={() => setBackground(option.value)} type="button">
                  <span className="wallpaper-preview" />
                  <strong>{option.label}</strong>
                  <small>{option.description}</small>
                </button>
              ))}
            </div>
            <div className="wallpaper-upload">
              <div>
                <strong>本地图片壁纸</strong>
                <p className="muted">支持 JPG、PNG、WebP、GIF，最大 8MB。上传后会自动启用“自定义”。</p>
                {wallpaperURL ? <a href={wallpaperURL} target="_blank" rel="noreferrer">查看当前壁纸</a> : null}
              </div>
              <label className="btn" aria-disabled={uploading}>
                {uploading ? "上传中" : "选择图片"}
                <input accept="image/jpeg,image/png,image/webp,image/gif" hidden type="file" onChange={(e) => uploadWallpaper(e.target.files?.[0])} />
              </label>
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
            <button className="btn primary" disabled={saving} onClick={save} type="button"><Save /> {saving ? "保存中" : "保存设置"}</button>
          </div>
        </aside>
      </section>
    </AppShell>
  );
}
