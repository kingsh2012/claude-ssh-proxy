import type { ProLayoutProps } from "@ant-design/pro-components";

/**
 * @name
 */
const Settings: ProLayoutProps & {
  logo?: false;
} = {
  navTheme: "light",
  siderWidth: 208,
  colorPrimary: "#15803D",
  layout: "mix",
  defaultCollapsed:
    typeof window !== "undefined" ? window.innerWidth < 992 : false,
  contentWidth: "Fluid",
  fixedHeader: true,
  fixSiderbar: true,
  colorWeak: false,
  title: "ops-ssh-proxy",
  logo: false,
  iconfontUrl: "",
  token: {
    bgLayout: "#F4F5F7",
    header: {
      colorBgHeader: "#FFFFFF",
      colorBgScrollHeader: "#FFFFFF",
      colorHeaderTitle: "#1F2329",
      colorBgMenuItemHover: "#F0FDF4",
      colorBgMenuItemSelected: "#DCFCE7",
      colorTextMenuSelected: "#166534",
      colorTextMenuActive: "#14532D",
      colorTextMenu: "#424750",
      colorTextMenuSecondary: "#646A73",
      colorBgRightActionsItemHover: "#F0FDF4",
      colorTextRightActionsItem: "#424750",
      heightLayoutHeader: 48,
    },
    sider: {
      colorBgCollapsedButton: "#FFFFFF",
      colorTextCollapsedButtonHover: "#166534",
      colorTextCollapsedButton: "#646A73",
      colorMenuBackground: "#FFFFFF",
      menuHeight: 40,
      colorBgMenuItemCollapsedElevated: "#FFFFFF",
      colorMenuItemDivider: "#E8E9EB",
      colorBgMenuItemHover: "#F0FDF4",
      colorBgMenuItemActive: "#DCFCE7",
      colorBgMenuItemSelected: "#DCFCE7",
      colorTextMenuSelected: "#166534",
      colorTextMenuItemHover: "#166534",
      colorTextMenuActive: "#14532D",
      colorTextMenu: "#424750",
      colorTextMenuSecondary: "#646A73",
      colorTextMenuTitle: "#1F2329",
      colorTextSubMenuSelected: "#166534",
      paddingInlineLayoutMenu: 8,
      paddingBlockLayoutMenu: 8,
    },
    pageContainer: {
      colorBgPageContainer: "#F4F5F7",
      colorBgPageContainerFixed: "#F4F5F7",
      paddingInlinePageContainerContent: 12,
      paddingBlockPageContainerContent: 12,
    },
  },
};

export default Settings;
