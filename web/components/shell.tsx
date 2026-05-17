"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import type React from "react";
import {
  Activity,
  BarChart3,
  FileText,
  Gauge,
  KeyRound,
  LayoutDashboard,
  LogOut,
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
  { href: "/keys", label: "API Keys", icon: KeyRound },
  { href: "/usage", label: "用量", icon: BarChart3 },
  { href: "/plans", label: "套餐", icon: WalletCards },
  { href: "/settings", label: "设置", icon: Settings },
];

const adminNav: NavItem[] = [
  { href: "/admin/dashboard", label: "管理总览", icon: Gauge },
  { href: "/admin/channels", label: "渠道", icon: Route },
  { href: "/admin/users", label: "用户", icon: Users },
  { href: "/admin/tokens", label: "令牌", icon: Shield },
  { href: "/admin/plans", label: "套餐", icon: Package },
  { href: "/admin/logs", label: "日志", icon: FileText },
  { href: "/admin/audit-logs", label: "审计", icon: UserRound },
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
      <Sidebar variant={variant} />
      <main className="main">
        <header className="topbar">
          <div className="topbar-title">{title}</div>
          <div className="topbar-actions">
            <span className="badge green">
              <Activity size={14} /> All systems normal
            </span>
            <Link className="btn" href="/login" title="退出">
              <LogOut /> Logout
            </Link>
          </div>
        </header>
        <div className="content">{children}</div>
      </main>
    </div>
  );
}

function Sidebar({ variant }: { variant: AppShellVariant }) {
  const pathname = usePathname();
  const homeHref = variant === "admin" ? "/admin/dashboard" : "/overview";
  return (
    <aside className="sidebar">
      <Link className="brand" href={homeHref}>
        <span>{variant === "admin" ? "管理后台" : "控制台"}</span>
      </Link>
      {variant === "admin" ? (
        <NavGroup label="Admin" items={adminNav} pathname={pathname} />
      ) : (
        <NavGroup label="Console" items={userNav} pathname={pathname} />
      )}
    </aside>
  );
}

function NavGroup({ label, items, pathname }: { label: string; items: NavItem[]; pathname: string }) {
  return (
    <nav className="nav-section" aria-label={label}>
      <p className="nav-label">{label}</p>
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
  eyebrow,
  title,
  description,
  action,
}: {
  eyebrow: string;
  title: string;
  description: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="page-head">
      <div>
        <p className="eyebrow">{eyebrow}</p>
        <h1>{title}</h1>
        <p className="lede">{description}</p>
      </div>
      {action}
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

export function ToolbarButton({ children }: { children: React.ReactNode }) {
  return <button className="btn primary">{children}</button>;
}
