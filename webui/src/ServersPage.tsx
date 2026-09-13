import {
  Grid,
  Badge,
  Button,
  Dropdown,
  Input,
  Modal,
  Table,
  Tag,
  Typography,
} from "antd";
import { useFeedback } from "./useFeedback";
import { useEffect, useRef, useState } from "react";
import {
  api,
  ApiError,
  type ClientCredential,
  type ServerRecord,
  type ServerCredential,
} from "./api";
import { ChipList } from "./ChipList";
import {
  MultiSelectDropdown,
  SelectDropdown,
  SingleSelectDropdown,
} from "./MultiSelectDropdown";
import { Tooltip } from "./Tooltip";

const emptyServer: ServerRecord = {
  connection_type: "ssh",
  id: 0,
  proxy_user: "",
  target_host: "",
  target_port: 22,
  route_mode: "fixed",
  port_min: 1,
  port_max: 65535,
  enabled: true,
  legacy_algorithms: false,
  host_key_fingerprint: "",
  client_credential_labels: [],
  last_test_at: null,
  last_test_ok: null,
  server_credential_id: null,
};

// reconcileClientCredentials 把"这个代理登录名应该关联哪些客户端凭据"落地成实际的 API 调用:
// 对比每份客户端凭据当前的 proxy_users 和期望的勾选结果,只在有变化的凭据上调用更新接口。
// save() 和 CSV 导入(逐行调用)都用这个函数,保证行为一致。
//
// 注意:成功调用后会把 c.proxy_users 就地更新——批量导入时如果好几行服务器共用同一份
// 客户端凭据,必须让后面几行看到前面几行刚写入的关联,否则会互相覆盖,只有最后一行生效。
async function reconcileClientCredentials(
  clientCredentials: ClientCredential[],
  proxyUser: string,
  selectedIds: Set<number>,
) {
  for (const c of clientCredentials) {
    const shouldHave = selectedIds.has(c.id);
    const currentlyHas = c.proxy_users.includes(proxyUser);
    if (shouldHave === currentlyHas) continue;
    const { id, has_password, ...rest } = c;
    void id;
    void has_password;
    const proxyUsers = shouldHave
      ? [...c.proxy_users, proxyUser]
      : c.proxy_users.filter((ln) => ln !== proxyUser);
    await api.updateClientCredential(c.id, {
      ...rest,
      proxy_users: proxyUsers,
    });
    c.proxy_users = proxyUsers;
  }
}

