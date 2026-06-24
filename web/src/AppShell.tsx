import { ComponentType, useState } from "react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { logout } from "./api/client";
import { useAuth } from "./auth";
import { useTheme } from "./theme";
import { Assistant } from "./components/Assistant";
import {
  AlertsIcon,
  AppsIcon,
  AuditIcon,
  CustomersIcon,
  DashboardIcon,
  DevicesIcon,
  EnrollIcon,
  JobsIcon,
  PoliciesIcon,
  SchedulesIcon,
  ScriptsIcon,
  SettingsIcon,
  TokensIcon,
  UpdatesIcon,
  UsersIcon,
} from "./components/icons";

interface NavItem {
  to: string;
  label: string;
  icon: ComponentType<{ size?: number }>;
  perm?: string;
}

interface NavGroup {
  title: string;
  items: NavItem[];
}

// Grouped by workflow: what you look at (Overview), the fleet you manage
// (Endpoints), how you watch it (Monitoring), what you run on it
// (Automation), who/what it belongs to (Organization), and admin (System).
const NAV: NavGroup[] = [
  {
    title: "Overview",
    items: [{ to: "/dashboard", label: "Dashboard", icon: DashboardIcon }],
  },
  {
    title: "Endpoints",
    items: [
      { to: "/devices", label: "Devices", icon: DevicesIcon },
      {
        to: "/enroll",
        label: "Enrollment",
        icon: EnrollIcon,
        perm: "devices.enroll",
      },
    ],
  },
  {
    title: "Monitoring",
    items: [
      { to: "/alerts", label: "Alerts", icon: AlertsIcon, perm: "alerts.read" },
      {
        to: "/policies",
        label: "Policies",
        icon: PoliciesIcon,
        perm: "policies.read",
      },
    ],
  },
  {
    title: "Automation",
    items: [
      {
        to: "/scripts",
        label: "Scripts",
        icon: ScriptsIcon,
        perm: "scripts.read",
      },
      { to: "/jobs", label: "Jobs", icon: JobsIcon, perm: "scripts.read" },
      {
        to: "/schedules",
        label: "Schedules",
        icon: SchedulesIcon,
        perm: "scripts.read",
      },
      { to: "/apps", label: "App Deployment", icon: AppsIcon, perm: "apps.deploy" },
      {
        to: "/updates",
        label: "Agent Updates",
        icon: UpdatesIcon,
        perm: "devices.read",
      },
    ],
  },
  {
    title: "Organization",
    items: [
      { to: "/customers", label: "Customers", icon: CustomersIcon },
      { to: "/users", label: "Users", icon: UsersIcon },
      { to: "/tokens", label: "API Tokens", icon: TokensIcon },
    ],
  },
  {
    title: "System",
    items: [
      { to: "/audit", label: "Audit Log", icon: AuditIcon },
      { to: "/settings", label: "Settings", icon: SettingsIcon },
    ],
  },
];

function ThemeToggle() {
  const { theme, toggle } = useTheme();
  return (
    <button
      type="button"
      className="theme-toggle"
      onClick={toggle}
      title={`Switch to ${theme === "dark" ? "light" : "dark"} mode`}
      aria-label="Toggle color theme"
    >
      {theme === "dark" ? "☀" : "☾"}
    </button>
  );
}

export function AppShell() {
  const { me, can } = useAuth();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [navOpen, setNavOpen] = useState(false);

  async function onLogout() {
    try {
      await logout();
    } finally {
      qc.clear();
      navigate("/login", { replace: true });
    }
  }

  const groups = NAV.map((g) => ({
    ...g,
    items: g.items.filter((item) => !item.perm || can(item.perm)),
  })).filter((g) => g.items.length > 0);

  return (
    <div className={`shell ${navOpen ? "nav-open" : ""}`}>
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-mark">◆</span> rmmagic
        </div>
        <nav>
          {groups.map((group) => (
            <div className="nav-group" key={group.title}>
              <div className="nav-group-title">{group.title}</div>
              {group.items.map((item) => {
                const Icon = item.icon;
                return (
                  <NavLink
                    key={item.to}
                    to={item.to}
                    onClick={() => setNavOpen(false)}
                    className={({ isActive }) => (isActive ? "active" : "")}
                  >
                    <span className="nav-icon">
                      <Icon />
                    </span>
                    {item.label}
                  </NavLink>
                );
              })}
            </div>
          ))}
        </nav>
      </aside>
      <div className="shell-main">
        <header className="topbar">
          <button
            type="button"
            className="nav-burger"
            onClick={() => setNavOpen((o) => !o)}
            aria-label="Toggle navigation"
          >
            ☰
          </button>
          <span className="tenant">{me.tenant.name}</span>
          <span className="who">
            <ThemeToggle />
            <span className="who-email">{me.user.email}</span>
            <button type="button" onClick={() => void onLogout()}>
              Log out
            </button>
          </span>
        </header>
        <main className="content">
          <Outlet />
        </main>
      </div>
      {navOpen && (
        <div className="nav-scrim" onClick={() => setNavOpen(false)} />
      )}
      <Assistant />
    </div>
  );
}
