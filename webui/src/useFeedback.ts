import { App } from "antd";
export function useFeedback() {
  const { modal, message } = App.useApp();
  return {
    confirm: (content: string) =>
      modal
        .confirm({
          title: "确认操作",
          content,
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
