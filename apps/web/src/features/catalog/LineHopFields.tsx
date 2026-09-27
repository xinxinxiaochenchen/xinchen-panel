import type { NodeRecord } from '../../lib/catalog'

export function LineHopFields({ ids, nodes, onChange }: { ids: string[]; nodes: NodeRecord[]; onChange: (ids: string[]) => void }) {
  return <>
    <p className="catalog-form-note">先保存拓扑，再启用线路。所有节点须配置中继端口，且每一跳都在套餐授权范围内；Agent 应用配置后代理连接才会出现在订阅中。</p>
    {ids.map((id, position) => {
      const label = position === 0 ? '入口' : position === ids.length - 1 ? '出口' : '中转'
      const capability = position === 0 ? 'proxy' : 'forward'
      return <div key={position}>
        <label htmlFor={`line-hop-${position}`}>{position + 1}. {label}节点</label>
        <select id={`line-hop-${position}`} required value={id} onChange={(event) => onChange(ids.map((value, index) => index === position ? event.target.value : value))}>
          <option value="">请选择节点</option>
          {nodes.filter((node) => node.enabled && node.relay_port && node.capabilities.includes('forward') && node.capabilities.includes(capability) && (node.id === id || !ids.includes(node.id))).map((node) => <option key={node.id} value={node.id}>{node.name} · {node.group_code} · {node.region}</option>)}
        </select>
        {position > 0 && position < ids.length - 1 && <button className="catalog-text-button" type="button" onClick={() => onChange(ids.filter((_, index) => index !== position))}>移除此中转</button>}
      </div>
    })}
    {ids.length < 8 && <button className="catalog-text-button" type="button" onClick={() => onChange([...ids.slice(0, -1), '', ids[ids.length - 1]])}>添加中转节点</button>}
  </>
}
