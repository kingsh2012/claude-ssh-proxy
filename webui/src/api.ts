export type AuthType = "password" | "private_key";

export interface RouteRecord {
  route_user: string;
  target_host: string;
  target_port: number;
  target_user: string;
  auth_type: AuthType;
  auth_password?: string;
  auth_private_key?: string;
  auth_private_key_passphrase?: string;

  client_key_labels: string[];

  listen_password?: string;
  clear_listen_password?: boolean;
  has_listen_password: boolean;

  last_test_at: string | null;
  last_test_ok: boolean | null;
  last_test_error?: string;
}

export interface ClientKey {
  id: number;
  label: string;
  public_key: string;
  route_users: string[];
}

export interface AuditLog {
  id: number;
  ts: string;
  route_user: string;
  remote_addr: string;
  target_host: string;
  target_port: number;
  event_type: string;
  detail: string;
  exit_status: number | null;
  truncated: boolean;
  client_key_label: string;
}

class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    ...init,
  });
  if (!res.ok) {
    let message = res.statusText;
    try {
      const body = await res.json();
      if (body?.error) message = body.error;
    } catch {
      /* ignore */
    }
    throw new ApiError(res.status, message);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

export interface MeResponse {
  username: string;
  initialized: boolean;
}

export const api = {
  login: (username: string, password: string) =>
    request<MeResponse>("/api/login", {
      method: "POST",
      body: JSON.stringify({ Username: username, Password: password }),
    }),
  logout: () => request<{ ok: boolean }>("/api/logout", { method: "POST" }),
  me: () => request<MeResponse>("/api/me"),
  changePassword: (oldPassword: string, newPassword: string) =>
    request<{ ok: boolean }>("/api/admin/password", {
      method: "PUT",
      body: JSON.stringify({ OldPassword: oldPassword, NewPassword: newPassword }),
    }),

  listRoutes: () => request<RouteRecord[]>("/api/routes"),
  upsertRoute: (route: RouteRecord) =>
    request<{ ok: boolean }>("/api/routes", {
      method: "POST",
      body: JSON.stringify(route),
    }),
  deleteRoute: (routeUser: string) =>
    request<{ ok: boolean }>(`/api/routes/${encodeURIComponent(routeUser)}`, {
      method: "DELETE",
    }),
  testRoute: (routeUser: string) =>
    request<RouteRecord>(`/api/routes/${encodeURIComponent(routeUser)}/test`, { method: "POST" }),
  testAllRoutes: () => request<RouteRecord[]>("/api/routes/test-all", { method: "POST" }),

  listClientKeys: () => request<ClientKey[]>("/api/client-keys"),
  createClientKey: (key: Omit<ClientKey, "id">) =>
    request<{ ok: boolean; id: number }>("/api/client-keys", {
      method: "POST",
      body: JSON.stringify(key),
    }),
  updateClientKey: (id: number, key: Omit<ClientKey, "id">) =>
    request<{ ok: boolean }>(`/api/client-keys/${id}`, {
      method: "PUT",
      body: JSON.stringify(key),
    }),
  deleteClientKey: (id: number) =>
    request<{ ok: boolean }>(`/api/client-keys/${id}`, { method: "DELETE" }),

  getSettings: () => request<{ listen_addr: string }>("/api/settings"),
  updateSettings: (listenAddr: string) =>
    request<{ ok: boolean }>("/api/settings", {
      method: "PUT",
      body: JSON.stringify({ listen_addr: listenAddr }),
    }),

  listAudit: (limit = 200, routeUser = "") =>
    request<AuditLog[]>(
      `/api/audit?limit=${limit}${routeUser ? `&route_user=${encodeURIComponent(routeUser)}` : ""}`
    ),
};

export { ApiError };
