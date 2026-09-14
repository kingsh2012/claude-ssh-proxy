import { Input, Button } from "antd";
import { useFeedback } from "./useFeedback";
import { useEffect, useState } from "react";
import {
  api,
  ApiError,
  type SelfRegistration,
} from "./api";

function displayAddress(value: SelfRegistration): SelfRegistration {
  if (!value.server_url) return value;
  const url = new URL(value.server_url);
  return { ...value, server_url: `https://${url.host}` };
}

export function SelfRegistrationSettings() {
  const { confirm } = useFeedback();
  const [settings, setSettings] = useState<SelfRegistration | null>(null);
  const [address, setAddress] = useState(
    window.location.protocol === "https:"
      ? window.location.origin
      : "",
  );
  const [token, setToken] = useState("");
  const [tokenAddress, setTokenAddress] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  useEffect(() => {
    api.getSelfRegistration()
      .then(displayAddress)
      .then((value) => {
        setSettings(value);
        setToken(value.token);
        setTokenAddress(value.server_url);
        if (value.server_url) setAddress(value.server_url);
      })
      .catch(() => setError("读取自注册设置失败，请刷新重试"));
  }, []);

  async function generate() {
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const draft = await api.generateSelfRegistration(address);
      setToken(draft.token);
      setTokenAddress(address);
      setMessage("密钥已生成，点击保存后生效");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "生成密钥失败");
    } finally {
      setBusy(false);
    }
  }

  async function save() {
    if (
      settings?.enabled && token !== settings.token &&
      !(await confirm(
        "保存新密钥后，旧密钥失效，使用它的 Agent 会断开。需要用新密钥重新启动，继续？",
      ))
    )
      return;
    setBusy(true);
    setError("");
    setMessage("");
    try {
      setSettings(
        displayAddress(await api.putSelfRegistration({
          token,
          server_url: address,
        })),
      );
      setMessage("自注册 Token 已生效");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "保存失败");
    } finally {
      setBusy(false);
    }
  }
  async function saveAddress() {
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const value = displayAddress(await api.saveRegistrationAddress(address));
      setSettings(value);
      setAddress(value.server_url);
      setToken(value.token);
      setTokenAddress(value.server_url);
      setMessage("公网连接地址已保存");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "保存地址失败");
    } finally {
      setBusy(false);
    }
  }
  const dirty = token !== settings?.token || !settings?.enabled;
  return (
    <section className="registration-card">
      <div className="space-y-3">
        <div className="flex flex-wrap items-center gap-3">
          <h3 className="text-base font-semibold">自注册 Token</h3>
          <span className="text-xs text-slate-500">
            {settings ? (settings.enabled ? "已启用" : "未启用") : "加载中…"}
          </span>
        </div>
        <p className="text-sm text-slate-500">
          多台 Windows 共用此密钥，启动 Agent
          后自动加入服务器列表。注册后需在服务器列表中手动授权，才能通过 SSH 代理访问。
        </p>
        <label htmlFor="registration-address" className="block text-sm">公网连接地址</label>
        <div className="registration-key-row">
          <Input
            id="registration-address"
            value={address}
            disabled={busy}
            onChange={(e) => setAddress(e.target.value)}
            placeholder="https://proxy.example.com"
          />
          <Button onClick={saveAddress} disabled={!settings || busy || !address || address === settings.server_url}>
            保存地址
          </Button>
        </div>
        <p className="text-xs text-slate-500">
          只填写 HTTPS 主域名，例如 https://proxy.example.com，不需要附加路径。
          Agent 通信使用 /agent 路由，自动通过 WSS 连接；地址已包含在 Token 中。
        </p>
        <div className="registration-key-row">
          <Input
            id="registration-token"
            aria-label="自注册 Token"
            className="font-mono text-xs"
            autoComplete="off"
            readOnly
            value={token}
            placeholder="点击随机生成密钥"
            onFocus={(e) => e.currentTarget.select()}
          />
          <Button onClick={generate} disabled={!settings || busy || !address || address !== settings.server_url}>
            随机生成密钥
          </Button>
          <Button type="primary" onClick={save} loading={busy} disabled={!settings || busy || !token || tokenAddress !== address || !dirty}>
            保存
          </Button>
        </div>
        {token && tokenAddress !== address && (
          <p className="text-sm text-amber-700">连接地址已修改，请先保存地址，再生成密钥。</p>
        )}
        <p className="text-sm text-slate-500">
          默认使用 Windows 主机名作为代理登录名，也可指定：
        </p>
        <pre className="agent-command">{`.\\aiagent-ssh-client.exe -token '${token || "自注册Token"}' -hostname 'es-windows-01'`}</pre>
        {message && <p className="text-sm text-emerald-600">{message}</p>}
        {error && (
          <p role="alert" className="text-sm text-red-600">
            {error}
          </p>
        )}
      </div>
    </section>
  );
}
