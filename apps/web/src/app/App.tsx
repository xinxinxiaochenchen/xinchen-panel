import { useEffect, useState } from 'react'
import { ArrowUpRight, LockKeyhole, Menu, Moon, Sun, X } from 'lucide-react'
import { sections, sectionFromHash, type SectionId } from './sections'
import { useHealth } from '../lib/useHealth'
import { PreviewHome } from '../features/preview/PreviewHome'
import { ModulePreview } from '../features/preview/ModulePreview'

function readTheme(): 'light' | 'dark' {
  return window.localStorage.getItem('control-theme') === 'dark' ? 'dark' : 'light'
}

export function App() {
  const [sectionId, setSectionId] = useState<SectionId>(sectionFromHash)
  const [theme, setTheme] = useState<'light' | 'dark'>(readTheme)
  const [menuOpen, setMenuOpen] = useState(false)
  const health = useHealth()
  const selected = sections.find((section) => section.id === sectionId) ?? sections[0]

  useEffect(() => {
    const onHashChange = () => {
      setSectionId(sectionFromHash())
      setMenuOpen(false)
    }
    window.addEventListener('hashchange', onHashChange)
    return () => window.removeEventListener('hashchange', onHashChange)
  }, [])

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    window.localStorage.setItem('control-theme', theme)
  }, [theme])

  useEffect(() => {
    document.title = `${selected.label} · 网络控制平面预览`
  }, [selected.label])

  return (
    <div className="app-shell">
      <header className="site-header">
        <div className="header-inner">
          <a className="brand" href="#/home" aria-label="返回控制台首页">
            <span className="brand-mark" aria-hidden="true"><i /><i /><i /><i /></span>
            <span className="brand-copy"><strong>网络控制平面</strong><small>CONTROL PLANE</small></span>
          </a>

          <nav className="desktop-nav" aria-label="主导航">
            {sections.map((section) => (
              <a key={section.id} className={sectionId === section.id ? 'nav-link active' : 'nav-link'}
                href={`#/${section.id}`} aria-current={sectionId === section.id ? 'page' : undefined}>
                {section.label}
              </a>
            ))}
          </nav>

          <div className="header-actions">
            <span className="preview-pill"><span className="preview-dot" />纯 IP 预览</span>
            <button className="icon-button" type="button" onClick={() => setTheme(theme === 'light' ? 'dark' : 'light')}
              aria-label={theme === 'light' ? '切换深色主题' : '切换浅色主题'}>
              {theme === 'light' ? <Moon size={18} /> : <Sun size={18} />}
            </button>
            <button className="icon-button mobile-menu-button" type="button" aria-label={menuOpen ? '关闭导航' : '打开导航'}
              aria-expanded={menuOpen} onClick={() => setMenuOpen(!menuOpen)}>
              {menuOpen ? <X size={20} /> : <Menu size={20} />}
            </button>
          </div>
        </div>
        {menuOpen && <nav className="mobile-nav" aria-label="手机导航">
          {sections.map((section) => <a key={section.id} href={`#/${section.id}`}
            className={sectionId === section.id ? 'mobile-nav-link active' : 'mobile-nav-link'}>
            <section.icon size={18} />{section.label}
          </a>)}
        </nav>}
      </header>

      <main className="main-content">
        {sectionId === 'home' ? <PreviewHome health={health} /> : <ModulePreview section={selected} />}
      </main>

      <footer className="site-footer">
        <span>网络控制平面 · 独立设计的节点与线路管理系统</span>
        <span className="footer-security"><LockKeyhole size={14} />管理登录将在安全接入后开放</span>
        <a href="/api/v1/health/ready" target="_blank" rel="noreferrer">查看健康接口 <ArrowUpRight size={14} /></a>
      </footer>
    </div>
  )
}
