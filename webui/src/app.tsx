import { App as AntApp, ConfigProvider } from "antd";
import { reportTheme } from "../config/reportTheme";
import zhCN from "antd/locale/zh_CN";
import type { ReactNode } from "react";
import "antd/dist/reset.css";
import "./index.css";
export function rootContainer(container: ReactNode) {
  return (
    <ConfigProvider locale={zhCN} theme={reportTheme}>
      <AntApp>{container}</AntApp>
    </ConfigProvider>
  );
}