export function ServersPage() {
  const screens = Grid.useBreakpoint();
  const { confirm, alert } = useFeedback();
  const [search, setSearch] = useState("");
  const [servers, setServers] = useState<ServerRecord[]>([]);
  const [serverCredentials, setServerCredentials] = useState<
    ServerCredential[]
  >([]);
  const [clientCredentials, setClientCredentials] = useState<
    ClientCredential[]
  >([]);
  const [editing, setEditing] = useState<ServerRecord | null>(null);
  const [selectedCredentialIds, setSelectedCredentialIds] = useState<
    Set<number>
  >(new Set());
  const [error, setError] = useState("");
  const [isNew, setIsNew] = useState(false);
  const [testingServer, setTestingServer] = useState<string | null>(null);
  const [testingAll, setTestingAll] = useState(false);
  const [importing, setImporting] = useState(false);

  async function load() {
    const [s, sc, cc] = await Promise.all([
      api.listServers(),
      api.listServerCredentials(),
      api.listClientCredentials(),
    ]);
    setServers(s ?? []);
    setServerCredentials(sc ?? []);
    setClientCredentials(cc ?? []);
  }

  useEffect(() => {
    load().catch(() => setError("加载失败，请刷新重试"));
  }, []);

  async function testOne(proxyUser: string) {
    setTestingServer(proxyUser);
    try {
      const updated = await api.testServer(proxyUser);
      setServers((prev) =>
        prev.map((s) => (s.proxy_user === proxyUser ? updated : s)),
      );
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "测试失败");
    } finally {
      setTestingServer(null);
    }
  }

  async function testAll() {
    setTestingAll(true);
    try {
      const updated = await api.testAllServers();
      setServers(updated ?? []);
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "测试失败");
    } finally {
      setTestingAll(false);
    }
  }

  async function toggleEnabled(s: ServerRecord) {
    try {
      const updated = await api.setServerEnabled(s.proxy_user, !s.enabled);
      setServers((prev) =>
        prev.map((x) => (x.proxy_user === s.proxy_user ? updated : x)),
      );
    } catch (err) {
      alert(err instanceof ApiError ? err.message : "操作失败");
    }
  }

  function credentialIdsForServer(proxyUser: string): Set<number> {
    return new Set(
      clientCredentials
        .filter((c) => c.proxy_users.includes(proxyUser))
        .map((c) => c.id),
    );
  }

  function startCreate() {
    setEditing({ ...emptyServer });
    setSelectedCredentialIds(new Set());
    setIsNew(true);
    setError("");
  }

  function startEdit(s: ServerRecord) {
    setEditing({ ...s });
    setSelectedCredentialIds(credentialIdsForServer(s.proxy_user));
    setIsNew(false);
    setError("");
  }

  function duplicate(s: ServerRecord) {
    setEditing({ ...s, id: 0 });
    setSelectedCredentialIds(credentialIdsForServer(s.proxy_user));
    setIsNew(true);
    setError("");
  }

  function toggleCredential(id: number) {
    setSelectedCredentialIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }

  async function save() {
    if (!editing) return;
    setError("");
    try {
      await api.upsertServer(editing);
      await reconcileClientCredentials(
        clientCredentials,
        editing.proxy_user,
        selectedCredentialIds,
      );
      setEditing(null);
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "保存失败");
    }
  }

  async function remove(proxyUser: string) {
    if (!(await confirm(`确定删除服务器 ${proxyUser} 吗?`))) return;
    await api.deleteServer(proxyUser);
    await load();
  }

  return (
    <div>
      {error && !editing && (
        <p role="alert" className="mb-3 text-red-600">
          {error}
        </p>
      )}
      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <h2 className="text-lg font-semibold text-slate-900 ">服务器</h2>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            type="link"
            onClick={() => load().catch(() => alert("刷新失败"))}
          >
            刷新
          </Button>
          <Button
            onClick={testAll}
            disabled={testingAll || servers.length === 0}
          >
            {testingAll ? "测试中..." : "测试所有服务器连接"}
          </Button>
          <Button onClick={() => setImporting(true)}>导入</Button>
          <Button type="primary" onClick={startCreate}>
            + 添加服务器
          </Button>
        </div>
      </div>

      <p className="mb-4 text-sm text-slate-500 ">
        每个代理登录名对应一台真实机器,可以在这里绑定服务器凭据和客户端凭据。
      </p>

      <div className="table-toolbar">
        <Input.Search
          aria-label="搜索服务器"
          placeholder="搜索代理登录名、目标地址"
          allowClear
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          style={{ maxWidth: 360 }}
        />
        <Typography.Text type="secondary">
          共 {servers.length} 台主机
        </Typography.Text>
      </div>
      <Table<ServerRecord>
        rowKey="id"
        size="middle"
        scroll={{ x: 1580 }}
        dataSource={servers.filter((s) =>
          `${s.proxy_user} ${s.target_host}`
            .toLowerCase()
            .includes(search.toLowerCase()),
        )}
        pagination={{
          defaultPageSize: 20,
          showSizeChanger: true,
          showTotal: (total) => `共 ${total} 台`,
        }}
        columns={[
          {
            title: "代理登录名",
            dataIndex: "proxy_user",
            width: 190,
            fixed: screens.md ? "left" : undefined,
            sorter: (a, b) => a.proxy_user.localeCompare(b.proxy_user),
            render: (v) => (
              <Typography.Text strong className="font-mono">
                {v}
              </Typography.Text>
            ),
          },
          {
            title: "目标主机",
            width: 230,
            render: (_, s) => (
              <div className="font-mono">
                {s.connection_type === "agent"
                  ? s.target_host
                  : `${s.target_host}:${s.route_mode === "dynamic_port" ? "${PORT}" : s.target_port}`}
              </div>
            ),
          },
          {
            title: "接入方式",
            width: 125,
            filters: [
              { text: "SSH", value: "ssh" },
              { text: "Agent", value: "agent" },
            ],
            onFilter: (v, s) => (s.connection_type || "ssh") === v,
            render: (_, s) => (
              <Tag color={s.connection_type === "agent" ? "blue" : "default"}>
                {s.connection_type === "agent"
                  ? "Agent"
                  : s.route_mode === "dynamic_port"
                    ? "SSH 动态端口"
                    : "SSH"}
              </Tag>
            ),
          },
          {
            title: "状态",
            width: 105,
            render: (_, s) => (
              <Badge
                status={
                  !s.enabled
                    ? "default"
                    : s.connection_type === "agent"
                      ? s.agent_online
                        ? "success"
                        : "warning"
                      : "processing"
                }
                text={
                  !s.enabled
                    ? "已禁用"
                    : s.connection_type === "agent"
                      ? s.agent_online
                        ? "在线"
                        : "离线"
                      : "已启用"
                }
              />
            ),
          },
          {
            title: "连接配置",
            width: 175,
            render: (_, s) =>
              s.connection_type === "agent" ? (
                <Typography.Text type="secondary">Token 认证</Typography.Text>
              ) : (
                <div>
                  <Tag color={s.legacy_algorithms ? "orange" : "default"}>
                    {s.legacy_algorithms ? "兼容旧设备" : "现代算法"}
                  </Tag>
                  <div className="mt-1">
                    <Tag color={s.host_key_fingerprint ? "green" : "default"}>
                      {s.host_key_fingerprint
                        ? "Host Key 已校验"
                        : "Host Key 未校验"}
                    </Tag>
                  </div>
                  {s.route_mode === "dynamic_port" && (
                    <small>
                      端口 {s.port_min}–{s.port_max}
                    </small>
                  )}
                </div>
              ),
          },
          {
            title: "连接测试",
            width: 150,
            render: (_, s) =>
              s.route_mode === "dynamic_port" ? (
                <Typography.Text type="secondary">
                  使用实际登录名测试
                </Typography.Text>
              ) : (
                <TestStatus server={s} />
              ),
          },
          {
            title: "服务器凭据",
            width: 190,
            render: (_, s) =>
              s.connection_type === "agent" ? (
                "—"
              ) : s.server_credential_id != null ? (
                <Tag>
                  {s.server_credential_label} ({s.target_user})
                </Tag>
              ) : (
                <Typography.Text type="secondary">未设置</Typography.Text>
              ),
          },
          {
            title: "客户端凭据",
            width: 230,
            render: (_, s) => (
              <ChipList
                items={s.client_credential_labels ?? []}
                emptyText="无"
                max={2}
              />
            ),
          },
          {
            title: "操作",
            width: 190,
            fixed: screens.md ? "right" : undefined,
            render: (_, s) => (
              <div className="row-actions">
                <Button type="link" size="small" onClick={() => startEdit(s)}>
                  编辑
                </Button>
                <Button
                  type="link"
                  size="small"
                  disabled={s.route_mode === "dynamic_port" || testingAll}
                  loading={testingServer === s.proxy_user}
                  onClick={() => testOne(s.proxy_user)}
                >
                  测试
                </Button>
                <Dropdown
                  menu={{
                    items: [
                      { key: "toggle", label: s.enabled ? "禁用" : "启用" },
                      ...(s.connection_type === "agent"
                        ? []
                        : [{ key: "copy", label: "复制" }]),
                      { key: "delete", label: "删除", danger: true },
                    ],
                    onClick: ({ key }) => {
                      if (key === "toggle") void toggleEnabled(s);
                      if (key === "copy") duplicate(s);
                      if (key === "delete")
                        void remove(s.proxy_user).catch(() =>
                          alert("删除失败"),
                        );
                    },
                  }}
                >
                  <Button type="text" size="small">
                    更多
                  </Button>
                </Dropdown>
              </div>
            ),
          },
        ]}
      />

      {editing && (
        <Modal
          open
          title={isNew ? "添加服务器" : `编辑 ${editing.proxy_user}`}
          onCancel={() => setEditing(null)}
          footer={null}
          width={600}
          destroyOnHidden
          styles={{
            body: { maxHeight: "70vh", overflowY: "auto", paddingTop: 16 },
          }}
        >
          <Field label="接入方式">
            <p className="text-sm text-slate-600 ">
              {editing.connection_type === "agent"
                ? "Agent 自动注册"
                : "SSH 直连"}
            </p>
          </Field>
          {editing.connection_type !== "agent" && (
            <Field label="路由模式">
              <SelectDropdown
                options={[
                  { value: "fixed", label: "固定端口" },
                  { value: "dynamic_port", label: "动态端口" },
                ]}
                value={editing.route_mode ?? "fixed"}
                onChange={(value) =>
                  setEditing({
                    ...editing,
                    route_mode: value,
                    proxy_user:
                      value === "dynamic_port" && !editing.proxy_user
                        ? "server-${PORT}"
                        : editing.proxy_user,
                  })
                }
              />
            </Field>
          )}

          <Field
            label={
              editing.route_mode === "dynamic_port"
                ? "代理登录名模板(唯一)"
                : "代理登录名(唯一)"
            }
          >
            <Input
              placeholder={
                editing.route_mode === "dynamic_port"
                  ? "server-${PORT}"
                  : "例如 server-01"
              }
              value={editing.proxy_user}
              readOnly={editing.connection_type === "agent"}
              onChange={(e) =>
                setEditing({ ...editing, proxy_user: e.target.value })
              }
            />
            {editing.route_mode === "dynamic_port" && (
              <p className="mt-1.5 text-xs text-slate-500 ">
                ${"{PORT}"} 必须放在末尾。登录名中的端口会成为目标 SSH 端口。
              </p>
            )}
          </Field>

          {editing.connection_type !== "agent" && (
            <>
              <Field label="目标SSH服务器IP/域名">
                <Input
                  value={editing.target_host}
                  onChange={(e) =>
                    setEditing({ ...editing, target_host: e.target.value })
                  }
                />
              </Field>

              {editing.route_mode === "dynamic_port" ? (
                <>
                  <Field label="允许的目标端口范围">
                    <div className="grid grid-cols-[1fr_auto_1fr] items-center gap-2">
                      <Input
                        type="number"
                        min={1}
                        max={65535}

                        value={editing.port_min}
                        onChange={(e) =>
                          setEditing({
                            ...editing,
                            port_min: Number(e.target.value),
                          })
                        }
                      />
                      <span className="text-slate-400">至</span>
                      <Input
                        type="number"
                        min={1}
                        max={65535}

                        value={editing.port_max}
                        onChange={(e) =>
                          setEditing({
                            ...editing,
                            port_max: Number(e.target.value),
                          })
                        }
                      />
                    </div>
                  </Field>
                  {editing.proxy_user.endsWith("${PORT}") &&
                    editing.target_host && (
                      <div className="mb-3 rounded-md bg-slate-50 p-2.5 text-xs text-slate-600  ">
                        示例:{" "}
                        <code className="font-mono text-slate-800 ">
                          {editing.proxy_user.replace("${PORT}", "8888")}
                        </code>
                        {" -> "}
                        <code className="font-mono text-slate-800 ">
                          {editing.target_host}:8888
                        </code>
                      </div>
                    )}
                </>
              ) : (
                <Field label="目标SSH服务器端口">
                  <Input
                    type="number"

                    value={editing.target_port}
                    onChange={(e) =>
                      setEditing({
                        ...editing,
                        target_port: Number(e.target.value),
                      })
                    }
                  />
                </Field>
              )}

              <details className="mb-3 rounded-md border border-slate-200 ">
                <summary className="cursor-pointer select-none px-3 py-2 text-sm font-medium text-slate-700 ">
                  高级配置
                  {(editing.legacy_algorithms ||
                    editing.host_key_fingerprint) && (
                    <span className="ml-2 text-xs font-normal text-indigo-600 ">
                      已配置
                    </span>
                  )}
                </summary>
                <div className="border-t border-slate-200 px-3 pt-3 ">
                  <Field label="连接算法">
                    <SelectDropdown
                      options={[
                        { value: "modern", label: "现代算法(默认,推荐)" },
                        { value: "legacy", label: "兼容旧设备" },
                      ]}
                      value={editing.legacy_algorithms ? "legacy" : "modern"}
                      onChange={(value) =>
                        setEditing({
                          ...editing,
                          legacy_algorithms: value === "legacy",
                        })
                      }
                    />
                    {editing.legacy_algorithms && (
                      <p className="mt-1.5 text-xs text-amber-600 ">
                        将附加弱加密算法,仅用于无法支持现代算法的老旧交换机等设备。
                      </p>
                    )}
                  </Field>

                  <Field label="目标机器 Host Key 指纹(可选)">
                    <Input
                      className="font-mono"
                      placeholder="SHA256:..."
                      value={editing.host_key_fingerprint ?? ""}
                      onChange={(e) =>
                        setEditing({
                          ...editing,
                          host_key_fingerprint: e.target.value,
                        })
                      }
                    />
                    <div className="mt-2 rounded-md bg-slate-50 p-2.5 text-xs text-slate-600  ">
                      <p>登录目标主机执行:</p>
                      <code className="mt-1 block overflow-x-auto whitespace-nowrap font-mono text-slate-800 ">
                        ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub
                      </code>
                      <p className="mt-1.5">
                        只填写输出中的{" "}
                        <code className="font-mono text-slate-800 ">
                          SHA256:...
                        </code>{" "}
                        部分。
                      </p>
                    </div>
                  </Field>
                </div>
              </details>

              <Field label="服务器凭据(提供SSH登录名+密码/私钥)">
                <SingleSelectDropdown
                  options={serverCredentials.map((c) => ({
                    id: c.id,
                    label: `#${c.id} ${c.label}(${c.target_user})`,
                  }))}
                  value={editing.server_credential_id ?? null}
                  onChange={(id) =>
                    setEditing({
                      ...editing,
                      server_credential_id: id as number | null,
                    })
                  }
                  placeholder="(不设置)"
                  emptyText='还没有配置任何服务器凭据,先去"服务器凭据"页面添加'
                />
              </Field>
            </>
          )}
          {editing.connection_type === "agent" && (
            <p className="mb-3 text-sm text-slate-500">
              此主机由 Agent 自动注册，访问权限可在下方修改。
            </p>
          )}

          <Field label="客户端凭据(可多选)">
            <MultiSelectDropdown
              options={clientCredentials.map((c) => ({
                id: c.id,
                label: `#${c.id} ${c.label}`,
                sublabel: `(${c.auth_type === "public_key" ? "公钥" : "密码"})`,
              }))}
              selectedIds={selectedCredentialIds}
              onToggle={(id) => toggleCredential(id as number)}
              placeholder="(未选择)"
              emptyText='还没有配置任何客户端凭据,先去"客户端凭据"页面添加'
            />
          </Field>

          {error && <p className="mb-2 text-sm text-red-600 ">{error}</p>}

          <div className="mt-4 flex justify-end gap-2">
            <Button onClick={() => setEditing(null)}>取消</Button>
            <Button type="primary" onClick={save}>
              保存
            </Button>
          </div>
        </Modal>
      )}

      {importing && (
        <ImportModal
          servers={servers}
          serverCredentials={serverCredentials}
          clientCredentials={clientCredentials}
          onClose={() => setImporting(false)}
          onDone={load}
        />
      )}
    </div>
  );
}

