import {
  ModalForm,
  ProFormText,
  ProFormSelect,
  ProFormDependency,
  ProFormDigit,
  ProFormSwitch,
  ProFormTextArea,
} from "@ant-design/pro-components";
import { Collapse, Typography } from "antd";
import { DownOutlined } from "@ant-design/icons";
import type { ServerRecord, ServerCredential, ClientCredential } from "./api";
export type ServerFormValues = ServerRecord & { credentialIds: number[] };
export function ServerForm({
  initial,
  isNew,
  serverCredentials,
  clientCredentials,
  onSave,
  onClose,
}: {
  initial: ServerFormValues;
  isNew: boolean;
  serverCredentials: ServerCredential[];
  clientCredentials: ClientCredential[];
  onSave: (values: ServerFormValues) => Promise<boolean>;
  onClose: () => void;
}) {
  const agent = initial.connection_type === "agent";
  return (
    <ModalForm<ServerFormValues>
      open
      key={initial.id || "new"}
      title={isNew ? "新建服务器" : "编辑服务器"}
      width={640}
      autoComplete="off"
      initialValues={initial}
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
      modalProps={{
        destroyOnHidden: true,
        styles: { body: { maxHeight: "70vh", overflowY: "auto" } },
      }}
      submitter={{ searchConfig: { submitText: "保存" } }}
      onFinish={onSave}
    >
      <ProFormText
        name="proxy_user"
        label="代理登录名"
        placeholder="例如server-01"
        rules={[{ required: true, message: "请输入代理登录名" }]}
        fieldProps={{ readOnly: agent, autoComplete: "off" }}
      />
      {agent ? (
        <Typography.Paragraph type="secondary">
          此主机由Agent自动注册，可调整访问凭证。
        </Typography.Paragraph>
      ) : (
        <>
          <ProFormSelect
            name="route_mode"
            label="路由模式"
            allowClear={false}
            options={[
              { value: "fixed", label: "固定端口" },
              { value: "dynamic_port", label: "动态端口" },
            ]}
          />
          <ProFormText
            name="target_host"
            label="目标地址"
            placeholder="请输入IP或域名"
            rules={[{ required: true, message: "请输入目标地址" }]}
          />
          <ProFormDependency name={["route_mode"]}>
            {({ route_mode }) =>
              route_mode === "dynamic_port" ? (
                <>
                  <Typography.Paragraph type="secondary">
                    代理登录名以 {"${PORT}"} 结尾，例如 {"server-${PORT}"}。
                  </Typography.Paragraph>
                  <ProFormDigit
                    name="port_min"
                    label="最小端口"
                    min={1}
                    max={65535}
                    rules={[{ required: true, message: "请输入最小端口" }]}
                  />
                  <ProFormDigit
                    name="port_max"
                    label="最大端口"
                    min={1}
                    max={65535}
                    rules={[{ required: true, message: "请输入最大端口" }]}
                  />
                </>
              ) : (
                <ProFormDigit
                  name="target_port"
                  label="SSH端口"
                  min={1}
                  max={65535}
                  rules={[{ required: true, message: "请输入SSH端口" }]}
                />
              )
            }
          </ProFormDependency>
          <ProFormSelect
            name="server_credential_id"
            label="服务器凭证"
            placeholder="选择服务器凭证"
            options={serverCredentials.map((c) => ({
              label: `${c.label} · ${c.target_user}`,
              value: c.id,
            }))}
            fieldProps={{ showSearch: true, optionFilterProp: "label" }}
          />

        </>
      )}
      <ProFormSelect
        name="ownership"
        label="归属"
        placeholder="选择服务器归属"
        options={[
          { value: "pve_vm", label: "IDC-PVE" },
          { value: "office_pve", label: "OFFICE-PVE" },
          { value: "physical", label: "物理机" },
          { value: "cloud", label: "云服务器" },
          { value: "network_device", label: "网络设备" },
        ]}
      />
      <ProFormTextArea
        name="remark"
        label="备注"
        placeholder="记录用途、连接限制或故障原因"
        fieldProps={{ autoSize: { minRows: 2, maxRows: 5 } }}
      />
      <ProFormSelect
        name="credentialIds"
        label="客户端凭证"
        mode="multiple"
        placeholder="选择客户端凭证"
        options={clientCredentials.map((c) => ({
          label: c.label,
          value: c.id,
        }))}
        fieldProps={{ optionFilterProp: "label" }}
      />
      {!agent && (
          <Collapse
            expandIconPlacement="end"
            expandIcon={({ isActive }) => <DownOutlined rotate={isActive ? 180 : 0} style={{ fontSize: 10, color: "#8f959e" }} />}
            styles={{ header: { padding: "10px 0", borderTop: "1px solid #f0f0f0", color: "#646a73" }, body: { padding: "12px 0 0" } }}
            ghost
            items={[
              {
                key: "advanced",
                label: "高级配置",
                children: (
                  <>
                    <ProFormSwitch
                      name="legacy_algorithms"
                      label="兼容旧设备算法"
                      extra="仅用于不支持现代算法的设备。"
                    />
                    <ProFormText
                      name="host_key_fingerprint"
                      label="Host Key指纹"
                      placeholder="SHA256:..."
                      extra="在目标主机执行ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub获取。"
                    />
                  </>
                ),
              },
            ]}
          />
      )}
    </ModalForm>
  );
}
