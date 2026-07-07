import { useEffect, useState } from "react";
import { api, ApiError } from "./api";

export function SettingsPage() {
  const [listenAddr, setListenAddr] = useState("");
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState("");

  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [pwMsg, setPwMsg] = useState("");

  useEffect(() => {
    api.getSettings().then((s) => setListenAddr(s.listen_addr));
  }, []);

  async function saveListenAddr() {
    setError("");
    setSaved(false);
    try {
      await api.updateSettings(listenAddr);
      setSaved(true);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "保存失败");
    }
  }

  async function changePassword() {
    setPwMsg("");
    try {
      await api.changePassword(oldPassword, newPassword);
      setPwMsg("修改成功");
      setOldPassword("");
      setNewPassword("");
    } catch (err) {
      setPwMsg(err instanceof ApiError ? err.message : "修改失败");
    }
  }

  return (
    <div className="max-w-lg space-y-8">
      <section>
        <h2 className="mb-3 text-lg font-semibold text-slate-900 dark:text-slate-100">SSH 监听地址</h2>
        <p className="mb-2 text-sm text-slate-500 dark:text-slate-400">
          修改后 proxy 会立刻重新监听新地址,已有连接不受影响,断线重连的客户端走新地址。
        </p>
        <div className="space-y-2">
          <input className="input" value={listenAddr} onChange={(e) => setListenAddr(e.target.value)} placeholder=":2222" />
          <button onClick={saveListenAddr} className="rounded-md bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-500">
            保存
          </button>
          {saved && <p className="text-sm text-emerald-600 dark:text-emerald-400">已生效</p>}
          {error && <p className="text-sm text-red-600 dark:text-red-400">{error}</p>}
        </div>
      </section>

      <section>
        <h2 className="mb-3 text-lg font-semibold text-slate-900 dark:text-slate-100">修改管理员密码</h2>
        <div className="space-y-2">
          <input
            type="password"
            className="input"
            placeholder="原密码"
            value={oldPassword}
            onChange={(e) => setOldPassword(e.target.value)}
          />
          <input
            type="password"
            className="input"
            placeholder="新密码 (至少 8 位)"
            value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
          />
          <button onClick={changePassword} className="rounded-md bg-indigo-600 px-4 py-2 text-sm font-medium text-white hover:bg-indigo-500">
            修改密码
          </button>
          {pwMsg && <p className="text-sm text-slate-600 dark:text-slate-400">{pwMsg}</p>}
        </div>
      </section>
    </div>
  );
}
