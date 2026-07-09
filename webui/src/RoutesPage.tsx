import { useEffect, useState } from "react";
import { api, ApiError, type ClientCredential, type RouteRecord, type ServerCredential } from "./api";
import { ChipList } from "./ChipList";
import { Tooltip } from "./Tooltip";

const emptyRoute: RouteRecord = {
  route_user: "",
  target_host: "",
  target_port: 22,
  target_user: "root",
  auth_type: "password",
  auth_password: "",
  auth_private_key: "",
  auth_private_key_passphrase: "",
  enabled: true,
  client_credential_labels: [],
  last_test_at: null,
  last_test_ok: null,
};

export function RoutesPage() {
  const [routes, setRoutes] = useState<RouteRecord[]>([]);
  const [serverCredentials, setServerCredentials] = useState<ServerCredential[]>([]);
  const [clientCredentials, setClientCredentials] = useState<ClientCredential[]>([]);
  const [editing, setEditing] = useState<RouteRecord | null>(null);
  const [useSharedCredential, setUseSharedCredential] = useState(false);
  const [selectedCredentialIds, setSelectedCredentialIds] = useState<Set<number>>(new Set());
  const [error, setError] = useState("");
  const [isNew, setIsNew] = useState(false);
  const [testingRoute, setTestingRoute] = useState<string | null>(null);
  const [testingAll, setTestingAll] = useState(false);

  async function load() {
    const [r, sc, cc] = await Promise.all([
      api.listRoutes(),
      api.listServerCredentials(),
      api.listClientCredentials(),
    ]);
    setRoutes(r ?? []);
    setServerCredentials(sc ?? []);
    setClientCredentials(cc ?? []);
  }

  useEffect(() => {
    load();
  }, []);

  async function testOne(routeUser: string) {
    setTestingRoute(routeUser);
    try {
      const updated = await api.testRoute(routeUser);
      setRoutes((prev) => prev.map((r) => (r.route_user === routeUser ? updated : r)));
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "测试失败");
    } finally {
      setTestingRoute(null);
    }
  }

  async function testAll() {
    setTestingAll(true);
    try {
      const updated = await api.testAllRoutes();
      setRoutes(updated ?? []);
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "测试失败");
    } finally {
      setTestingAll(false);
    }
  }

  async function toggleEnabled(r: RouteRecord) {
    try {
      const updated = await api.setRouteEnabled(r.route_user, !r.enabled);
      setRoutes((prev) => prev.map((x) => (x.route_user === r.route_user ? updated : x)));
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "操作失败");
    }
  }

  function credentialIdsForRoute(routeUser: string): Set<number> {
    return new Set(clientCredentials.filter((c) => c.route_users.includes(routeUser)).map((c) => c.id));
  }

  function startCreate() {
    setEditing({ ...emptyRoute });
    setUseSharedCredential(false);
    setSelectedCredentialIds(new Set());
    setIsNew(true);
    setError("");
  }

  function startEdit(r: RouteRecord) {
    setEditing({
      ...r,
      auth_password: "",
      auth_private_key: "",
      auth_private_key_passphrase: "",
    });
    setUseSharedCredential(r.server_credential_id != null);
    setSelectedCredentialIds(credentialIdsForRoute(r.route_user));
    setIsNew(false);
    setError("");
  }

  function duplicate(r: RouteRecord) {
    setEditing({
      ...r,
      route_user: "",
      auth_password: "",
      auth_private_key: "",
      auth_private_key_passphrase: "",
    });
    setUseSharedCredential(r.server_credential_id != null);
    setSelectedCredentialIds(credentialIdsForRoute(r.route_user));
    setIsNew(true);
    setError("");
  }

  function toggleCredential(id: number) {
    setSelectedCredentialIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }

  async function save() {
    if (!editing) return;
    setError("");
    try {
      await api.upsertRoute({
        ...editing,
        server_credential_id: useSharedCredential ? editing.server_credential_id : null,
      });

      // 双向维护客户端凭据关联:这里按勾选结果,把这个别名加进/移出每份客户端凭据的 route_users。
      const routeUser = editing.route_user;
      for (const c of clientCredentials) {
        const shouldHave = selectedCredentialIds.has(c.id);
        const currentlyHas = c.route_users.includes(routeUser);
        if (shouldHave === currentlyHas) continue;
        const { id, has_password, ...rest } = c;
        void id;
        void has_password;
        const routeUsers = shouldHave ? [...c.route_users, routeUser] : c.route_users.filter((ru) => ru !== routeUser);
        await api.updateClientCredential(c.id, { ...rest, route_users: routeUsers });
      }

      setEditing(null);
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "保存失败");
    }
  }

  async function remove(routeUser: string) {
    if (!confirm(`确定删除服务器 ${routeUser} 吗?`)) return;
    await api.deleteRoute(routeUser);
    await load();
  }

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-slate-900 dark:text-slate-100">后端服务器</h2>
        <div className="flex gap-2">
          <button
            onClick={testAll}
            disabled={testingAll || routes.length === 0}
            className="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 disabled:opacity-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
          >
            {testingAll ? "测试中..." : "测试所有服务器连接"}
          </button>
          <button
            onClick={startCreate}
            className="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500"
          >
            + 添加服务器
          </button>
        </div>
      </div>

      <p className="mb-4 text-sm text-slate-500 dark:text-slate-400">
        哪些客户端凭据能登录这台服务器,可以在这里编辑时勾选,也可以去"客户端凭据"页面管理。
      </p>

      <div className="overflow-x-auto rounded-lg border border-slate-200 dark:border-slate-800">
        <table className="w-full text-left text-sm">
          <thead className="bg-slate-50 text-slate-500 dark:bg-slate-900 dark:text-slate-400">
            <tr>
              <th className="px-4 py-2">登录别名</th>
              <th className="px-4 py-2">目标机器</th>
              <th className="px-4 py-2">目标用户</th>
              <th className="px-4 py-2">状态</th>
              <th className="px-4 py-2">连接测试</th>
              <th className="px-4 py-2">服务器凭据绑定</th>
              <th className="px-4 py-2">客户端凭据绑定</th>
              <th className="px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {routes.map((r) => (
              <tr key={r.route_user} className={`text-slate-800 dark:text-slate-200 ${r.enabled ? "" : "opacity-60"}`}>
                <td className="px-4 py-2 font-mono">{r.route_user}</td>
                <td className="px-4 py-2 font-mono">
                  {r.target_host}:{r.target_port}
                </td>
                <td className="px-4 py-2 font-mono">{r.target_user}</td>
                <td className="px-4 py-2">
                  {r.enabled ? (
                    <span className="text-emerald-600 dark:text-emerald-400">已启用</span>
                  ) : (
                    <span className="text-slate-400">已禁用</span>
                  )}
                </td>
                <td className="px-4 py-2">
                  <TestStatus route={r} />
                </td>
                <td className="px-4 py-2">
                  {r.server_credential_id != null ? (
                    <span className="rounded bg-indigo-100 px-1.5 py-0.5 text-xs text-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-300">
                      凭据: {r.server_credential_label}
                    </span>
                  ) : r.auth_type === "password" ? (
                    "密码"
                  ) : (
                    "私钥"
                  )}
                </td>
                <td className="px-4 py-2">
                  <ChipList items={r.client_credential_labels ?? []} emptyText="无" />
                </td>
                <td className="px-4 py-2 text-right whitespace-nowrap">
                  <button
                    onClick={() => toggleEnabled(r)}
                    className="mr-3 text-indigo-600 hover:underline dark:text-indigo-400"
                  >
                    {r.enabled ? "禁用" : "启用"}
                  </button>
                  <button
                    onClick={() => testOne(r.route_user)}
                    disabled={testingRoute === r.route_user || testingAll}
                    className="mr-3 text-indigo-600 hover:underline disabled:opacity-50 dark:text-indigo-400"
                  >
                    {testingRoute === r.route_user ? "测试中..." : "测试连接"}
                  </button>
                  <button onClick={() => duplicate(r)} className="mr-3 text-indigo-600 hover:underline dark:text-indigo-400">
                    复制
                  </button>
                  <button onClick={() => startEdit(r)} className="mr-3 text-indigo-600 hover:underline dark:text-indigo-400">
                    编辑
                  </button>
                  <button onClick={() => remove(r.route_user)} className="text-red-600 hover:underline dark:text-red-400">
                    删除
                  </button>
                </td>
              </tr>
            ))}
            {routes.length === 0 && (
              <tr>
                <td colSpan={8} className="px-4 py-6 text-center text-slate-400">
                  还没有配置任何服务器
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {editing && (
        <div className="fixed inset-0 flex items-center justify-center bg-black/40 p-4">
          <div className="w-full max-w-lg rounded-xl bg-white p-6 shadow-xl dark:bg-slate-950">
            <h3 className="mb-4 text-lg font-semibold text-slate-900 dark:text-slate-100">
              {isNew ? "添加后端服务器" : `编辑 ${editing.route_user}`}
            </h3>

            <div className="grid grid-cols-2 gap-3">
              <Field label="登录别名 (proxy 用户名,唯一)">
                <input
                  disabled={!isNew}
                  className="input"
                  value={editing.route_user}
                  onChange={(e) => setEditing({ ...editing, route_user: e.target.value })}
                />
              </Field>
              <Field label="目标机器 IP/域名">
                <input
                  className="input"
                  value={editing.target_host}
                  onChange={(e) => setEditing({ ...editing, target_host: e.target.value })}
                />
              </Field>
              <Field label="目标端口">
                <input
                  type="number"
                  className="input"
                  value={editing.target_port}
                  onChange={(e) => setEditing({ ...editing, target_port: Number(e.target.value) })}
                />
              </Field>
              <Field label="目标机器用户名">
                <input
                  className="input"
                  value={editing.target_user}
                  onChange={(e) => setEditing({ ...editing, target_user: e.target.value })}
                />
              </Field>
            </div>

            <Field label="连接目标机器的认证方式">
              <div className="mb-2 flex gap-4 text-sm text-slate-700 dark:text-slate-300">
                <label className="flex items-center gap-1.5">
                  <input type="radio" checked={!useSharedCredential} onChange={() => setUseSharedCredential(false)} />
                  单独指定
                </label>
                <label className="flex items-center gap-1.5">
                  <input type="radio" checked={useSharedCredential} onChange={() => setUseSharedCredential(true)} />
                  使用服务器凭据
                </label>
              </div>
            </Field>

            {useSharedCredential ? (
              <Field label="选择服务器凭据">
                {serverCredentials.length === 0 ? (
                  <p className="text-sm text-slate-400">还没有配置任何服务器凭据,先去"服务器凭据"页面添加</p>
                ) : (
                  <select
                    className="input"
                    value={editing.server_credential_id ?? ""}
                    onChange={(e) => setEditing({ ...editing, server_credential_id: Number(e.target.value) })}
                  >
                    <option value="" disabled>
                      请选择
                    </option>
                    {serverCredentials.map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.label}
                      </option>
                    ))}
                  </select>
                )}
              </Field>
            ) : (
              <>
                <Field label="认证方式">
                  <select
                    className="input"
                    value={editing.auth_type}
                    onChange={(e) =>
                      setEditing({ ...editing, auth_type: e.target.value as "password" | "private_key" })
                    }
                  >
                    <option value="password">密码</option>
                    <option value="private_key">私钥</option>
                  </select>
                </Field>

                {editing.auth_type === "password" ? (
                  <Field label="密码 (留空则不修改)">
                    <input
                      type="password"
                      className="input"
                      value={editing.auth_password}
                      onChange={(e) => setEditing({ ...editing, auth_password: e.target.value })}
                    />
                  </Field>
                ) : (
                  <>
                    <Field label="私钥内容 (PEM,留空则不修改)">
                      <textarea
                        className="input h-24 font-mono"
                        value={editing.auth_private_key}
                        onChange={(e) => setEditing({ ...editing, auth_private_key: e.target.value })}
                      />
                    </Field>
                    <Field label="私钥密码 (如果有)">
                      <input
                        type="password"
                        className="input"
                        value={editing.auth_private_key_passphrase}
                        onChange={(e) => setEditing({ ...editing, auth_private_key_passphrase: e.target.value })}
                      />
                    </Field>
                  </>
                )}
              </>
            )}

            <Field label="哪些客户端凭据能登录这台服务器">
              <div className="max-h-40 space-y-1 overflow-y-auto rounded-md border border-slate-300 p-2 dark:border-slate-700">
                {clientCredentials.length === 0 && (
                  <p className="text-sm text-slate-400">还没有配置任何客户端凭据,先去"客户端凭据"页面添加</p>
                )}
                {clientCredentials.map((c) => (
                  <label key={c.id} className="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-300">
                    <input
                      type="checkbox"
                      checked={selectedCredentialIds.has(c.id)}
                      onChange={() => toggleCredential(c.id)}
                    />
                    <span>{c.label}</span>
                    <span className="text-xs text-slate-400">({c.auth_type === "public_key" ? "公钥" : "密码"})</span>
                  </label>
                ))}
              </div>
            </Field>

            {error && <p className="mb-2 text-sm text-red-600 dark:text-red-400">{error}</p>}

            <div className="mt-4 flex justify-end gap-2">
              <button
                onClick={() => setEditing(null)}
                className="rounded-md border border-slate-300 px-3 py-1.5 text-sm dark:border-slate-700 dark:text-slate-200"
              >
                取消
              </button>
              <button
                onClick={save}
                className="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500"
              >
                保存
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="mb-3">
      <label className="mb-1 block text-xs text-slate-500 dark:text-slate-400">{label}</label>
      {children}
    </div>
  );
}

function TestStatus({ route }: { route: RouteRecord }) {
  if (!route.last_test_at || route.last_test_ok === null) {
    return <span className="text-xs text-slate-400">尚未测试</span>;
  }

  const time = new Date(route.last_test_at).toLocaleString();

  if (route.last_test_ok) {
    return (
      <div className="text-xs">
        <span className="text-emerald-600 dark:text-emerald-400">成功</span>
        <div className="text-slate-400">{time}</div>
      </div>
    );
  }

  return (
    <div className="text-xs">
      <Tooltip text={route.last_test_error || "未知错误"}>
        <span className="cursor-help text-red-600 underline decoration-dotted dark:text-red-400">失败</span>
      </Tooltip>
      <div className="text-slate-400">{time}</div>
    </div>
  );
}
