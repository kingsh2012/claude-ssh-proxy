import { PageContainer, ProTable } from "@ant-design/pro-components";
import { Input, Button, Tag, Typography, App } from "antd";
import { useEffect, useState } from "react";
import { api, type AuditLog } from "./api";

export function AuditPage() {
  const [logs, setLogs] = useState<AuditLog[]>([]);
  const [proxyUser, setProxyUser] = useState("");
  const [targetHost, setTargetHost] = useState("");
  const [clientCredentialLabel, setClientCredentialLabel] = useState("");
  const { message } = App.useApp();
  const [refreshing, setRefreshing] = useState(false);

  async function load() {
    try {
      setLogs(
        (await api.listAudit(200, {
          proxyUser,
          targetHost,
          clientCredentialLabel,
        })) ?? [],
      );
    } catch {
      void message.error({ content: "读取审计日志失败", key: "audit-load" });
    }
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
  }, [proxyUser, targetHost, clientCredentialLabel]);

  return (
    <PageContainer
      title="审计日志"
      extra={
        <>
          <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
            <Input
              placeholder="按代理登录名过滤"
              value={proxyUser}
              onChange={(e) => setProxyUser(e.target.value)}
            />
            <Input
              placeholder="按目标服务器过滤"
              value={targetHost}
              onChange={(e) => setTargetHost(e.target.value)}
            />
            <Input
              placeholder="按客户端凭据过滤"
              value={clientCredentialLabel}
              onChange={(e) => setClientCredentialLabel(e.target.value)}
            />
            <Button onClick={refresh} disabled={refreshing}>
              {refreshing ? "刷新中..." : "刷新"}
            </Button>
          </div>
        </>
      }
    >
      <Typography.Paragraph type="secondary">
        显示最近 200 条匹配记录，每 5 秒自动刷新。展开记录查看命令和输出。
      </Typography.Paragraph>
      <ProTable<AuditLog>
        search={false}
        options={false}
        cardProps={{ variant: "borderless" }}
        rowKey="id"
        size="middle"
        dataSource={logs}
        scroll={{ x: 1200 }}
        pagination={{
          defaultPageSize: 20,
          showSizeChanger: true,
          showTotal: (total) => `共 ${total} 条`,
        }}
        columns={[
          {
            title: "时间",
            dataIndex: "ts",
            width: 195,
            render: (_, record) => new Date(record.ts).toLocaleString(),
          },
          { title: "代理登录名", dataIndex: "proxy_user", width: 180 },
          {
            title: "客户端凭据",
            dataIndex: "client_credential_label",
            width: 170,
            render: (v) => v || "—",
          },
          {
            title: "目标",
            width: 210,
            render: (_, l) => `${l.target_host}:${l.target_port}`,
          },
          { title: "来源", dataIndex: "remote_addr", width: 180 },
          {
            title: "类型",
            dataIndex: "event_type",
            width: 100,
            render: (v) => <Tag color="blue">{v}</Tag>,
          },
          {
            title: "结果",
            width: 165,
            render: (_, l) => (
              <Tag
                color={
                  l.status === "running"
                    ? "processing"
                    : l.exit_status === 0
                      ? "success"
                      : "error"
                }
              >
                {l.status === "running"
                  ? "运行中"
                  : l.exit_status === null
                    ? "未收到退出码"
                    : `退出码 ${l.exit_status}`}
              </Tag>
            ),
          },
        ]}
        expandable={{
          expandedRowRender: (l) => (
            <div className="audit-detail">
              {l.event_type === "exec" ? (
                <>
                  <Typography.Text strong>命令</Typography.Text>
                  <pre>{l.command || "（空）"}</pre>
                  <Typography.Text strong>输出</Typography.Text>
                  <pre>
                    {l.output || "（无输出内容）"}
                    {l.truncated && "\n…（已截断）"}
                  </pre>
                </>
              ) : (
                <pre>
                  {l.detail || "（无输出内容）"}
                  {l.truncated && "\n…（已截断）"}
                </pre>
              )}
            </div>
          ),
        }}
      />
    </PageContainer>
  );
}
