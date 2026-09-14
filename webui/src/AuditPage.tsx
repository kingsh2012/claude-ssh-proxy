import { useSearchParams } from "@umijs/max";
import type { ProColumns } from "@ant-design/pro-components";
import { Empty } from "antd";
import {
  ReloadOutlined as RefreshIcon,
  ClearOutlined,
} from "@ant-design/icons";
import { ToolbarIconAction, TextColumnFilter } from "./ListControls";
import { useListView } from "./useListView";
import { PageContainer, ProTable } from "@ant-design/pro-components";
import { Tag, Button, Modal, App } from "antd";
import { useEffect, useState } from "react";
import { api, type AuditLog } from "./api";

export function AuditPage() {
  const [logs, setLogs] = useState<AuditLog[]>([]);
  const [detail, setDetail] = useState<AuditLog | null>(null);
  const [params] = useSearchParams();
  const proxyUser = params.get("proxy_user") || "";
  const targetHost = params.get("target_host") || "";
  const clientCredentialLabel = params.get("client_credential_label") || "";
  const { message } = App.useApp();
  const [loading, setRefreshing] = useState(false);

  const [error, setError] = useState("");
  async function load(silent = false) {
    if (!silent) setRefreshing(true);
    try {
      const next = (await api.listAudit(200, {
          proxyUser,
          targetHost,
          clientCredentialLabel,
        })) ?? [];
      setLogs((previous) => JSON.stringify(previous) === JSON.stringify(next) ? previous : next);
      setDetail((previous) => previous ? next.find((log) => log.id === previous.id) ?? previous : null);
      setError("");
    } catch {
      setError("读取审计日志失败");
      if (!silent) void message.error({ content: "读取审计日志失败", key: "audit-load" });
    } finally {
      if (!silent) setRefreshing(false);
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
    const t = setInterval(() => void load(true), 5000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [proxyUser, targetHost, clientCredentialLabel]);

  const columns: ProColumns<AuditLog>[] = [
    {
      title: "时间",
      dataIndex: "ts",
      width: 195,
      render: (_, record) => new Date(record.ts).toLocaleString(),
    },
    {
      title: "代理登录名",
      dataIndex: "proxy_user",
      filterDropdown: TextColumnFilter,
      width: 180,
    },
    {
      title: "客户端凭证",
      dataIndex: "client_credential_label",
      filterDropdown: TextColumnFilter,
      width: 170,
      render: (v) => v || "—",
    },
    {
      title: "目标",
      key: "target_host",
      filterDropdown: TextColumnFilter,
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
      key: "result",
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
    {
      title: "操作",
      key: "option",
      width: 90,
      render: (_, record) => <Button type="link" onClick={() => setDetail(record)}>详情</Button>,
    },
  ];
  const view = useListView(logs, columns, "AuditPage", { match: () => true });

  return (
    <PageContainer
      title={false}
      ghost
      style={{ padding: 0 }}
      breadcrumb={{ items: [{ title: "运维管理" }, { title: "审计日志" }] }}
    >
      <ProTable<AuditLog>
        rowKey="id"

        {...view.tableProps}
        loading={loading}
        headerTitle={`最近 200 条匹配记录 · ${error ? "更新失败，正在重试" : "每 5 秒自动更新"}`}
        toolBarRender={() => [
          <ToolbarIconAction
            key="refresh"
            label="刷新"
            icon={<RefreshIcon />}
            disabled={loading}
            onClick={refresh}
          />,
          <ToolbarIconAction
            key="clear"
            label="清除筛选"
            icon={<ClearOutlined />}
            disabled={!view.hasFilters}
            onClick={view.reset}
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
      <Modal
        title={detail ? `审计详情 · ${detail.proxy_user}` : "审计详情"}
        open={detail !== null}
        onCancel={() => setDetail(null)}
        footer={null}
        centered
        width="min(1280px, calc(100vw - 32px))"
        destroyOnHidden
      >
        {detail && (
          <div className="audit-detail">
            <div className="audit-detail-meta">
              <span>{new Date(detail.ts).toLocaleString()}</span>
              <span>{detail.target_host}:{detail.target_port}</span>
              <Tag>{detail.event_type}</Tag>
              <Tag color={detail.status === "running" ? "processing" : detail.exit_status === 0 ? "success" : "error"}>
                {detail.status === "running" ? "运行中" : detail.exit_status === null ? "未收到退出码" : `退出码 ${detail.exit_status}`}
              </Tag>
            </div>
            <section className="audit-terminal audit-terminal-input">
              <h3>输入命令</h3>
              <pre tabIndex={0} aria-label="输入命令"><code>{detail.command || (detail.event_type === "exec" ? "（空命令）" : "（交互会话，记录见下方）")}</code></pre>
            </section>
            <section className="audit-terminal audit-terminal-output">
              <h3>{detail.event_type === "exec" ? "输出" : "会话记录"}</h3>
              <pre tabIndex={0} aria-label="输出"><code>{(detail.event_type === "exec" ? detail.output : detail.detail) || "（无输出内容）"}{detail.truncated && "\n…（已截断）"}</code></pre>
            </section>
          </div>
        )}
      </Modal>
    </PageContainer>
  );
}
