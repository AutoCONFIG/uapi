"use client";

import { useEffect, useState } from "react";
import { Save } from "lucide-react";
import { AppShell, PageHead } from "@/components/shell";
import { adminApi } from "@/lib/api";

export default function AdminSettingsPage() {
  const [logRetention, setLogRetention] = useState(180);
  const [redeemRetention, setRedeemRetention] = useState(180);
  const [message, setMessage] = useState("");
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.admin.token");
    if (!token) return;
    adminApi.settings(token).then((settings) => {
      setLogRetention(settings.log_retention_days);
      setRedeemRetention(settings.redeem_code_retention_days);
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
      });
      setLogRetention(updated.log_retention_days);
      setRedeemRetention(updated.redeem_code_retention_days);
      setMessage("设置已保存");
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "保存失败");
    } finally {
      setSaving(false);
    }
  }

  return (
    <AppShell title="系统设置" variant="admin">
      <PageHead title="系统设置" description="配置日志保留、兑换码清理等全局运行参数。" />
      <section className="card card-pad" style={{ maxWidth: 720 }}>
        <div className="grid grid-2">
          <div className="field">
            <label>调用日志保留天数</label>
            <input className="input" type="number" min={1} value={logRetention} onChange={(e) => setLogRetention(Number(e.target.value))} />
          </div>
          <div className="field">
            <label>已用/过期兑换码保留天数</label>
            <input className="input" type="number" min={1} value={redeemRetention} onChange={(e) => setRedeemRetention(Number(e.target.value))} />
          </div>
        </div>
        {message ? <p className={message.includes("失败") || message.includes("must") ? "form-error" : "form-success"}>{message}</p> : null}
        <button className="btn primary" disabled={saving} onClick={save} type="button"><Save /> {saving ? "保存中" : "保存设置"}</button>
      </section>
    </AppShell>
  );
}
