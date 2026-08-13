import { useEffect, useState } from "react";
import { api, type ActiveConnection } from "./api";

export function ConnectionsPage() {
  const [connections, setConnections] = useState<ActiveConnection[]>([]);
  const [now, setNow] = useState(Date.now());

  async function load() {
    setConnections((await api.listConnections()) ?? []);
    setNow(Date.now());
  }

  useEffect(() => {
    load();
    const timer = setInterval(load, 3000);
    return () => clearInterval(timer);
  }, []);

  return (
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-slate-900 dark:text-slate-100">当前连接</h2>
        <button
          onClick={load}
          className="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50 dark:border-slate-700 dark:text-slate-200 dark:hover:bg-slate-800"
        >
          刷新
        </button>
      </div>

      <div className="overflow-x-auto rounded-lg border border-slate-200 dark:border-slate-800">
        <table className="w-full text-left text-sm">
          <thead className="bg-slate-50 text-slate-500 dark:bg-slate-900 dark:text-slate-400">
            <tr>
              <th className="px-4 py-2">代理名称</th>
              <th className="px-4 py-2">客户端凭据</th>
              <th className="px-4 py-2">来源</th>
              <th className="px-4 py-2">目标服务器</th>
              <th className="px-4 py-2">连接时间</th>
              <th className="px-4 py-2">持续时间</th>
              <th className="px-4 py-2">会话数</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {connections.map((connection) => (
              <tr key={connection.id} className="text-slate-800 dark:text-slate-200">
                <td className="px-4 py-2 font-mono">{connection.proxy_user}</td>
                <td className="px-4 py-2">{connection.client_credential_label || "-"}</td>
                <td className="px-4 py-2 font-mono text-xs">{connection.remote_addr}</td>
                <td className="px-4 py-2 font-mono text-xs">
                  {connection.target_user}@{connection.target_host}:{connection.target_port}
                </td>
                <td className="px-4 py-2 whitespace-nowrap">{new Date(connection.connected_at).toLocaleString()}</td>
                <td className="px-4 py-2 whitespace-nowrap">{formatDuration(now - new Date(connection.connected_at).getTime())}</td>
                <td className="px-4 py-2 text-center">{connection.active_sessions}</td>
              </tr>
            ))}
            {connections.length === 0 && (
              <tr>
                <td colSpan={7} className="px-4 py-8 text-center text-slate-400">当前没有 SSH 连接</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function formatDuration(milliseconds: number) {
  const seconds = Math.max(0, Math.floor(milliseconds / 1000));
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const rest = seconds % 60;
  return [hours, minutes, rest].map((value) => String(value).padStart(2, "0")).join(":");
}
