import { useEffect, useState } from "react";
import { api, ApiError, type ClientCredential, type RouteRecord, type ServerCredential } from "./api";
import { ChipList } from "./ChipList";
import { Tooltip } from "./Tooltip";

const emptyRoute: RouteRecord = {
  route_user: "",
  target_host: "",
  target_port: 22,
  enabled: true,
  client_credential_labels: [],
  last_test_at: null,
  last_test_ok: null,
  server_credential_id: null,
};

// reconcileClientCredentials 把"这个别名应该关联哪些客户端凭据"落地成实际的 API 调用:
// 对比每份客户端凭据当前的 route_users 和期望的勾选结果,只在有变化的凭据上调用更新接口。
// save() 和 CSV 导入(逐行调用)都用这个函数,保证行为一致。
//
// 注意:成功调用后会把 c.route_users 就地更新——批量导入时如果好几行服务器共用同一份
// 客户端凭据,必须让后面几行看到前面几行刚写入的关联,否则会互相覆盖,只有最后一行生效。
async function reconcileClientCredentials(
  clientCredentials: ClientCredential[],
  routeUser: string,
  selectedIds: Set<number>
) {
  for (const c of clientCredentials) {
    const shouldHave = selectedIds.has(c.id);
    const currentlyHas = c.route_users.includes(routeUser);
    if (shouldHave === currentlyHas) continue;
    const { id, has_password, ...rest } = c;
    void id;
    void has_password;
    const routeUsers = shouldHave ? [...c.route_users, routeUser] : c.route_users.filter((ru) => ru !== routeUser);
    await api.updateClientCredential(c.id, { ...rest, route_users: routeUsers });
    c.route_users = routeUsers;
  }
}

