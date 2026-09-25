import { ArrowLeft, ArrowUpRight, CircleDashed, LockKeyhole } from 'lucide-react'
import type { Section } from '../../app/sections'

export function ModulePreview({ section }: { section: Section }) {
  return <div className="module-page">
    <a className="back-link" href="#/home"><ArrowLeft size={16} /> 返回总览</a>
    <div className="module-page-head"><span className="module-page-icon"><section.icon size={28} strokeWidth={1.7} /></span><span className="section-overline">{section.eyebrow} / PREVIEW</span><h1>{section.title}</h1><p>{section.description}</p></div>
    <div className="module-page-grid">
      <div className="module-empty-card">
        <div className="empty-illustration" aria-hidden="true"><span /><span /><CircleDashed size={38} /></div>
        <span className="empty-kicker">WORKSPACE IN PROGRESS</span>
        <h2>工作区正在接入</h2>
        <p>本页面展示产品结构。登录、真实资源和管理操作将在安全接入及对应后端能力就绪后开放。</p>
        <div className="empty-status"><LockKeyhole size={15} /> 只读预览 · 无账户数据</div>
      </div>
      <aside className="module-aside"><span className="aside-overline">模块能力</span><h3>接下来会在这里管理</h3><ul>{section.details.map((detail, index) => <li key={detail}><span>0{index + 1}</span>{detail}<ArrowUpRight size={15} /></li>)}</ul><p>实际操作以服务端权限和套餐授权为准。</p></aside>
    </div>
  </div>
}
