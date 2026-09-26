import type { LucideIcon } from "lucide-react";
import {
  CircleUserRound,
  GitBranch,
  Globe2,
  Home,
  Layers3,
  ListFilter,
  Route,
  Shield,
} from "lucide-react";

export type SectionId =
  | "home"
  | "nodes"
  | "lines"
  | "forward"
  | "subscriptions"
  | "routing"
  | "account"
  | "admin";

export type Section = {
  id: SectionId;
  label: string;
  eyebrow: string;
  title: string;
  description: string;
  details: string[];
  icon: LucideIcon;
};

export const sections: Section[] = [
  {
    id: "home",
    label: "首页",
    eyebrow: "OVERVIEW",
    title: "控制台总览",
    description: "账户、套餐和网络资源汇于一处。",
    details: [],
    icon: Home,
  },
  {
    id: "nodes",
    label: "节点",
    eyebrow: "NODE DIRECTORY",
    title: "节点与资源域",
    description: "统一查看代理出口和中转节点的能力、区域与状态。",
    details: ["节点组与资源域", "出口和中转能力", "在线状态与负载"],
    icon: Globe2,
  },
  {
    id: "lines",
    label: "线路",
    eyebrow: "ROUTE TOPOLOGY",
    title: "线路编排",
    description: "将节点组成有序路径，并为每条线路定义角色和优先级。",
    details: ["入口与落地角色", "线路启停和权重", "套餐可用范围"],
    icon: Route,
  },
  {
    id: "forward",
    label: "转发",
    eyebrow: "PORT FORWARDING",
    title: "转发规则",
    description: "从入口端口到目标地址，以明确的授权和配置版本保持收敛。",
    details: ["TCP、UDP 与双协议", "端口占用和目标策略", "配置下发状态"],
    icon: GitBranch,
  },
  {
    id: "subscriptions",
    label: "订阅",
    eyebrow: "CLIENT DELIVERY",
    title: "订阅配置",
    description: "按套餐许可选择节点和线路，生成独立的客户端配置。",
    details: ["多订阅与绑定", "Token 重置", "客户端名称模板"],
    icon: Layers3,
  },
  {
    id: "routing",
    label: "分流",
    eyebrow: "TRAFFIC POLICY",
    title: "分流策略",
    description: "让域名和地址规则选择合适的线路，并为未命中流量设置默认路径。",
    details: ["域名与 CIDR 规则", "规则优先级", "默认线路"],
    icon: ListFilter,
  },
  {
    id: "account",
    label: "账户",
    eyebrow: "ACCOUNT & ACCESS",
    title: "账户与权限",
    description: "在一个地方查看套餐授权、登录安全和账户设置。",
    details: ["套餐状态", "资源权限", "安全设置"],
    icon: CircleUserRound,
  },
  {
    id: "admin",
    label: "管理",
    eyebrow: "ADMINISTRATION",
    title: "资源与账户管理",
    description: "配置用户、套餐、资源域和节点。",
    details: [],
    icon: Shield,
  },
];

export function sectionFromHash(): SectionId {
  const id = window.location.hash.replace(/^#\/?/, "");
  return sections.find((section) => section.id === id)?.id ?? "home";
}
