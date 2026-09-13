import { App } from "antd";
export function useFeedback() {
  const { modal, message } = App.useApp();
  return {
    success: (content: string) => {
      void message.success(content);
    },
    confirm: (content: string) =>
      modal
        .confirm({
          title: content,
          okButtonProps: { danger: /删除|取消勾选|禁用/.test(content) },
          okText: "确定",
          cancelText: "取消",
        })
        .then(
          (value) => Boolean(value),
          () => false,
        ),
    alert: (content: string) => {
      void message.error(content);
    },
  };
}
