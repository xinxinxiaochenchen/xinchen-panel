import type { NodeRecord } from '../../lib/catalog'

export function LineHopFields({ ids, nodes, onChange }: { ids: string[]; nodes: NodeRecord[]; onChange: (ids: string[]) => void }) {
  return <>
    <p className="catalog-form-note">多跳线路暂不支持实际转发，只能保存停用草稿。每一跳都须在套餐授权范围内。</p>
    {ids.map((id, position) => {
      const label = position === 0 ? '入口' : position === ids.length - 1 ? '出口' : '中转'
      const capability = position === 0 ? 'proxy' : 'forward'
      return <div key={position}>
        <label htmlFor={`line-hop-${position}`}>{position + 1}. {label}节点</label>
        <select id={`line-hop-${position}`} required value={id} onChange={(event) => onChange(ids.map((value, index) => index === position ? event.target.value : value))}>
          <option value="">请选择节点</option>
          {nodes.filter((node) => node.enabled && node.capabilities.includes(capability) && (node.id === id || !ids.includes(node.id))).map((node) => <option key={node.id} value={node.id}>{node.name} · {node.group_code} · {node.region}</option>)}
        </select>
        {position > 0 && position < ids.length - 1 && <button className="catalog-text-button" type="button" onClick={() => onChange(ids.filter((_, index) => index !== position))}>移除此中转</button>}
      </div>
    })}
    {ids.length < 8 && <button className="catalog-text-button" type="button" onClick={() => onChange([...ids.slice(0, -1), '', ids[ids.length - 1]])}>添加中转节点</button>}
  </>
}
