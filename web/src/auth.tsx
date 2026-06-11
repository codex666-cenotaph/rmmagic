import { createContext, ReactNode, useContext } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Navigate } from "react-router-dom";
import { ApiError, getMe, Me } from "./api/client";

interface AuthValue {
  me: Me;
  /** True if any grant (any scope) includes the permission. */
  can: (permission: string) => boolean;
}

const AuthContext = createContext<AuthValue | null>(null);

export function useAuth(): AuthValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used inside RequireAuth");
  return ctx;
}

export function useMeQuery() {
  return useQuery({
    queryKey: ["me"],
    queryFn: getMe,
    retry: (failureCount, error) =>
      !(error instanceof ApiError && error.status === 401) && failureCount < 2,
    staleTime: 60_000,
  });
}

export function useInvalidateAll() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries();
}

/** Gate: renders children only when authenticated; 401 redirects to /login. */
export function RequireAuth({ children }: { children: ReactNode }) {
  const { data: me, isLoading, error } = useMeQuery();

  if (isLoading) {
    return <p style={{ padding: 24 }}>Loading…</p>;
  }
  if (error instanceof ApiError && error.status === 401) {
    return <Navigate to="/login" replace />;
  }
  if (error || !me) {
    return (
      <p className="error" style={{ padding: 24 }}>
        Failed to load session: {error instanceof Error ? error.message : "unknown error"}
      </p>
    );
  }

  const can = (permission: string) =>
    me.grants.some((g) => g.permissions.includes(permission));

  return (
    <AuthContext.Provider value={{ me, can }}>{children}</AuthContext.Provider>
  );
}