function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="mb-3">
      <label className="mb-1 block text-xs text-slate-500 ">{label}</label>
      {children}
    </div>
  );
}

function TestStatus({ server }: { server: ServerRecord }) {
  const [showError, setShowError] = useState(false);
  const errorInputRef = useRef<import("antd").InputRef>(null);

  useEffect(() => {
    if (showError) {
      errorInputRef.current?.focus();
      errorInputRef.current?.select();
    }
  }, [showError]);

  if (!server.last_test_at || server.last_test_ok === null) {
    return <span className="text-xs text-slate-400">尚未测试</span>;
  }

  const time = new Date(server.last_test_at).toLocaleString();

  if (server.last_test_ok) {
    return (
      <div className="text-xs">
        <span className="text-emerald-600 ">成功</span>
        <div className="text-slate-400">{time}</div>
      </div>
    );
  }

  const errorText = server.last_test_error || "未知错误";

  return (
    <div className="text-xs">
      <Tooltip text={errorText}>
        <Button
          danger
          type="link"
          htmlType="button"
          onClick={() => setShowError((v) => !v)}
        >
          失败(点击查看)
        </Button>
      </Tooltip>
      <div className="text-slate-400">{time}</div>
      {showError && (
        // navigator.clipboard 在非 HTTPS/权限受限的环境下会静默失败,所以这里不依赖它,
        // 而是给一个 readOnly 的 input,点开就自动全选,不管剪贴板 API 能不能用,
        // 用户都能靠 Ctrl+C 手动复制到内容。
        <Input
          ref={errorInputRef}
          readOnly
          value={errorText}
          onFocus={(e) => e.currentTarget.select()}
          className="mt-1 text-xs"
        />
      )}
    </div>
  );
}

