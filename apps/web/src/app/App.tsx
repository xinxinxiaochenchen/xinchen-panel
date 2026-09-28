import { useEffect, useState } from "react";
import {
  ArrowUpRight,
  LockKeyhole,
  LogOut,
  Menu,
  Moon,
  Sun,
  X,
} from "lucide-react";
import { sections, sectionFromHash, type SectionId } from "./sections";
import { useHealth } from "../lib/useHealth";
import { PreviewHome } from "../features/preview/PreviewHome";
import { ModulePreview } from "../features/preview/ModulePreview";
import { DashboardHome } from "../features/dashboard/DashboardHome";
import { AccountPage } from "../features/dashboard/AccountPage";
import { SignIn } from "../features/dashboard/SignIn";
import { InitialSetup } from "../features/dashboard/InitialSetup";
import { CatalogDirectory } from "../features/catalog/CatalogDirectory";
import { ForwardDirectory } from "../features/catalog/ForwardDirectory";
import { SubscriptionDirectory } from "../features/catalog/SubscriptionDirectory";
import { RoutingDirectory } from "../features/catalog/RoutingDirectory";
import { AdminDirectory } from "../features/admin/AdminDirectory";
import { hasAdminAccess } from "../lib/admin";
import { useViewer } from "../lib/useViewer";
import { readCSRFToken, sessionTransportLabel } from "../lib/browserSession";

function readTheme(): "light" | "dark" {
  return window.localStorage.getItem("control-theme") === "dark"
    ? "dark"
    : "light";
}

