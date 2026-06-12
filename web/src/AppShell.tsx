import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { logout } from "./api/client";
import { useAuth } from "./auth";

const NAV = [
  { to: "/devices", label: "Devices" },
  { to: "/alerts", label: "Alerts", perm: "alerts.read" },
  { to: "/policies", label: "Policies", perm: "policies.read" },
  { to: "/scripts", label: "Scripts", perm: "scripts.read" },
  { to: "/jobs", label: "Jobs", perm: "scripts.read" },
  { to: "/schedules", label: "Schedules", perm: "scripts.read" },
  { to: "/enroll", label: "Enrollment", perm: "devices.enroll" },
  { to: "/customers", label: "Customers" },
  { to: "/users", label: "Users" },
  { to: "/tokens", label: "API Tokens" },
  { to: "/audit", label: "Audit Log" },
  { to: "/settings", label: "Settings" },
];

export function AppShell() {
  const { me, can } = useAuth();
  const navigate = useNavigate();
  const qc = useQueryClient();

  async function onLogout() {
    try {
      await logout();
    } finally {
      qc.clear();
      navigate("/login", { replace: true });
    }
  }

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">rmmagic</div>
        <nav>
          {NAV.filter((item) => !item.perm || can(item.perm)).map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) => (isActive ? "active" : "")}
            >
              {item.label}
            </NavLink>
          ))}
        </nav>
      </aside>
      <div className="shell-main">
        <header className="topbar">
          <span className="tenant">{me.tenant.name}</span>
          <span className="who">
            <span>{me.user.email}</span>
            <button type="button" onClick={() => void onLogout()}>
              Log out
            </button>
          </span>
        </header>
        <main className="content">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
