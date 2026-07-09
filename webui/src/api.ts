export type AuthType = "password" | "private_key";
export type ClientAuthType = "public_key" | "password";

export interface ServerRecord {
  login_name: string;
  target_host: string;
  target_port: number;

  enabled: boolean;

  client_credential_labels: string[];

  last_test_at: string | null;
  last_test_ok: boolean | null;
  last_test_error?: string;

  // 认证信息(目标用户名/密码/私钥)完全来自关联的"服务器凭据",这几个字段都是只读展示,
  // 不能通过表单直接编辑。server_credential_id 留空表示这条服务器暂时没有可用的认证信息。
  target_user?: string;
  auth_type?: AuthType;
  server_credential_id?: number | null;
  server_credential_label?: string;
}

export interface ClientCredential {
  id: number;
  label: string;
  auth_type: ClientAuthType;
  public_key?: string;
  password?: string; // 明文,只在设置/修改密码时非空传入
  has_password: boolean;
  login_names: string[];
}

export interface ServerCredential {
  id: number;
  label: string;
  target_user: string;
  auth_type: AuthType;
  auth_password?: string;
  auth_private_key?: string;
  auth_private_key_passphrase?: string;
  login_names: string[];
}

export interface AuditLog {
  id: number;
  ts: string;
  login_name: string;
  remote_addr: string;
  target_host: string;
  target_port: number;
  event_type: string;
  detail: string;
  exit_status: number | null;
  truncated: boolean;
  client_credential_label: string;
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

  listServers: () => request<ServerRecord[]>("/api/servers"),
  upsertServer: (server: ServerRecord) =>
    request<{ ok: boolean }>("/api/servers", {
      method: "POST",
      body: JSON.stringify(server),
    }),
  deleteServer: (loginName: string) =>
    request<{ ok: boolean }>(`/api/servers/${encodeURIComponent(loginName)}`, {
      method: "DELETE",
    }),
  testServer: (loginName: string) =>
    request<ServerRecord>(`/api/servers/${encodeURIComponent(loginName)}/test`, { method: "POST" }),
  testAllServers: () => request<ServerRecord[]>("/api/servers/test-all", { method: "POST" }),
  setServerEnabled: (loginName: string, enabled: boolean) =>
    request<ServerRecord>(`/api/servers/${encodeURIComponent(loginName)}/enabled`, {
      method: "PUT",
      body: JSON.stringify({ enabled }),
    }),

  listServerCredentials: () => request<ServerCredential[]>("/api/server-credentials"),
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
    request<{ ok: boolean }>(`/api/server-credentials/${id}`, { method: "DELETE" }),

  listClientCredentials: () => request<ClientCredential[]>("/api/client-credentials"),
  createClientCredential: (cred: Omit<ClientCredential, "id" | "has_password">) =>
    request<{ ok: boolean; id: number }>("/api/client-credentials", {
      method: "POST",
      body: JSON.stringify(cred),
    }),
  updateClientCredential: (id: number, cred: Omit<ClientCredential, "id" | "has_password">) =>
    request<{ ok: boolean }>(`/api/client-credentials/${id}`, {
      method: "PUT",
      body: JSON.stringify(cred),
    }),
  deleteClientCredential: (id: number) =>
    request<{ ok: boolean }>(`/api/client-credentials/${id}`, { method: "DELETE" }),

  getSettings: () => request<{ listen_addr: string }>("/api/settings"),
  updateSettings: (listenAddr: string) =>
    request<{ ok: boolean }>("/api/settings", {
      method: "PUT",
      body: JSON.stringify({ listen_addr: listenAddr }),
    }),

  listAudit: (limit = 200, loginName = "") =>
    request<AuditLog[]>(
      `/api/audit?limit=${limit}${loginName ? `&login_name=${encodeURIComponent(loginName)}` : ""}`
    ),
};

export { ApiError };
