"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import type React from "react";
import {
  Activity,
  BarChart3,
  Blocks,
  FileText,
  Gauge,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Network,
  Package,
  Route,
  Settings,
  Shield,
  UserRound,
  Users,
  WalletCards,
} from "lucide-react";

type NavItem = {
  href: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
};

const userNav: NavItem[] = [
  { href: "/overview", label: "总览", icon: LayoutDashboard },
  { href: "/keys", label: "密钥管理", icon: KeyRound },
  { href: "/usage", label: "用量", icon: BarChart3 },
  { href: "/plans", label: "套餐", icon: WalletCards },
  { href: "/settings", label: "设置", icon: Settings },
];

const adminNav: NavItem[] = [
  { href: "/admin/dashboard", label: "运营总览", icon: Gauge },
  { href: "/admin/relay-nodes", label: "节点管理", icon: Network },
  { href: "/admin/channels", label: "渠道管理", icon: Route },
  { href: "/admin/users", label: "用户管理", icon: Users },
  { href: "/admin/tokens", label: "令牌管理", icon: Shield },
  { href: "/admin/plans", label: "套餐管理", icon: Package },
  { href: "/admin/logs", label: "调用日志", icon: FileText },
  { href: "/admin/audit-logs", label: "系统审计", icon: UserRound },
];

type AppShellVariant = "user" | "admin";

export function AppShell({
  children,
  title = "控制台",
  variant = "user",
}: {
  children: React.ReactNode;
  title?: string;
  variant?: AppShellVariant;
}) {
  return (
    <div className="app-shell">
      <main className="main">
        <TopNav title={title} variant={variant} />
        <div className="content">{children}</div>
      </main>
    </div>
  );
}

function clearStoredAuth() {
  if (typeof window === "undefined") return;
  window.localStorage.removeItem("uapi.admin.token");
  window.localStorage.removeItem("uapi.admin.refresh_token");
  window.localStorage.removeItem("uapi.admin.access_expires_at");
  window.localStorage.removeItem("uapi.admin.refresh_expires_at");
  window.localStorage.removeItem("uapi.user.token");
  window.localStorage.removeItem("uapi.user.refresh_token");
  window.localStorage.removeItem("uapi.user.access_expires_at");
  window.localStorage.removeItem("uapi.user.refresh_expires_at");
}

function TopNav({ title, variant }: { title: string; variant: AppShellVariant }) {
  const pathname = usePathname();
  const homeHref = variant === "admin" ? "/admin/dashboard" : "/overview";
  return (
    <header className="topbar">
      <Link className="brand" href={homeHref} title={title}>
        <span className="brand-mark"><Blocks size={18} /></span>
        <span>UAPI</span>
      </Link>
      <NavGroup items={variant === "admin" ? adminNav : userNav} pathname={pathname} />
      <div className="topbar-actions">
        <span className="badge green">
          <Activity size={14} /> 系统正常
        </span>
        <Link className="btn" href="/login" title="退出" onClick={clearStoredAuth}>
          <LogOut /> 退出
        </Link>
      </div>
    </header>
  );
}

function NavGroup({ items, pathname }: { items: NavItem[]; pathname: string }) {
  return (
    <nav className="nav-section" aria-label="主导航">
      {items.map((item) => {
        const Icon = item.icon;
        const active = pathname === item.href || pathname.startsWith(`${item.href}/`);
        return (
          <Link className={`nav-link${active ? " active" : ""}`} href={item.href} key={item.href}>
            <Icon />
            <span>{item.label}</span>
          </Link>
        );
      })}
    </nav>
  );
}

export function PageHead({
  title,
  description,
  action,
}: {
  title: string;
  description: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="page-head">
      <div className="page-title-block">
        <h1>{title}</h1>
        <p className="lede">{description}</p>
      </div>
      {action ? <div className="page-actions">{action}</div> : null}
    </div>
  );
}

export function MetricCard({ label, value, foot, tone }: { label: string; value: string; foot: string; tone?: string }) {
  return (
    <section className="card card-pad metric">
      <span className="metric-label">{label}</span>
      <strong className="metric-value">{value}</strong>
      <span className={`badge ${tone === "green" ? "green" : tone === "amber" ? "amber" : ""}`}>{foot}</span>
    </section>
  );
}

export function StatusBadge({ value }: { value: string }) {
  const lower = value.toLowerCase();
  const tone =
    lower.includes("healthy") || lower.includes("enabled") || lower.includes("ready") || lower.includes("active") || lower === "200"
      ? "green"
      : lower.includes("cool") || lower.includes("paused") || lower.includes("pending")
        ? "amber"
        : "red";
  return <span className={`badge ${tone}`}>{value}</span>;
}

export function EmptyState({
  title,
  description,
  action,
}: {
  title: string;
  description?: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="empty-state">
      <strong>{title}</strong>
      {description ? <p>{description}</p> : null}
      {action ? <div>{action}</div> : null}
    </div>
  );
}
