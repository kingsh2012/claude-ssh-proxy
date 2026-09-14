import { defineConfig } from "@umijs/max";
import routes from "./routes";

// Ant Design Pro 官方精简骨架：保留路由和构建，移除 mock、示例及 OpenAPI。
export default defineConfig({
  title: "aiagent-ssh-proxy",
  favicons: ["data:,"],
  hash: true,
  esbuildMinifyIIFE: true,
  publicPath: "/",
  routes,
  npmClient: "npm",
  extraPostCSSPlugins: [require("@tailwindcss/postcss")()],
  proxy: { "/api": { target: "http://127.0.0.1:8080", changeOrigin: true } },
});