export function RoutesPage() {
  const [routes, setRoutes] = useState<RouteRecord[]>([]);
  const [serverCredentials, setServerCredentials] = useState<ServerCredential[]>([]);
  const [clientCredentials, setClientCredentials] = useState<ClientCredential[]>([]);
  const [editing, setEditing] = useState<RouteRecord | null>(null);
  const [selectedCredentialIds, setSelectedCredentialIds] = useState<Set<number>>(new Set());
  const [error, setError] = useState("");
  const [isNew, setIsNew] = useState(false);
  const [testingRoute, setTestingRoute] = useState<string | null>(null);
  const [testingAll, setTestingAll] = useState(false);
  const [importing, setImporting] = useState(false);

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
    setSelectedCredentialIds(new Set());
    setIsNew(true);
    setError("");
  }

  function startEdit(r: RouteRecord) {
    setEditing({ ...r });
    setSelectedCredentialIds(credentialIdsForRoute(r.route_user));
    setIsNew(false);
    setError("");
  }

  function duplicate(r: RouteRecord) {
    setEditing({ ...r, route_user: "" });
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
      await api.upsertRoute(editing);
      await reconcileClientCredentials(clientCredentials, editing.route_user, selectedCredentialIds);
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
            onClick={() => setImporting(true)}
            className="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
          >
            导入
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
        每个登录别名对应一台真实机器,连接目标机器用的服务器凭据、能登录它的客户端凭据都在这里关联——两边的关联关系在"服务器凭据""客户端凭据"页面也能反过来编辑。
      </p>

      <div className="overflow-x-auto rounded-lg border border-slate-200 dark:border-slate-800">
        <table className="w-full text-left text-sm">
          <thead className="bg-slate-50 text-slate-500 dark:bg-slate-900 dark:text-slate-400">
            <tr>
              <th className="px-4 py-2">登录别名</th>
              <th className="px-4 py-2">目标SSH服务器</th>
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
                      {r.server_credential_label}({r.target_user})
                    </span>
                  ) : (
                    <span className="text-slate-400">未设置</span>
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
                <td colSpan={7} className="px-4 py-6 text-center text-slate-400">
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

            <Field label="代理登录名（唯一）">
              <input
                disabled={!isNew}
                className="input"
                value={editing.route_user}
                onChange={(e) => setEditing({ ...editing, route_user: e.target.value })}
              />
            </Field>

            <Field label="目标SSH服务器IP/域名">
              <input
                className="input"
                value={editing.target_host}
                onChange={(e) => setEditing({ ...editing, target_host: e.target.value })}
              />
            </Field>

            <Field label="目标SSH服务器端口">
              <input
                type="number"
                className="input"
                value={editing.target_port}
                onChange={(e) => setEditing({ ...editing, target_port: Number(e.target.value) })}
              />
            </Field>

            <Field label="服务器凭据(提供SSH登录名+密码/私钥)">
              {serverCredentials.length === 0 ? (
                <p className="text-sm text-slate-400">还没有配置任何服务器凭据,先去"服务器凭据"页面添加</p>
              ) : (
                <select
                  className="input"
                  value={editing.server_credential_id ?? ""}
                  onChange={(e) =>
                    setEditing({
                      ...editing,
                      server_credential_id: e.target.value === "" ? null : Number(e.target.value),
                    })
                  }
                >
                  <option value="">(不设置)</option>
                  {serverCredentials.map((c) => (
                    <option key={c.id} value={c.id}>
                      #{c.id} {c.label}({c.target_user})
                    </option>
                  ))}
                </select>
              )}
            </Field>

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
                    <span>
                      #{c.id} {c.label}
                    </span>
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

      {importing && (
        <ImportModal
          routes={routes}
          serverCredentials={serverCredentials}
          clientCredentials={clientCredentials}
          onClose={() => setImporting(false)}
          onDone={load}
        />
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

const IMPORT_HEADER = "route_user,target_host,target_port,server_credential_id,client_credential_id";

interface ParsedImportRow {
  line: number; // 1-based,含表头
  route_user: string;
  target_host: string;
  target_port: number;
  server_credential_id: number | null;
  client_credential_ids: number[];
}

interface ImportRowResult {
  line: number;
  route_user: string;
  ok: boolean;
  message: string;
}

function parseImportCSV(
  text: string,
  serverCredentials: ServerCredential[],
  clientCredentials: ClientCredential[]
): { rows: ParsedImportRow[]; errors: string[] } {
  const errors: string[] = [];
  const lines = text.split(/\r\n|\n/).filter((l, i, arr) => l.trim() !== "" || i < arr.length - 1);
  if (lines.length === 0) {
    return { rows: [], errors: ["内容不能为空"] };
  }

  const header = lines[0].trim();
  if (header !== IMPORT_HEADER) {
    return { rows: [], errors: [`表头必须是: ${IMPORT_HEADER}`] };
  }

  const serverCredIds = new Set(serverCredentials.map((c) => c.id));
  const clientCredIds = new Set(clientCredentials.map((c) => c.id));

  const rows: ParsedImportRow[] = [];
  for (let i = 1; i < lines.length; i++) {
    const lineNo = i + 1;
    const raw = lines[i];
    if (raw.trim() === "") continue;
    const cols = raw.split(",");
    if (cols.length !== 5) {
      errors.push(`第 ${lineNo} 行:应为 5 列,实际 ${cols.length} 列`);
      continue;
    }
    const [routeUserRaw, targetHostRaw, targetPortRaw, serverCredRaw, clientCredRaw] = cols.map((c) => c.trim());

    if (!routeUserRaw) {
      errors.push(`第 ${lineNo} 行:route_user 不能为空`);
    }
    if (!targetHostRaw) {
      errors.push(`第 ${lineNo} 行:target_host 不能为空`);
    }

    let targetPort = 22;
    if (targetPortRaw !== "") {
      const n = Number(targetPortRaw);
      if (!Number.isInteger(n) || n < 1 || n > 65535) {
        errors.push(`第 ${lineNo} 行:target_port "${targetPortRaw}" 不合法,应为 1-65535 的整数或留空`);
      } else {
        targetPort = n;
      }
    }

    let serverCredentialId: number | null = null;
    if (serverCredRaw !== "") {
      const n = Number(serverCredRaw);
      if (!Number.isInteger(n) || !serverCredIds.has(n)) {
        errors.push(`第 ${lineNo} 行:server_credential_id "${serverCredRaw}" 不存在`);
      } else {
        serverCredentialId = n;
      }
    }

    const clientCredentialIds: number[] = [];
    if (clientCredRaw !== "") {
      for (const part of clientCredRaw.split(";")) {
        const p = part.trim();
        if (p === "") continue;
        const n = Number(p);
        if (!Number.isInteger(n) || !clientCredIds.has(n)) {
          errors.push(`第 ${lineNo} 行:client_credential_id "${p}" 不存在`);
        } else {
          clientCredentialIds.push(n);
        }
      }
    }

    rows.push({
      line: lineNo,
      route_user: routeUserRaw,
      target_host: targetHostRaw,
      target_port: targetPort,
      server_credential_id: serverCredentialId,
      client_credential_ids: clientCredentialIds,
    });
  }

  return { rows, errors };
}

function ImportModal({
  routes,
  serverCredentials,
  clientCredentials,
  onClose,
  onDone,
}: {
  routes: RouteRecord[];
  serverCredentials: ServerCredential[];
  clientCredentials: ClientCredential[];
  onClose: () => void;
  onDone: () => Promise<void>;
}) {
  const [text, setText] = useState("");
  const [errors, setErrors] = useState<string[]>([]);
  const [results, setResults] = useState<ImportRowResult[] | null>(null);
  const [running, setRunning] = useState(false);

  async function runImport() {
    setResults(null);
    const { rows, errors: parseErrors } = parseImportCSV(text, serverCredentials, clientCredentials);
    if (parseErrors.length > 0) {
      setErrors(parseErrors);
      return;
    }
    setErrors([]);
    setRunning(true);

    // 拷贝一份客户端凭据快照,让整批导入过程中的 route_users 变化在批内累积、
    // 又不直接改动父组件的 state(reconcileClientCredentials 会就地更新这份拷贝)。
    const workingCredentials = clientCredentials.map((c) => ({ ...c, route_users: [...c.route_users] }));

    const existingUsers = new Set(routes.map((r) => r.route_user));
    const rowResults: ImportRowResult[] = [];
    for (const row of rows) {
      const wasExisting = existingUsers.has(row.route_user);
      try {
        await api.upsertRoute({
          ...emptyRoute,
          route_user: row.route_user,
          target_host: row.target_host,
          target_port: row.target_port,
          server_credential_id: row.server_credential_id,
        });
        await reconcileClientCredentials(workingCredentials, row.route_user, new Set(row.client_credential_ids));
        rowResults.push({
          line: row.line,
          route_user: row.route_user,
          ok: true,
          message: wasExisting ? "已更新" : "已新增",
        });
      } catch (err) {
        rowResults.push({
          line: row.line,
          route_user: row.route_user,
          ok: false,
          message: err instanceof ApiError ? err.message : "失败",
        });
      }
    }

    setResults(rowResults);
    setRunning(false);
    await onDone();
  }

  return (
    <div className="fixed inset-0 flex items-center justify-center bg-black/40 p-4">
      <div className="w-full max-w-2xl rounded-xl bg-white p-6 shadow-xl dark:bg-slate-950">
        <h3 className="mb-4 text-lg font-semibold text-slate-900 dark:text-slate-100">导入服务器(CSV)</h3>

        <p className="mb-2 text-sm text-slate-500 dark:text-slate-400">
          第一行必须是表头,后面每行一台服务器。<code className="rounded bg-slate-100 px-1 dark:bg-slate-800">route_user</code>
          {" "}是唯一键,已存在则覆盖更新,不存在则新增。<code className="rounded bg-slate-100 px-1 dark:bg-slate-800">target_port</code>
          /<code className="rounded bg-slate-100 px-1 dark:bg-slate-800">server_credential_id</code>/
          <code className="rounded bg-slate-100 px-1 dark:bg-slate-800">client_credential_id</code> 可以留空,分别默认
          22、不关联服务器凭据、不关联客户端凭据;<code className="rounded bg-slate-100 px-1 dark:bg-slate-800">client_credential_id</code>
          {" "}一个格子里可以用分号分隔多个 id。
        </p>
        <pre className="mb-3 overflow-x-auto rounded bg-slate-50 p-2 text-xs text-slate-600 dark:bg-slate-900 dark:text-slate-300">
{`${IMPORT_HEADER}
srv1,192.168.1.2,,1,1;2
srv2,192.168.1.3,22,,3
srv3,192.168.1.4,,,`}
        </pre>

        <textarea
          className="input h-40 font-mono text-xs"
          placeholder={IMPORT_HEADER}
          value={text}
          onChange={(e) => setText(e.target.value)}
        />

        {errors.length > 0 && (
          <div className="mt-2 max-h-32 overflow-y-auto rounded border border-red-200 bg-red-50 p-2 text-xs text-red-700 dark:border-red-900 dark:bg-red-950 dark:text-red-300">
            {errors.map((e, i) => (
              <div key={i}>{e}</div>
            ))}
          </div>
        )}

        {results && (
          <div className="mt-2 max-h-48 overflow-y-auto rounded border border-slate-200 p-2 text-xs dark:border-slate-800">
            {results.map((r) => (
              <div key={r.line} className={r.ok ? "text-emerald-600 dark:text-emerald-400" : "text-red-600 dark:text-red-400"}>
                第 {r.line} 行 {r.route_user}:{r.message}
              </div>
            ))}
          </div>
        )}

        <div className="mt-4 flex justify-end gap-2">
          <button
            onClick={onClose}
            className="rounded-md border border-slate-300 px-3 py-1.5 text-sm dark:border-slate-700 dark:text-slate-200"
          >
            关闭
          </button>
          <button
            onClick={runImport}
            disabled={running || text.trim() === ""}
            className="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500 disabled:opacity-50"
          >
            {running ? "导入中..." : "导入"}
          </button>
        </div>
      </div>
    </div>
  );
}
