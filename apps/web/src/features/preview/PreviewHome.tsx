import { Activity, ArrowRight, ArrowUpRight, Check, CircleDot, Cloud, LockKeyhole, Radio, ServerCog } from 'lucide-react'
import { sections } from '../../app/sections'
import type { HealthState } from '../../lib/useHealth'

function HealthCard({ health }: { health: HealthState }) {
  const online = health.status === 'online'
  const checking = health.status === 'checking'
  return <div className="health-card">
    <div className="health-card-top">
      <span className="health-label"><Activity size={16} /> 实时状态</span>
      <span className={online ? 'health-chip online' : 'health-chip'}>
        <span className="health-dot" />{checking ? '检测中' : online ? '运行正常' : '连接异常'}
      </span>
    </div>
    <div className="health-orbit" aria-hidden="true"><span /><span /><span /></div>
    <div className="health-card-main">
      <span className="health-kicker">CONTROL PLANE STATUS</span>
      <strong>{checking ? '正在连接控制面' : online ? '控制面已连接' : '控制面暂不可用'}</strong>
      <p>{online ? '服务与数据库就绪检查已通过。' : checking ? '正在向服务器发起实时健康检查。' : '就绪检查未通过，请稍后重试。'}</p>
    </div>
    <div className="health-card-bottom">
      <span><Radio size={14} /> 15 秒自动刷新</span>
      <span>{health.checkedAt ? `更新于 ${health.checkedAt.toLocaleTimeString('zh-CN', { hour12: false })}` : '等待首次结果'}</span>
    </div>
  </div>
}

export function PreviewHome({ health }: { health: HealthState }) {
  return <>
    <section className="hero-section">
      <div className="hero-copy">
        <div className="eyebrow"><span className="eyebrow-line" /> NETWORK CONTROL PLANE <span className="eyebrow-tag">PREVIEW / 01</span></div>
        <h1>让网络资源<br /><em>清晰可控。</em></h1>
        <p>在一个控制平面里组织节点、线路与转发规则。先从纯 IP 预览开始，逐步连接 Agent、订阅与流量能力。</p>
        <div className="hero-actions">
          <a className="primary-button" href="#/nodes">探索资源结构 <ArrowRight size={17} /></a>
          <a className="text-button" href="/api/v1/health/ready" target="_blank" rel="noreferrer">查看 API 状态 <ArrowUpRight size={16} /></a>
        </div>
        <div className="hero-footnote"><LockKeyhole size={15} /> 当前为只读预览，账户和流量数据未公开</div>
      </div>
      <HealthCard health={health} />
    </section>

    <section className="system-strip" aria-label="部署阶段">
      <div className="stage-item active"><span className="stage-icon"><ServerCog size={20} /></span><span><small>01 / CONTROL</small><strong>控制面基础</strong><span>{health.status === 'online' ? '服务与数据库在线' : health.status === 'checking' ? '正在检查连接' : '连接检查未通过'}</span></span>{health.status === 'online' && <Check className="stage-check" size={16} />}</div>
      <div className="stage-line" />
      <div className="stage-item"><span className="stage-icon"><Radio size={20} /></span><span><small>02 / AGENT</small><strong>节点连接</strong><span>安全通信接入中</span></span></div>
      <div className="stage-line" />
      <div className="stage-item"><span className="stage-icon"><Cloud size={20} /></span><span><small>03 / ACCESS</small><strong>用户工作台</strong><span>等待安全入口开放</span></span></div>
    </section>

    <section className="module-section">
      <div className="section-heading">
        <div><span className="section-overline">ONE NETWORK, ONE CONTROL PLANE</span><h2>围绕网络资源构建的工作台</h2><p>从节点能力到用户订阅，各个模块拥有清晰的边界与流转关系。</p></div>
        <span className="section-count"><CircleDot size={15} /> 06 个资源模块</span>
      </div>
      <div className="module-grid">
        {sections.filter((section) => section.id !== 'home' && section.id !== 'account').map((section, index) => <a className="module-card" href={`#/${section.id}`} key={section.id}>
          <span className="module-index">0{index + 1}</span>
          <span className="module-icon"><section.icon size={22} strokeWidth={1.8} /></span>
          <strong>{section.label}</strong><p>{section.description}</p>
          <span className="module-footer">查看模块 <ArrowUpRight size={16} /></span>
        </a>)}
        <a className="module-card account-card" href="#/account">
          <span className="module-index">06</span>
          <span className="module-icon"><LockKeyhole size={22} strokeWidth={1.8} /></span>
          <strong>账户与权限</strong><p>套餐范围、资源授权和安全设置集中管理。</p>
          <span className="module-footer">查看模块 <ArrowUpRight size={16} /></span>
        </a>
      </div>
    </section>

    <section className="preview-banner"><div><span className="banner-icon"><LockKeyhole size={19} /></span><span><strong>当前为公开预览</strong><small>真实账户信息、管理操作和代理凭据不会在纯 HTTP 页面展示。</small></span></div><span className="banner-label">READ ONLY</span></section>
  </>
}