const IMPORT_HEADER =
  "proxy_user,target_host,target_port,server_credential_id,client_credential_id";

interface ParsedImportRow {
  line: number; // 1-based,含表头
  proxy_user: string;
  target_host: string;
  target_port: number;
  server_credential_id: number | null;
  client_credential_ids: number[];
}

interface ImportRowResult {
  line: number;
  proxy_user: string;
  ok: boolean;
  message: string;
}

function parseImportCSV(
  text: string,
  serverCredentials: ServerCredential[],
  clientCredentials: ClientCredential[],
): { rows: ParsedImportRow[]; errors: string[] } {
  const errors: string[] = [];
  const lines = text
    .split(/\r\n|\n/)
    .filter((l, i, arr) => l.trim() !== "" || i < arr.length - 1);
  if (lines.length === 0) {
    return { rows: [], errors: ["内容不能为空"] };
  }

  const header = lines[0].trim();
  if (header !== IMPORT_HEADER) {
    return { rows: [], errors: [`表头必须是: ${IMPORT_HEADER}`] };
  }

  const serverCredIds = new Set(serverCredentials.map((c) => c.id));
  const clientCredIds = new Set(clientCredentials.map((c) => c.id));

  const rows: ParsedImportRow[] = [];
  for (let i = 1; i < lines.length; i++) {
    const lineNo = i + 1;
    const raw = lines[i];
    if (raw.trim() === "") continue;
    const cols = raw.split(",");
    if (cols.length !== 5) {
      errors.push(`第 ${lineNo} 行:应为 5 列,实际 ${cols.length} 列`);
      continue;
    }
    const [
      proxyUserRaw,
      targetHostRaw,
      targetPortRaw,
      serverCredRaw,
      clientCredRaw,
    ] = cols.map((c) => c.trim());

    if (!proxyUserRaw) {
      errors.push(`第 ${lineNo} 行:proxy_user 不能为空`);
    }
    if (!targetHostRaw) {
      errors.push(`第 ${lineNo} 行:target_host 不能为空`);
    }

    let targetPort = 22;
    if (targetPortRaw !== "") {
      const n = Number(targetPortRaw);
      if (!Number.isInteger(n) || n < 1 || n > 65535) {
        errors.push(
          `第 ${lineNo} 行:target_port "${targetPortRaw}" 不合法,应为 1-65535 的整数或留空`,
        );
      } else {
        targetPort = n;
      }
    }

    let serverCredentialId: number | null = null;
    if (serverCredRaw !== "") {
      const n = Number(serverCredRaw);
      if (!Number.isInteger(n) || !serverCredIds.has(n)) {
        errors.push(
          `第 ${lineNo} 行:server_credential_id "${serverCredRaw}" 不存在`,
        );
      } else {
        serverCredentialId = n;
      }
    }

    const clientCredentialIds: number[] = [];
    if (clientCredRaw !== "") {
      for (const part of clientCredRaw.split(";")) {
        const p = part.trim();
        if (p === "") continue;
        const n = Number(p);
        if (!Number.isInteger(n) || !clientCredIds.has(n)) {
          errors.push(`第 ${lineNo} 行:client_credential_id "${p}" 不存在`);
        } else {
          clientCredentialIds.push(n);
        }
      }
    }

    rows.push({
      line: lineNo,
      proxy_user: proxyUserRaw,
      target_host: targetHostRaw,
      target_port: targetPort,
      server_credential_id: serverCredentialId,
      client_credential_ids: clientCredentialIds,
    });
  }

  return { rows, errors };
}

