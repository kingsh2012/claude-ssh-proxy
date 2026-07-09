import { useEffect, useState } from "react";
import { api, type AuditLog } from "./api";

export function AuditPage() {
  const [logs, setLogs] = useState<AuditLog[]>([]);
  const [proxyUser, setProxyUser] = useState("");
  const [expanded, setExpanded] = useState<number | null>(null);
  const [refreshing, setRefreshing] = useState(false);

  async function load() {
    setLogs((await api.listAudit(200, proxyUser)) ?? []);
  }

  async function refresh() {
    setRefreshing(true);
    try {
      await load();
    } finally {
      setRefreshing(false);
    }
  }

  useEffect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [proxyUser]);

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-slate-900 dark:text-slate-100">审计日志</h2>
        <div className="flex gap-2">
          <input
            className="input max-w-xs"
            placeholder="按代理登录名过滤"
            value={proxyUser}
            onChange={(e) => setProxyUser(e.target.value)}
          />
          <button
            onClick={refresh}
            disabled={refreshing}
            className="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium whitespace-nowrap text-slate-700 hover:bg-slate-50 disabled:opacity-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
          >
            {refreshing ? "刷新中..." : "刷新"}
          </button>
        </div>
      </div>

      <div className="space-y-2">
        {logs.map((l) => (
          <div key={l.id} className="rounded-lg border border-slate-200 p-3 text-sm dark:border-slate-800">
            <div
              className="flex cursor-pointer flex-wrap items-center gap-3 text-slate-700 dark:text-slate-300"
              onClick={() => setExpanded(expanded === l.id ? null : l.id)}
            >
              <span className="text-xs text-slate-400">{new Date(l.ts).toLocaleString()}</span>
              <span className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-xs dark:bg-slate-800">{l.proxy_user}</span>
              {l.client_credential_label && (
                <span className="rounded bg-slate-100 px-1.5 py-0.5 text-xs text-slate-500 dark:bg-slate-800">
                  {l.client_credential_label}
                </span>
              )}
              <span className="font-mono text-xs text-slate-500">
                → {l.target_host}:{l.target_port}
              </span>
              <span className="rounded bg-indigo-100 px-1.5 py-0.5 text-xs text-indigo-700 dark:bg-indigo-900/40 dark:text-indigo-300">
                {l.event_type}
              </span>
              <span className="text-xs text-slate-400">来自 {l.remote_addr}</span>
              {l.exit_status !== null && (
                <span className={`text-xs ${l.exit_status === 0 ? "text-emerald-500" : "text-red-500"}`}>
                  exit={l.exit_status}
                </span>
              )}
            </div>
            {expanded === l.id && (
              <pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap rounded bg-slate-50 p-2 font-mono text-xs text-slate-700 dark:bg-slate-900 dark:text-slate-300">
                {l.detail || "(无输出内容)"}
                {l.truncated && "\n... (已截断)"}
              </pre>
            )}
          </div>
        ))}
        {logs.length === 0 && <p className="py-8 text-center text-slate-400">暂无审计记录</p>}
      </div>
    </div>
  );
}