export function App() {
  const [sectionId, setSectionId] = useState<SectionId>(sectionFromHash);
  const [theme, setTheme] = useState<"light" | "dark">(readTheme);
  const [menuOpen, setMenuOpen] = useState(false);
  const health = useHealth();
  const { viewer, refresh } = useViewer();
  const [signOutError, setSignOutError] = useState("");
  const selected =
    sections.find((section) => section.id === sectionId) ?? sections[0];
  const visibleSections = sections.filter(
    (section) =>
      section.id !== "admin" ||
      (viewer.kind === "signed-in" && hasAdminAccess(viewer.user.permissions)),
  );

  useEffect(() => {
    const onHashChange = () => {
      setSectionId(sectionFromHash());
      setMenuOpen(false);
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    window.localStorage.setItem("control-theme", theme);
  }, [theme]);

  useEffect(() => {
    document.title = `${selected.label} · 网络控制平面${viewer.kind === "preview" ? "预览" : ""}`;
  }, [selected.label, viewer.kind]);

  async function signOut() {
    const csrf = readCSRFToken(document.cookie, window.location.protocol);
    if (!csrf) {
      setSignOutError("退出凭据不可用，请刷新页面后重试。");
      return;
    }
    try {
      const response = await fetch("/api/v1/auth/logout", {
        method: "POST",
        credentials: "same-origin",
        headers: { "X-CSRF-Token": csrf },
      });
      if (!response.ok) {
        setSignOutError("退出失败，请稍后重试。");
        return;
      }
      setSignOutError("");
      refresh();
    } catch {
      setSignOutError("无法连接账户服务，请稍后重试。");
    }
  }

  return (
    <div className="app-shell">
      <header className="site-header">
        <div className="header-inner">
          <a className="brand" href="#/home" aria-label="返回控制台首页">
            <span className="brand-mark" aria-hidden="true">
              <i />
              <i />
              <i />
              <i />
            </span>
            <span className="brand-copy">
              <strong>网络控制平面</strong>
              <small>CONTROL PLANE</small>
            </span>
          </a>

          <nav className="desktop-nav" aria-label="主导航">
            {visibleSections.map((section) => (
              <a
                key={section.id}
                className={
                  sectionId === section.id ? "nav-link active" : "nav-link"
                }
                href={`#/${section.id}`}
                aria-current={sectionId === section.id ? "page" : undefined}
              >
                {section.label}
              </a>
            ))}
          </nav>

          <div className="header-actions">
            {viewer.kind === "preview" ? (
              <span className="preview-pill">
                <span className="preview-dot" />纯 IP 预览
              </span>
            ) : viewer.kind === "signed-in" ? (
              <span className="preview-pill account-pill">
                {viewer.user.email}
              </span>
            ) : null}
            {viewer.kind === "signed-in" && (
              <button
                className="icon-button"
                type="button"
                onClick={() => void signOut()}
                aria-label="退出登录"
              >
                <LogOut size={18} />
              </button>
            )}
            <button
              className="icon-button"
              type="button"
              onClick={() => setTheme(theme === "light" ? "dark" : "light")}
              aria-label={theme === "light" ? "切换深色主题" : "切换浅色主题"}
            >
              {theme === "light" ? <Moon size={18} /> : <Sun size={18} />}
            </button>
            <button
              className="icon-button mobile-menu-button"
              type="button"
              aria-label={menuOpen ? "关闭导航" : "打开导航"}
              aria-expanded={menuOpen}
              onClick={() => setMenuOpen(!menuOpen)}
            >
              {menuOpen ? <X size={20} /> : <Menu size={20} />}
            </button>
          </div>
        </div>
        {menuOpen && (
          <nav className="mobile-nav" aria-label="手机导航">
            {visibleSections.map((section) => (
              <a
                key={section.id}
                href={`#/${section.id}`}
                className={
                  sectionId === section.id
                    ? "mobile-nav-link active"
                    : "mobile-nav-link"
                }
              >
                <section.icon size={18} />
                {section.label}
              </a>
            ))}
          </nav>
        )}
      </header>

      <main className="main-content">
        {viewer.kind === "preview" ? (
          sectionId === "home" ? (
            <PreviewHome health={health} />
          ) : (
            <ModulePreview section={selected} />
          )
        ) : viewer.kind === "checking" ? (
          <div className="page-state" role="status">
            正在检查账户会话…
          </div>
        ) : viewer.kind === "error" ? (
          <div className="page-state" role="alert">
            {viewer.message}
            <button type="button" onClick={refresh}>
              重试
            </button>
          </div>
        ) : viewer.kind === "setup" ? (
          <InitialSetup enabled={viewer.enabled} onInitialized={refresh} />
        ) : viewer.kind === "guest" ? (
          <SignIn onSignedIn={refresh} />
        ) : sectionId === "home" ? (
          <DashboardHome user={viewer.user} />
        ) : sectionId === "account" ? (
          <AccountPage user={viewer.user} onSessionChanged={refresh} />
        ) : sectionId === "nodes" || sectionId === "lines" ? (
          <CatalogDirectory section={selected} user={viewer.user} />
        ) : sectionId === "forward" ? (
          <ForwardDirectory section={selected} user={viewer.user} />
        ) : sectionId === "subscriptions" ? (
          <SubscriptionDirectory section={selected} user={viewer.user} />
        ) : sectionId === "routing" ? (
          <RoutingDirectory section={selected} user={viewer.user} />
        ) : sectionId === "admin" ? (
          hasAdminAccess(viewer.user.permissions) ? (
            <AdminDirectory user={viewer.user} />
          ) : (
            <div className="page-state" role="alert">
              当前账户无管理权限。
            </div>
          )
        ) : (
          <ModulePreview section={selected} signedIn />
        )}
        {signOutError && (
          <div className="global-error" role="alert">
            {signOutError}
          </div>
        )}
      </main>

      <footer className="site-footer">
        <span>网络控制平面 · 独立设计的节点与线路管理系统</span>
        <span className="footer-security">
          <LockKeyhole size={14} />
          {viewer.kind === "preview"
            ? "此部署已关闭账户登录"
            : sessionTransportLabel(window.location.protocol)}
        </span>
        <a href="/api/v1/health/ready" target="_blank" rel="noreferrer">
          查看健康接口 <ArrowUpRight size={14} />
        </a>
      </footer>
    </div>
  );
}
