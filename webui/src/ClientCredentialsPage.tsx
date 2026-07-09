import { useEffect, useState } from "react";
import { api, ApiError, type ClientCredential, type ServerRecord } from "./api";
import { ChipList } from "./ChipList";

const emptyCredential: Omit<ClientCredential, "id" | "has_password"> = {
  label: "",
  auth_type: "public_key",
  public_key: "",
  password: "",
  login_names: [],
};

// extractLabelFromPublicKey 取公钥内容里最后一段(comment,比如 "root@vultr")作为默认名称建议。
// authorized_keys 格式是 "类型 base64内容 [comment]",comment 是可选的。
function extractLabelFromPublicKey(publicKey: string): string {
  const parts = publicKey.trim().split(/\s+/);
  return parts.length >= 3 ? parts[parts.length - 1] : "";
}

export function ClientCredentialsPage() {
  const [creds, setCreds] = useState<ClientCredential[]>([]);
  const [servers, setServers] = useState<ServerRecord[]>([]);
  const [editing, setEditing] = useState<
    (Omit<ClientCredential, "id" | "has_password"> & { id?: number; has_password?: boolean }) | null
  >(null);
  const [labelAuto, setLabelAuto] = useState(true);
  const [error, setError] = useState("");

  async function load() {
    const [c, r] = await Promise.all([api.listClientCredentials(), api.listServers()]);
    setCreds(c ?? []);
    setServers(r ?? []);
  }

  useEffect(() => {
    load();
  }, []);

  function startCreate() {
    setEditing({ ...emptyCredential });
    setLabelAuto(true);
    setError("");
  }

  function startEdit(c: ClientCredential) {
    setEditing({ ...c, password: "" });
    setLabelAuto(c.auth_type === "public_key" && c.label === extractLabelFromPublicKey(c.public_key ?? ""));
    setError("");
  }

  function onPublicKeyChange(value: string) {
    if (!editing) return;
    const derived = labelAuto ? extractLabelFromPublicKey(value) : editing.label;
    setEditing({ ...editing, public_key: value, label: derived });
  }

  function onLabelChange(value: string) {
    if (!editing) return;
    setLabelAuto(false);
    setEditing({ ...editing, label: value });
  }

  function toggleServer(loginName: string) {
    if (!editing) return;
    const set = new Set(editing.login_names);
    if (set.has(loginName)) {
      set.delete(loginName);
    } else {
      set.add(loginName);
    }
    setEditing({ ...editing, login_names: Array.from(set) });
  }

  async function save() {
    if (!editing) return;
    setError("");
    try {
      if (editing.id != null) {
        await api.updateClientCredential(editing.id, editing);
      } else {
        await api.createClientCredential(editing);
      }
      setEditing(null);
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "保存失败");
    }
  }

  async function remove(id: number, label: string) {
    if (!confirm(`确定删除客户端凭据 "${label}" 吗?删除后所有关联它的服务器都会失去这份凭据的登录权限。`)) return;
    await api.deleteClientCredential(id);
    await load();
  }

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-slate-900 dark:text-slate-100">客户端凭据</h2>
        <button
          onClick={startCreate}
          className="rounded-md bg-indigo-600 px-3 py-1.5 text-sm font-medium text-white hover:bg-indigo-500"
        >
          + 添加客户端凭据
        </button>
      </div>

      <p className="mb-4 text-sm text-slate-500 dark:text-slate-400">
        每份凭据代表一个客户端身份(比如某个 Claude Agent),认证方式是公钥或密码,可以关联多台服务器——关联了哪些,这份凭据就能登录哪些。
      </p>

      <div className="overflow-x-auto rounded-lg border border-slate-200 dark:border-slate-800">
        <table className="w-full text-left text-sm">
          <thead className="bg-slate-50 text-slate-500 dark:bg-slate-900 dark:text-slate-400">
            <tr>
              <th className="px-4 py-2">ID</th>
              <th className="px-4 py-2">名称</th>
              <th className="px-4 py-2">认证方式</th>
              <th className="px-4 py-2">关联的服务器</th>
              <th className="px-4 py-2"></th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {creds.map((c) => (
              <tr key={c.id} className="text-slate-800 dark:text-slate-200">
                <td className="px-4 py-2 font-mono text-xs text-slate-500">{c.id}</td>
                <td className="px-4 py-2">{c.label}</td>
                <td className="px-4 py-2">
                  {c.auth_type === "public_key" ? (
                    <span
                      className="inline-block max-w-[10rem] truncate align-bottom font-mono text-xs"
                      title={c.public_key}
                    >
                      公钥: {c.public_key}
                    </span>
                  ) : (
                    "密码"
                  )}
                </td>
                <td className="px-4 py-2">
                  <ChipList items={c.login_names} emptyText="未关联任何服务器" />
                </td>
                <td className="px-4 py-2 text-right">
                  <button onClick={() => startEdit(c)} className="mr-3 text-indigo-600 hover:underline dark:text-indigo-400">
                    编辑
                  </button>
                  <button onClick={() => remove(c.id, c.label)} className="text-red-600 hover:underline dark:text-red-400">
                    删除
                  </button>
                </td>
              </tr>
            ))}
            {creds.length === 0 && (
              <tr>
                <td colSpan={5} className="px-4 py-6 text-center text-slate-400">
                  还没有添加任何客户端凭据
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
              {editing.id != null ? `编辑 ${editing.label}` : "添加客户端凭据"}
            </h3>

            <div className="mb-3">
              <label className="mb-1 block text-xs text-slate-500 dark:text-slate-400">认证方式</label>
              <select
                className="input"
                value={editing.auth_type}
                onChange={(e) => setEditing({ ...editing, auth_type: e.target.value as "public_key" | "password" })}
              >
                <option value="public_key">公钥</option>
                <option value="password">密码</option>
              </select>
            </div>

            {editing.auth_type === "public_key" ? (
              <>
                <div className="mb-3">
                  <label className="mb-1 block text-xs text-slate-500 dark:text-slate-400">公钥内容</label>
                  <textarea
                    className="input h-20 font-mono"
                    value={editing.public_key}
                    onChange={(e) => onPublicKeyChange(e.target.value)}
                    placeholder="ssh-ed25519 AAAA... claude-client"
                    autoFocus
                  />
                </div>
                <div className="mb-3">
                  <label className="mb-1 block text-xs text-slate-500 dark:text-slate-400">
                    名称(默认从公钥末尾的 comment 自动截取,可以手动改)
                  </label>
                  <input className="input" value={editing.label} onChange={(e) => onLabelChange(e.target.value)} />
                </div>
              </>
            ) : (
              <>
                <div className="mb-3">
                  <label className="mb-1 block text-xs text-slate-500 dark:text-slate-400">
                    {editing.has_password ? "密码(已设置,留空则不修改)" : "密码"}
                  </label>
                  <input
                    type="password"
                    className="input"
                    value={editing.password}
                    onChange={(e) => setEditing({ ...editing, password: e.target.value })}
                    autoFocus
                  />
                </div>
                <div className="mb-3">
                  <label className="mb-1 block text-xs text-slate-500 dark:text-slate-400">名称</label>
                  <input
                    className="input"
                    value={editing.label}
                    onChange={(e) => setEditing({ ...editing, label: e.target.value })}
                  />
                </div>
              </>
            )}

            <div className="mb-3">
              <label className="mb-1 block text-xs text-slate-500 dark:text-slate-400">这份凭据能登录哪些服务器</label>
              <div className="max-h-48 space-y-1 overflow-y-auto rounded-md border border-slate-300 p-2 dark:border-slate-700">
                {servers.length === 0 && (
                  <p className="text-sm text-slate-400">还没有配置任何服务器,先去"服务器"页面添加</p>
                )}
                {servers.map((r) => (
                  <label key={r.login_name} className="flex items-center gap-2 text-sm text-slate-700 dark:text-slate-300">
                    <input
                      type="checkbox"
                      checked={editing.login_names.includes(r.login_name)}
                      onChange={() => toggleServer(r.login_name)}
                    />
                    <span className="font-mono">{r.login_name}</span>
                    <span className="text-xs text-slate-400">
                      ({r.target_host}:{r.target_port})
                    </span>
                  </label>
                ))}
              </div>
            </div>

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
