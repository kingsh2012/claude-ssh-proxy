import type { ProColumns } from "@ant-design/pro-components";
import { Empty } from "antd";
import { ReloadOutlined as RefreshIcon } from "@ant-design/icons";
import { ToolbarIconAction } from "./ListControls";
import { useListView } from "./useListView";
import { PageContainer, ProTable } from "@ant-design/pro-components";
import { App, Badge } from "antd";
import { useCallback, useEffect, useState } from "react";
import { api, type ActiveConnection } from "./api";

export function ConnectionsPage() {
  const [loading, setLoading] = useState(false);
  const { message } = App.useApp();
  const [connections, setConnections] = useState<ActiveConnection[]>([]);
  const [error, setError] = useState("");
  const [now, setNow] = useState(Date.now());

  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true);
    try {
      const next = (await api.listConnections()) ?? [];
      setConnections((previous) => JSON.stringify(previous) === JSON.stringify(next) ? previous : next);
      setError("");
      setNow(Date.now());
    } catch {
      setError("读取连接失败");
      if (!silent) void message.error({ content: "读取连接失败", key: "connections-load" });
    } finally {
      if (!silent) setLoading(false);
    }
  }, [message]);

  useEffect(() => {
    load();
    const timer = setInterval(() => void load(true), 3000);
    return () => clearInterval(timer);
  }, [load]);

  const columns: ProColumns<ActiveConnection>[] = [
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
      key: "target",
      width: 240,
      render: (_, c) => `${c.target_user}@${c.target_host}:${c.target_port}`,
    },
    {
      title: "连接时间",
      key: "connected_at",
      width: 200,
      render: (_, c) => new Date(c.connected_at).toLocaleString(),
    },
    {
      title: "持续时间",
      key: "duration",
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
  ];
  const view = useListView(connections, columns, "ConnectionsPage", {});

  return (
    <PageContainer
      title={false}
      ghost
      style={{ padding: 0 }}
      breadcrumb={{ items: [{ title: "运维管理" }, { title: "当前连接" }] }}
    >
      <ProTable<ActiveConnection>
        rowKey="id"

        {...view.tableProps}
        loading={loading}
        headerTitle={`当前 ${connections.length} 条连接 · ${error ? "更新失败，正在重试" : "每 3 秒自动更新"}`}
        toolBarRender={() => [
          <ToolbarIconAction
            key="refresh"
            label="刷新"
            icon={<RefreshIcon />}
            disabled={loading}
            onClick={() => void load()}
          />,
        ]}
        locale={{
          emptyText: (
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={
                error
                  ? "加载失败，请刷新重试"
                  : view.hasFilters
                    ? "未找到匹配记录，请修改筛选条件"
                    : "暂无记录"
              }
            />
          ),
        }}
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
