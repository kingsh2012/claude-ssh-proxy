import { PageContainer, ProTable } from "@ant-design/pro-components";
import { App, Badge, Button } from "antd";
import { useCallback, useEffect, useState } from "react";
import { api, type ActiveConnection } from "./api";

export function ConnectionsPage() {
  const { message } = App.useApp();
  const [connections, setConnections] = useState<ActiveConnection[]>([]);
  const [now, setNow] = useState(Date.now());

  const load = useCallback(async () => {
    try {
      setConnections((await api.listConnections()) ?? []);
      setNow(Date.now());
    } catch {
      void message.error({ content: "读取连接失败", key: "connections-load" });
    }
  }, [message]);

  useEffect(() => {
    load();
    const timer = setInterval(load, 3000);
    return () => clearInterval(timer);
  }, [load]);

  return (
    <PageContainer
      title="当前连接"
      extra={
        <>
          <Button onClick={load}>刷新</Button>
        </>
      }
    >
      <p className="mb-4 text-slate-500">
        每 3 秒自动刷新，当前 {connections.length} 条 SSH 连接。
      </p>
      <ProTable<ActiveConnection>
        search={false}
        options={false}
        cardProps={{ variant: "borderless" }}
        rowKey="id"
        size="middle"
        dataSource={connections}
        scroll={{ x: 1150 }}
        pagination={{ defaultPageSize: 20, showSizeChanger: true }}
        columns={[
          { title: "代理登录名", dataIndex: "proxy_user", width: 180 },
          {
            title: "客户端凭据",
            dataIndex: "client_credential_label",
            width: 180,
            render: (v) => v || "—",
          },
          { title: "来源", dataIndex: "remote_addr", width: 190 },
          {
            title: "目标服务器",
            width: 240,
            render: (_, c) =>
              `${c.target_user}@${c.target_host}:${c.target_port}`,
          },
          {
            title: "连接时间",
            width: 200,
            render: (_, c) => new Date(c.connected_at).toLocaleString(),
          },
          {
            title: "持续时间",
            width: 120,
            render: (_, c) =>
              formatDuration(now - new Date(c.connected_at).getTime()),
          },
          {
            title: "会话数",
            width: 90,
            render: (_, c) => (
              <Badge count={c.active_sessions} showZero color="#1677ff" />
            ),
          },
        ]}
      />
    </PageContainer>
  );
}

function formatDuration(milliseconds: number) {
  const seconds = Math.max(0, Math.floor(milliseconds / 1000));
  const hours = Math.floor(seconds / 3600);
  const minutes = Math.floor((seconds % 3600) / 60);
  const rest = seconds % 60;
  return [hours, minutes, rest]
    .map((value) => String(value).padStart(2, "0"))
    .join(":");
}
