import { Button, Input, Switch } from "antd";
import { useEffect, useState } from "react";
import { api, ApiError, type ListenerSettings as ListenerValues } from "./api";

export function ListenerSettings() {
  const [value, setValue] = useState<ListenerValues | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  useEffect(() => {
    api.getListeners().then(setValue).catch(() => setError("读取Web和Agent监听设置失败，请刷新重试"));
  }, []);
  function change(patch: Partial<ListenerValues>) {
    setValue((current) => current ? { ...current, ...patch } : current);
    setMessage("");
  }
  async function save() {
    if (!value) return;
    setBusy(true);
    setError("");
    setMessage("");
    try {
      await api.updateListeners(value);
      setMessage("已保存，重启服务后生效。请使用新的Web地址重新打开后台。");
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "保存监听设置失败");
    } finally {
      setBusy(false);
    }
  }
  return (
    <section>
      <h3 className="mb-3 text-base font-semibold text-slate-900">Web与Agent监听</h3>
      <p className="mb-2 text-sm text-slate-500">Web仅提供HTTP后台，Agent仅提供/agent接入。公网只映射Agent端口。</p>
      <div className="settings-fields">
        <label htmlFor="web-listen-address">Web监听地址</label>
        <Input id="web-listen-address" value={value?.web_listen_addr ?? ""} disabled={!value || busy}
          placeholder="127.0.0.1:8080" onChange={(e) => change({ web_listen_addr: e.target.value })} />
        <label htmlFor="agent-listen-address">Agent监听地址</label>
        <Input id="agent-listen-address" value={value?.agent_listen_addr ?? ""} disabled={!value || busy}
          placeholder=":8443" onChange={(e) => change({ agent_listen_addr: e.target.value })} />
        <label htmlFor="agent-tls-enabled">Agent内置TLS</label>
        <Switch id="agent-tls-enabled" checked={value?.agent_tls_enabled ?? false} disabled={!value || busy}
          onChange={(checked) => change({ agent_tls_enabled: checked })} />
        {value?.agent_tls_enabled && <>
          <label htmlFor="agent-cert-file">证书文件路径</label>
          <Input id="agent-cert-file" value={value.agent_tls_cert_file} disabled={busy}
            placeholder="/data/aiagent-ssh-proxy/tls/server.crt" onChange={(e) => change({ agent_tls_cert_file: e.target.value })} />
          <label htmlFor="agent-key-file">私钥文件路径</label>
          <Input id="agent-key-file" value={value.agent_tls_key_file} disabled={busy}
            placeholder="/data/aiagent-ssh-proxy/tls/server.key" onChange={(e) => change({ agent_tls_key_file: e.target.value })} />
          <p className="text-xs text-slate-500">填写服务器上的PEM文件路径。使用公网IP连接时，证书必须包含该公网IP，客户端需信任签发CA。</p>
        </>}
        {!value?.agent_tls_enabled && <p className="text-xs text-slate-500">关闭时Agent提供明文WebSocket，需由反向代理提供TLS后再接入公网。</p>}
        <Button type="primary" onClick={save} loading={busy} disabled={!value || busy}>保存监听设置</Button>
        {message && <p className="text-sm text-emerald-600">{message}</p>}
        {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
      </div>
    </section>
  );
}
