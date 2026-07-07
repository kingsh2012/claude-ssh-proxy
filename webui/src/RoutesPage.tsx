import { useEffect, useState } from "react";
import { api, ApiError, type RouteRecord } from "./api";

const emptyRoute: RouteRecord = {
  route_user: "",
  target_host: "",
  target_port: 22,
  target_user: "root",
  auth_type: "password",
  auth_password: "",
  auth_private_key: "",
  auth_private_key_passphrase: "",
  client_key_labels: [],
  listen_password: "",
  clear_listen_password: false,
  has_listen_password: false,
  last_test_at: null,
  last_test_ok: null,
};

export function RoutesPage() {
  const [routes, setRoutes] = useState<RouteRecord[]>([]);
  const [editing, setEditing] = useState<RouteRecord | null>(null);
  const [error, setError] = useState("");
  const [isNew, setIsNew] = useState(false);
  const [testingRoute, setTestingRoute] = useState<string | null>(null);
  const [testingAll, setTestingAll] = useState(false);

  async function load() {
    setRoutes((await api.listRoutes()) ?? []);
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

  function startCreate() {
    setEditing({ ...emptyRoute });
    setIsNew(true);
    setError("");
  }

  function startEdit(r: RouteRecord) {
    setEditing({
      ...r,
      auth_password: "",
      auth_private_key: "",
      auth_private_key_passphrase: "",
      listen_password: "",
      clear_listen_password: false,
    });
    setIsNew(false);
    setError("");
  }

  async function save() {
    if (!editing) return;
    setError("");
    try {
      await api.upsertRoute(editing);
      setEditing(null);
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "保存失败");
    }
  }

  async function remove(routeUser: string) {
    if (!confirm(`确定删除路由 ${routeUser} 吗?`)) return;
    await api.deleteRoute(routeUser);
    await load();
  }

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-slate-900 dark:text-slate-100">后端服务器路由</h2>
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
        哪些客户端公钥能登录这个别名,在"客户端密钥"页面管理。
      </p>

      <div className="overflow-x-auto rounded-lg border border-slate-200 dark:border-slate-800">
        <table className="w-full text-left text-sm">
          <thead className="bg-slate-50 text-slate-500 dark:bg-slate-900 dark:text-slate-400">
            <tr>
              <th className="px-4 py-2">登录别名</th>
              <th className="px-4 py-2">目标机器</th>
              <th className="px-4 py-2">目标用户</th>
              <th className="px-4 py-2">认证方式</th>
              <th className="px-4 py-2">关联的客户端密钥</th>
              <th className="px-4 py-2">密码登录</th>
              <th className="px-4 py-2">连接测试</th>
              <th className="px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {routes.map((r) => (
              <tr key={r.route_user} className="text-slate-800 dark:text-slate-200">
                <td className="px-4 py-2 font-mono">{r.route_user}</td>
                <td className="px-4 py-2 font-mono">
                  {r.target_host}:{r.target_port}
                </td>
                <td className="px-4 py-2 font-mono">{r.target_user}</td>
                <td className="px-4 py-2">{r.auth_type === "password" ? "密码" : "私钥"}</td>
                <td className="px-4 py-2">
                  {(r.client_key_labels ?? []).length === 0 ? (
                    <span className="text-slate-400">无</span>
                  ) : (
                    <div className="flex flex-wrap gap-1">
                      {r.client_key_labels.map((label) => (
                        <span
                          key={label}
                          className="rounded bg-slate-100 px-1.5 py-0.5 text-xs dark:bg-slate-800"
                        >
                          {label}
                        </span>
                      ))}
                    </div>
                  )}
                </td>
                <td className="px-4 py-2">
                  {r.has_listen_password ? (
                    <span className="text-emerald-600 dark:text-emerald-400">已启用</span>
                  ) : (
                    <span className="text-slate-400">未启用</span>
                  )}
                </td>
                <td className="px-4 py-2">
                  <TestStatus route={r} />
                </td>
                <td className="px-4 py-2 text-right">
                  <button
                    onClick={() => testOne(r.route_user)}
                    disabled={testingRoute === r.route_user || testingAll}
                    className="mr-3 text-indigo-600 hover:underline disabled:opacity-50 dark:text-indigo-400"
                  >
                    {testingRoute === r.route_user ? "测试中..." : "测试连接"}
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
                  还没有配置任何路由
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
              <Field label="登录别名 (proxy 用户名)">
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
              <select
                className="input"
                value={editing.auth_type}
                onChange={(e) => setEditing({ ...editing, auth_type: e.target.value as "password" | "private_key" })}
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

            <Field
              label={
                editing.has_listen_password
                  ? "登录密码 (已设置,留空则不修改;和关联的客户端公钥并存,任一种都能登录)"
                  : "登录密码 (可选,留空则只能靠关联的客户端公钥登录)"
              }
            >
              <input
                type="password"
                className="input"
                value={editing.listen_password}
                disabled={editing.clear_listen_password}
                onChange={(e) => setEditing({ ...editing, listen_password: e.target.value })}
              />
              {editing.has_listen_password && (
                <label className="mt-1 flex items-center gap-2 text-xs text-slate-500 dark:text-slate-400">
                  <input
                    type="checkbox"
                    checked={editing.clear_listen_password}
                    onChange={(e) =>
                      setEditing({ ...editing, clear_listen_password: e.target.checked, listen_password: "" })
                    }
                  />
                  移除密码登录,只保留公钥
                </label>
              )}
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
    <div className="text-xs" title={route.last_test_error || "未知错误"}>
      <span className="cursor-help text-red-600 underline decoration-dotted dark:text-red-400">失败</span>
      <div className="text-slate-400">{time}</div>
    </div>
  );
}
