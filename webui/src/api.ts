export type AuthType = "password" | "private_key";
export type ClientAuthType = "public_key" | "password";

export interface ServerRecord {
  connection_type?: "ssh" | "agent";
  agent_online?: boolean;
  id: number;
  proxy_user: string;
  target_host: string;
  target_port: number;
  route_mode: "fixed" | "dynamic_port";
  port_min: number;
  port_max: number;

  enabled: boolean;

  // 兼容旧设备:勾选后连接该服务器时会额外带上过时的弱加密算法(CBC 类 cipher、老 KEX、
  // ssh-dss host key)做兜底协商,仅用于无法支持现代算法的老旧交换机等设备。
  legacy_algorithms: boolean;

  host_key_fingerprint?: string;

  client_credential_labels: string[];

  last_test_at: string | null;
  last_test_ok: boolean | null;
  last_test_error?: string;

  // 认证信息(目标用户名/密码/私钥)完全来自关联的"服务器凭证",这几个字段都是只读展示,
  // 不能通过表单直接编辑。server_credential_id 留空表示这条服务器暂时没有可用的认证信息。
  target_user?: string;
  auth_type?: AuthType;
  server_credential_id?: number | null;
  server_credential_label?: string;
}

export interface SelfRegistration {
  enabled: boolean;
  server_url: string;
  token: string;
  client_credential_ids: number[];
}

export interface ClientCredential {
  id: number;
  label: string;
  auth_type: ClientAuthType;
  public_key?: string;
  password?: string; // 明文,只在设置/修改密码时非空传入
  has_password: boolean;
  proxy_users: string[];
}

export interface ServerCredential {
  id: number;
  label: string;
  target_user: string;
  auth_type: AuthType;
  auth_password?: string;
  auth_private_key?: string;
  auth_private_key_passphrase?: string;
  proxy_users: string[];
}

export interface AuditLog {
  id: number;
  ts: string;
  proxy_user: string;
  remote_addr: string;
  target_host: string;
  target_port: number;
  event_type: string;
  command?: string;
  output?: string;
  detail?: string;
  exit_status: number | null;
  truncated: boolean;
  status: "running" | "completed";
  client_credential_label: string;
}

export interface ActiveConnection {
  id: number;
  proxy_user: string;
  remote_addr: string;
  target_host: string;
  target_port: number;
  target_user: string;
  client_credential_label: string;
  connected_at: string;
  active_sessions: number;
}

export interface AuditFilters {
  proxyUser?: string;
  targetHost?: string;
  clientCredentialLabel?: string;
}

export const authExpiredEvent = "ssh-proxy-auth-expired";

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
    if (res.status === 401 && path !== "/api/login") {
      window.dispatchEvent(new Event(authExpiredEvent));
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
      body: JSON.stringify({
        OldPassword: oldPassword,
        NewPassword: newPassword,
      }),
    }),

  getSelfRegistration: () =>
    request<SelfRegistration>("/api/settings/agent-registration"),
  saveRegistrationAddress: (serverURL: string) =>
    request<SelfRegistration>("/api/settings/agent-registration/address", {
      method: "PUT",
      body: JSON.stringify({ server_url: serverURL }),
    }),
  generateSelfRegistration: (serverURL: string) =>
    request<{ token: string }>("/api/settings/agent-registration/generate", {
      method: "POST",
      body: JSON.stringify({ server_url: serverURL }),
    }),
  putSelfRegistration: (body: {
    token?: string;
    server_url: string;
    client_credential_ids?: number[];
  }) =>
    request<SelfRegistration>("/api/settings/agent-registration", {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  disableSelfRegistration: () =>
    request<{ ok: boolean }>("/api/settings/agent-registration", {
      method: "DELETE",
    }),

  listServers: () => request<ServerRecord[]>("/api/servers"),
  upsertServer: (server: ServerRecord) =>
    request<{ ok: boolean }>("/api/servers", {
      method: "POST",
      body: JSON.stringify(server),
    }),
  deleteServer: (proxyUser: string) =>
    request<{ ok: boolean }>(`/api/servers/${encodeURIComponent(proxyUser)}`, {
      method: "DELETE",
    }),
  testServer: (proxyUser: string) =>
    request<ServerRecord>(
      `/api/servers/${encodeURIComponent(proxyUser)}/test`,
      { method: "POST" },
    ),
  testAllServers: () =>
    request<ServerRecord[]>("/api/servers/test-all", { method: "POST" }),
  setServerEnabled: (proxyUser: string, enabled: boolean) =>
    request<ServerRecord>(
      `/api/servers/${encodeURIComponent(proxyUser)}/enabled`,
      {
        method: "PUT",
        body: JSON.stringify({ enabled }),
      },
    ),

  listServerCredentials: () =>
    request<ServerCredential[]>("/api/server-credentials"),
  createServerCredential: (cred: Omit<ServerCredential, "id">) =>
    request<{ ok: boolean; id: number }>("/api/server-credentials", {
      method: "POST",
      body: JSON.stringify(cred),
    }),
  updateServerCredential: (id: number, cred: Omit<ServerCredential, "id">) =>
    request<{ ok: boolean }>(`/api/server-credentials/${id}`, {
      method: "PUT",
      body: JSON.stringify(cred),
    }),
  deleteServerCredential: (id: number) =>
    request<{ ok: boolean }>(`/api/server-credentials/${id}`, {
      method: "DELETE",
    }),

  listClientCredentials: () =>
    request<ClientCredential[]>("/api/client-credentials"),
  createClientCredential: (
    cred: Omit<ClientCredential, "id" | "has_password">,
  ) =>
    request<{ ok: boolean; id: number }>("/api/client-credentials", {
      method: "POST",
      body: JSON.stringify(cred),
    }),
  updateClientCredential: (
    id: number,
    cred: Omit<ClientCredential, "id" | "has_password">,
  ) =>
    request<{ ok: boolean }>(`/api/client-credentials/${id}`, {
      method: "PUT",
      body: JSON.stringify(cred),
    }),
  deleteClientCredential: (id: number) =>
    request<{ ok: boolean }>(`/api/client-credentials/${id}`, {
      method: "DELETE",
    }),

  getSettings: () => request<{ listen_addr: string }>("/api/settings"),
  updateSettings: (listenAddr: string) =>
    request<{ ok: boolean }>("/api/settings", {
      method: "PUT",
      body: JSON.stringify({ listen_addr: listenAddr }),
    }),

  listAudit: (limit = 200, filters: AuditFilters = {}) => {
    const params = new URLSearchParams({ limit: String(limit) });
    if (filters.proxyUser) params.set("proxy_user", filters.proxyUser);
    if (filters.targetHost) params.set("target_host", filters.targetHost);
    if (filters.clientCredentialLabel)
      params.set("client_credential_label", filters.clientCredentialLabel);
    return request<AuditLog[]>(`/api/audit?${params}`);
  },
  listConnections: () => request<ActiveConnection[]>("/api/connections"),
};

export { ApiError };