function ImportModal({
  servers,
  serverCredentials,
  clientCredentials,
  onClose,
  onDone,
}: {
  servers: ServerRecord[];
  serverCredentials: ServerCredential[];
  clientCredentials: ClientCredential[];
  onClose: () => void;
  onDone: () => Promise<void>;
}) {
  const [text, setText] = useState("");
  const [errors, setErrors] = useState<string[]>([]);
  const [results, setResults] = useState<ImportRowResult[] | null>(null);
  const [running, setRunning] = useState(false);

  async function runImport() {
    setResults(null);
    const { rows, errors: parseErrors } = parseImportCSV(
      text,
      serverCredentials,
      clientCredentials,
    );
    if (parseErrors.length > 0) {
      setErrors(parseErrors);
      return;
    }
    setErrors([]);
    setRunning(true);

    // 拷贝一份客户端凭据快照,让整批导入过程中的 proxy_users 变化在批内累积、
    // 又不直接改动父组件的 state(reconcileClientCredentials 会就地更新这份拷贝)。
    const workingCredentials = clientCredentials.map((c) => ({
      ...c,
      proxy_users: [...c.proxy_users],
    }));

    const existingLogins = new Set(servers.map((s) => s.proxy_user));
    const rowResults: ImportRowResult[] = [];
    for (const row of rows) {
      const wasExisting = existingLogins.has(row.proxy_user);
      try {
        await api.upsertServer({
          ...emptyServer,
          proxy_user: row.proxy_user,
          target_host: row.target_host,
          target_port: row.target_port,
          server_credential_id: row.server_credential_id,
        });
        await reconcileClientCredentials(
          workingCredentials,
          row.proxy_user,
          new Set(row.client_credential_ids),
        );
        rowResults.push({
          line: row.line,
          proxy_user: row.proxy_user,
          ok: true,
          message: wasExisting ? "已更新" : "已新增",
        });
      } catch (err) {
        rowResults.push({
          line: row.line,
          proxy_user: row.proxy_user,
          ok: false,
          message: err instanceof ApiError ? err.message : "失败",
        });
      }
    }

    setResults(rowResults);
    setRunning(false);
    await onDone();
  }

  return (
    <Modal
      open
      title="导入服务器（CSV）"
      onCancel={onClose}
      footer={null}
      width={760}
      styles={{
        body: { maxHeight: "70vh", overflowY: "auto", paddingTop: 16 },
      }}
    >
      <p className="mb-2 text-sm text-slate-500 ">
        第一行必须是表头,后面每行一台服务器。
        <code className="rounded bg-slate-100 px-1 ">proxy_user</code>{" "}
        是唯一键,已存在则覆盖更新,不存在则新增。
        <code className="rounded bg-slate-100 px-1 ">target_port</code>/
        <code className="rounded bg-slate-100 px-1 ">server_credential_id</code>
        /
        <code className="rounded bg-slate-100 px-1 ">client_credential_id</code>{" "}
        可以留空,分别默认 22、不关联服务器凭据、不关联客户端凭据;
        <code className="rounded bg-slate-100 px-1 ">client_credential_id</code>{" "}
        一个格子里可以用分号分隔多个 id。
      </p>
      <pre className="mb-3 table-scroll overflow-x-auto rounded bg-slate-50 p-2 text-xs text-slate-600  ">
        {`${IMPORT_HEADER}
srv1,192.168.1.2,,1,1;2
srv2,192.168.1.3,22,,3
srv3,192.168.1.4,,,`}
      </pre>

      <Input.TextArea
        className="h-40 font-mono text-xs"
        placeholder={IMPORT_HEADER}
        value={text}
        onChange={(e) => setText(e.target.value)}
      />

      {errors.length > 0 && (
        <div className="mt-2 max-h-32 overflow-y-auto rounded border border-red-200 bg-red-50 p-2 text-xs text-red-700   ">
          {errors.map((e, i) => (
            <div key={i}>{e}</div>
          ))}
        </div>
      )}

      {results && (
        <div className="mt-2 max-h-48 overflow-y-auto rounded border border-slate-200 p-2 text-xs ">
          {results.map((r) => (
            <div
              key={r.line}
              className={r.ok ? "text-emerald-600 " : "text-red-600 "}
            >
              第 {r.line} 行 {r.proxy_user}:{r.message}
            </div>
          ))}
        </div>
      )}

      <div className="mt-4 flex justify-end gap-2">
        <Button onClick={onClose}>关闭</Button>
        <Button
          type="primary"
          onClick={runImport}
          disabled={running || text.trim() === ""}
        >
          {running ? "导入中..." : "导入"}
        </Button>
      </div>
    </Modal>
  );
}
