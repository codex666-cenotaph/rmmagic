import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import {
  QueryCache,
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query";
import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom";
import { ApiError } from "./api/client";
import { RequireAuth } from "./auth";
import { ThemeProvider } from "./theme";
import { AppShell } from "./AppShell";
import { LoginPage } from "./pages/LoginPage";
import { DashboardPage } from "./pages/DashboardPage";
import { DevicesPage } from "./pages/DevicesPage";
import { DeviceDetailPage } from "./pages/DeviceDetailPage";
import { ScriptsPage } from "./pages/ScriptsPage";
import { JobsPage } from "./pages/JobsPage";
import { SchedulesPage, HealthChecksPage } from "./pages/SchedulesPage";
import { AppsPage } from "./pages/AppsPage";
import { DeploymentsPage } from "./pages/DeploymentsPage";
import { UpdatesPage } from "./pages/UpdatesPage";
import { EnrollPage } from "./pages/EnrollPage";
import { CustomersPage } from "./pages/CustomersPage";
import { UsersPage } from "./pages/UsersPage";
import { TokensPage } from "./pages/TokensPage";
import { AuditPage } from "./pages/AuditPage";
import { AlertsPage } from "./pages/AlertsPage";
import { PoliciesPage } from "./pages/PoliciesPage";
import { SettingsPage } from "./pages/SettingsPage";
import "./styles.css";

const queryClient = new QueryClient({
  queryCache: new QueryCache({
    onError: (error, query) => {
      // Any 401 means the session is gone; re-check /auth/me so RequireAuth
      // redirects to /login.
      if (
        error instanceof ApiError &&
        error.status === 401 &&
        query.queryKey[0] !== "me"
      ) {
        void queryClient.invalidateQueries({ queryKey: ["me"] });
      }
    },
  }),
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <Routes>
            <Route path="/login" element={<LoginPage />} />
            <Route
              element={
                <RequireAuth>
                  <AppShell />
                </RequireAuth>
              }
            >
              <Route path="/dashboard" element={<DashboardPage />} />
              <Route path="/devices" element={<DevicesPage />} />
              <Route path="/devices/:id" element={<DeviceDetailPage />} />
              <Route path="/scripts" element={<ScriptsPage />} />
              <Route path="/jobs" element={<JobsPage />} />
              <Route path="/schedules" element={<SchedulesPage />} />
              <Route path="/health-checks" element={<HealthChecksPage />} />
              <Route path="/deployments" element={<DeploymentsPage />} />
              <Route path="/apps" element={<AppsPage />} />
              <Route path="/updates" element={<UpdatesPage />} />
              <Route path="/enroll" element={<EnrollPage />} />
              <Route path="/customers" element={<CustomersPage />} />
              <Route path="/users" element={<UsersPage />} />
              <Route path="/tokens" element={<TokensPage />} />
              <Route path="/audit" element={<AuditPage />} />
              <Route path="/alerts" element={<AlertsPage />} />
              <Route path="/policies" element={<PoliciesPage />} />
              <Route path="/settings" element={<SettingsPage />} />
            </Route>
            <Route path="*" element={<Navigate to="/dashboard" replace />} />
          </Routes>
        </BrowserRouter>
      </QueryClientProvider>
    </ThemeProvider>
  </StrictMode>,
);
