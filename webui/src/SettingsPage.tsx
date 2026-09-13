import { Card, Input, Button } from "antd";
import { useEffect, useState } from "react";
import { api, ApiError } from "./api";
import { SelfRegistrationSettings } from "./SelfRegistrationSettings";

export function SettingsPage() {
  const [listenAddr, setListenAddr] = useState("");
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState("");

  const [oldPassword, setOldPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [pwMsg, setPwMsg] = useState("");

  useEffect(() => {
    api
      .getSettings()
      .then((s) => setListenAddr(s.listen_addr))
      .catch(() => setError("读取监听设置失败，请刷新重试"));
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
    <div>
      <div className="mb-4 flex items-center justify-between">
        <h2 className="text-lg font-semibold text-slate-900 ">服务设置</h2>
      </div>

      <div className="settings-grid">
        <Card>
          <h3 className="mb-3 text-base font-semibold text-slate-900 ">
            SSH 监听地址
          </h3>
          <p className="mb-2 text-sm text-slate-500 ">
            修改后 proxy
            会立刻重新监听新地址,已有连接不受影响,断线重连的客户端走新地址。
          </p>
          <div className="settings-fields">
            <Input
              value={listenAddr}
              onChange={(e) => setListenAddr(e.target.value)}
              placeholder=":2222"
            />
            <Button type="primary" onClick={saveListenAddr}>
              保存
            </Button>
            {saved && <p className="text-sm text-emerald-600 ">已生效</p>}
            {error && <p className="text-sm text-red-600 ">{error}</p>}
          </div>
        </Card>

        <SelfRegistrationSettings />

        <Card>
          <h3 className="mb-3 text-base font-semibold text-slate-900 ">
            修改管理员密码
          </h3>
          <div className="settings-fields">
            <Input.Password
              placeholder="原密码"
              value={oldPassword}
              onChange={(e) => setOldPassword(e.target.value)}
            />
            <Input.Password
              placeholder="新密码(至少 8 位)"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
            />
            <Button type="primary" onClick={changePassword}>
              修改密码
            </Button>
            {pwMsg && <p className="text-sm text-slate-600 ">{pwMsg}</p>}
          </div>
        </Card>
      </div>
    </div>
  );
}
