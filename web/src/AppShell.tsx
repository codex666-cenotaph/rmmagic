import { useState } from "react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { logout } from "./api/client";
import { useAuth } from "./auth";
import { useTheme } from "./theme";

interface NavItem {
  to: string;
  label: string;
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
    items: [{ to: "/dashboard", label: "Dashboard" }],
  },
  {
    title: "Endpoints",
    items: [
      { to: "/devices", label: "Devices" },
      { to: "/enroll", label: "Enrollment", perm: "devices.enroll" },
    ],
  },
  {
    title: "Monitoring",
    items: [
      { to: "/alerts", label: "Alerts", perm: "alerts.read" },
      { to: "/policies", label: "Policies", perm: "policies.read" },
    ],
  },
  {
    title: "Automation",
    items: [
      { to: "/scripts", label: "Scripts", perm: "scripts.read" },
      { to: "/jobs", label: "Jobs", perm: "scripts.read" },
      { to: "/schedules", label: "Schedules", perm: "scripts.read" },
    ],
  },
  {
    title: "Organization",
    items: [
      { to: "/customers", label: "Customers" },
      { to: "/users", label: "Users" },
      { to: "/tokens", label: "API Tokens" },
    ],
  },
  {
    title: "System",
    items: [
      { to: "/audit", label: "Audit Log" },
      { to: "/settings", label: "Settings" },
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
              {group.items.map((item) => (
                <NavLink
                  key={item.to}
                  to={item.to}
                  onClick={() => setNavOpen(false)}
                  className={({ isActive }) => (isActive ? "active" : "")}
                >
                  {item.label}
                </NavLink>
              ))}
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
    </div>
  );
}
