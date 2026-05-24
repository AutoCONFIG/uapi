"use client";

import type { FormEvent } from "react";
import { useEffect, useState } from "react";
import { Mail, Save } from "lucide-react";
import { AppShell, PageHead } from "@/components/shell";
import { userApi } from "@/lib/api";

type FormState = {
  loading: boolean;
  error: string;
  success: string;
};

const idleState: FormState = { loading: false, error: "", success: "" };

export default function SettingsPage() {
  const [currentEmail, setCurrentEmail] = useState("");
  const [emailState, setEmailState] = useState<FormState>(idleState);
  const [passwordState, setPasswordState] = useState<FormState>(idleState);

  useEffect(() => {
    const token = window.localStorage.getItem("uapi.user.token");
    if (!token) return;

    userApi.profile(token)
      .then((profile) => setCurrentEmail(profile.email))
      .catch(() => {
        // Static preview can still render the settings form without a live API.
      });
  }, []);

  async function handleEmailSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const token = window.localStorage.getItem("uapi.user.token");
    const form = event.currentTarget;
    const data = new FormData(form);
    const email = String(data.get("email") ?? "").trim().toLowerCase();
    const password = String(data.get("password") ?? "");

    if (!token) {
      setEmailState({ loading: false, error: "请先登录后再修改邮箱。", success: "" });
      return;
    }
    if (!email || !password) {
      setEmailState({ loading: false, error: "请填写新邮箱和当前密码。", success: "" });
      return;
    }

    setEmailState({ loading: true, error: "", success: "" });
    try {
      await userApi.updateEmail(token, { email, password });
      setCurrentEmail(email);
      form.reset();
      setEmailState({ loading: false, error: "", success: "邮箱已更新。" });
    } catch (err) {
      setEmailState({
        loading: false,
        error: err instanceof Error ? err.message : "邮箱更新失败。",
        success: "",
      });
    }
  }

  async function handlePasswordSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const token = window.localStorage.getItem("uapi.user.token");
    const form = event.currentTarget;
    const data = new FormData(form);
    const oldPassword = String(data.get("old_password") ?? "");
    const newPassword = String(data.get("new_password") ?? "");

    if (!token) {
      setPasswordState({ loading: false, error: "请先登录后再修改密码。", success: "" });
      return;
    }
    if (!oldPassword || !newPassword) {
      setPasswordState({ loading: false, error: "请填写当前密码和新密码。", success: "" });
      return;
    }
    if (newPassword.length < 8) {
      setPasswordState({ loading: false, error: "新密码至少需要 8 个字符。", success: "" });
      return;
    }

    setPasswordState({ loading: true, error: "", success: "" });
    try {
      await userApi.updatePassword(token, { old_password: oldPassword, new_password: newPassword });
      form.reset();
      setPasswordState({ loading: false, error: "", success: "密码已更新。" });
    } catch (err) {
      setPasswordState({
        loading: false,
        error: err instanceof Error ? err.message : "密码更新失败。",
        success: "",
      });
    }
  }

  return (
    <AppShell title="设置">
      <PageHead
        title="账号设置"
        description="管理用户控制台账号的登录邮箱和密码。"
      />
      <div className="grid grid-2">
        <form className="card card-pad" onSubmit={handleEmailSubmit}>
          <h2>邮箱</h2>
          {currentEmail ? <p className="muted">当前邮箱：{currentEmail}</p> : null}
          <div className="field">
            <label htmlFor="email">新邮箱</label>
            <input className="input" id="email" name="email" placeholder="new@example.com" type="email" />
          </div>
          <div className="field">
            <label htmlFor="email-password">当前密码</label>
            <input className="input" id="email-password" name="password" type="password" />
          </div>
          {emailState.error ? <p className="form-error">{emailState.error}</p> : null}
          {emailState.success ? <p className="form-success">{emailState.success}</p> : null}
          <button className="btn primary" disabled={emailState.loading} type="submit">
            <Mail /> {emailState.loading ? "保存中" : "保存邮箱"}
          </button>
        </form>

        <form className="card card-pad" onSubmit={handlePasswordSubmit}>
          <h2>密码</h2>
          <div className="field">
            <label htmlFor="old-password">当前密码</label>
            <input className="input" id="old-password" name="old_password" type="password" />
          </div>
          <div className="field">
            <label htmlFor="new-password">新密码</label>
            <input className="input" id="new-password" name="new_password" minLength={8} type="password" />
          </div>
          {passwordState.error ? <p className="form-error">{passwordState.error}</p> : null}
          {passwordState.success ? <p className="form-success">{passwordState.success}</p> : null}
          <button className="btn primary" disabled={passwordState.loading} type="submit">
            <Save /> {passwordState.loading ? "更新中" : "更新密码"}
          </button>
        </form>
      </div>
    </AppShell>
  );
}
