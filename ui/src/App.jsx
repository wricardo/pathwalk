import { useState, useEffect, useRef, useCallback, useMemo } from 'react'

// ── constants ──────────────────────────────────────────────────────────────────

const NODE_W = 180
const NODE_H = 52

const TYPE_META = {
  Default:    { bg: '#1e3a5f', stroke: '#3b82f6', label: 'LLM' },
  'End Call': { bg: '#3b0f0f', stroke: '#ef4444', label: 'Terminal' },
  Route:      { bg: '#431407', stroke: '#f97316', label: 'Route' },
  Webhook:    { bg: '#2d1b69', stroke: '#8b5cf6', label: 'Webhook' },
}

function getNodeMeta(type) {
  return TYPE_META[type] || { bg: '#1e293b', stroke: '#64748b', label: type || '?' }
}

// ── layout helpers ─────────────────────────────────────────────────────────────

// Assigns positions to nodes that don't have them using BFS layering.
function assignPositions(nodes, edges) {
  if (nodes.length && nodes.every(n => n.position)) return nodes

  const start = nodes.find(n => n.data?.isStart) || nodes[0]
  if (!start) return nodes.map((n, i) => ({ ...n, position: n.position || { x: i * 220, y: 0 } }))

  const adj = {}
  edges.forEach(e => {
    if (!adj[e.source]) adj[e.source] = []
    adj[e.source].push(e.target)
  })

  const layer = {}
  const queue = [start.id]
  layer[start.id] = 0
  while (queue.length) {
    const id = queue.shift()
    ;(adj[id] || []).forEach(tid => {
      if (layer[tid] === undefined) {
        layer[tid] = layer[id] + 1
        queue.push(tid)
      }
    })
  }

  const byLayer = {}
  nodes.forEach(n => {
    const l = layer[n.id] ?? 99
    if (!byLayer[l]) byLayer[l] = []
    byLayer[l].push(n.id)
  })

  const posMap = {}
  Object.entries(byLayer).forEach(([l, ids]) => {
    const y = parseInt(l) * 140
    ids.forEach((id, i) => {
      posMap[id] = { x: i * 220 - ((ids.length - 1) * 220) / 2 + 400, y }
    })
  })

  return nodes.map(n => ({ ...n, position: n.position || posMap[n.id] || { x: 0, y: 0 } }))
}

function calcViewBox(nodes, pad = 80) {
  if (!nodes.length) return { x: -pad, y: -pad, w: 800 + pad * 2, h: 600 + pad * 2 }
  const xs = nodes.map(n => n.position?.x ?? 0)
  const ys = nodes.map(n => n.position?.y ?? 0)
  const minX = Math.min(...xs) - pad
  const minY = Math.min(...ys) - pad
  const maxX = Math.max(...xs) + NODE_W + pad
  const maxY = Math.max(...ys) + NODE_H + pad
  return { x: minX, y: minY, w: maxX - minX, h: maxY - minY }
}

// ── SVG components ─────────────────────────────────────────────────────────────

function FlowEdge({ edge, nodeMap }) {
  const sn = nodeMap[edge.source]
  const tn = nodeMap[edge.target]
  if (!sn || !tn) return null

  const sp = sn.position || { x: 0, y: 0 }
  const tp = tn.position || { x: 0, y: 0 }
  const sx = sp.x + NODE_W / 2, sy = sp.y + NODE_H
  const tx = tp.x + NODE_W / 2, ty = tp.y
  const offset = Math.max(40, Math.abs(ty - sy) * 0.4)
  const d = `M ${sx} ${sy} C ${sx} ${sy + offset}, ${tx} ${ty - offset}, ${tx} ${ty}`
  const lx = (sx + tx) / 2
  const ly = (sy + ty) / 2

  return (
    <g>
      <path d={d} fill="none" stroke="#334155" strokeWidth="1.5" markerEnd="url(#arrowhead)" />
      {edge.data?.label && (
        <text
          x={lx} y={ly}
          textAnchor="middle" dominantBaseline="central"
          fill="#64748b" fontSize="10"
          style={{ pointerEvents: 'none' }}
        >
          {edge.data.label}
        </text>
      )}
    </g>
  )
}

function FlowNode({ node, isSelected, onClick }) {
  const meta = getNodeMeta(node.type)
  const { x = 0, y = 0 } = node.position || {}
  const name = node.data?.name || node.id
  const label = name.length > 22 ? name.slice(0, 20) + '…' : name
  const stroke = isSelected ? '#f8fafc' : meta.stroke
  const sWidth = isSelected ? 2.5 : 1.5

  if (node.type === 'Route') {
    const cx = x + NODE_W / 2, cy = y + NODE_H / 2
    const hw = NODE_W * 0.46, hh = NODE_H * 0.62
    const pts = `${cx},${cy - hh} ${cx + hw},${cy} ${cx},${cy + hh} ${cx - hw},${cy}`
    return (
      <g onClick={() => onClick(node)} style={{ cursor: 'pointer' }}>
        <polygon points={pts} fill={meta.bg} stroke={stroke} strokeWidth={sWidth} />
        <text x={cx} y={cy - 4} textAnchor="middle" fill="#f8fafc" fontSize="11" fontWeight="600"
          style={{ pointerEvents: 'none' }}>{label}</text>
        <text x={cx} y={cy + 12} textAnchor="middle" fill={meta.stroke} fontSize="9"
          style={{ pointerEvents: 'none' }}>{meta.label}</text>
      </g>
    )
  }

  const rx = node.type === 'End Call' ? 26 : 6

  return (
    <g onClick={() => onClick(node)} style={{ cursor: 'pointer' }}>
      <rect x={x} y={y} width={NODE_W} height={NODE_H} rx={rx} ry={rx}
        fill={meta.bg} stroke={stroke} strokeWidth={sWidth} />
      {node.data?.isStart && (
        <circle cx={x + NODE_W - 8} cy={y + 8} r={4} fill="#22c55e" />
      )}
      <text x={x + NODE_W / 2} y={y + 20} textAnchor="middle" fill="#f8fafc" fontSize="12"
        fontWeight="600" style={{ pointerEvents: 'none' }}>{label}</text>
      <text x={x + NODE_W / 2} y={y + 37} textAnchor="middle" fill={meta.stroke} fontSize="10"
        style={{ pointerEvents: 'none' }}>{meta.label}</text>
    </g>
  )
}

// ── node detail panel ──────────────────────────────────────────────────────────

function Tag({ color, children }) {
  return (
    <span style={{
      display: 'inline-block', padding: '2px 8px', borderRadius: 4,
      background: color + '22', border: `1px solid ${color}`,
      color, fontSize: 11, marginRight: 6, marginBottom: 8,
    }}>{children}</span>
  )
}

function Field({ label, value, mono }) {
  return (
    <div style={{ marginBottom: 10 }}>
      <div style={{ color: '#94a3b8', fontSize: 11, marginBottom: 3, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
        {label}
      </div>
      <div style={{
        color: '#e2e8f0', fontSize: 12,
        fontFamily: mono ? 'ui-monospace, monospace' : 'inherit',
        lineHeight: 1.6, whiteSpace: 'pre-wrap',
        maxHeight: 120, overflowY: 'auto',
        background: '#0f172a', padding: '6px 10px', borderRadius: 4,
        border: '1px solid #1e293b',
      }}>{value}</div>
    </div>
  )
}

function NodeDetail({ node, onClose }) {
  const meta = getNodeMeta(node.type)
  const d = node.data || {}

  return (
    <div style={{
      position: 'absolute', bottom: 0, left: 0, right: 0,
      background: '#1e293b', borderTop: `2px solid ${meta.stroke}`,
      padding: '14px 20px', maxHeight: '45%', overflowY: 'auto',
    }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 10 }}>
        <div>
          <span style={{ color: meta.stroke, fontWeight: 700, fontSize: 12 }}>{meta.label}</span>
          <span style={{ color: '#f8fafc', fontWeight: 600, fontSize: 16, marginLeft: 10 }}>
            {d.name || node.id}
          </span>
          <span style={{ color: '#475569', fontSize: 12, marginLeft: 8 }}>#{node.id}</span>
        </div>
        <button onClick={onClose} style={{
          background: 'none', border: 'none', color: '#64748b',
          cursor: 'pointer', fontSize: 18, lineHeight: 1, padding: '0 4px',
        }}>✕</button>
      </div>

      <div style={{ marginBottom: 8 }}>
        {d.isStart && <Tag color="#22c55e">Start</Tag>}
        {d.isGlobal && <Tag color="#a78bfa">Global · {d.globalLabel}</Tag>}
      </div>

      {d.prompt && <Field label="Prompt" value={d.prompt} />}
      {d.text && <Field label="Text" value={d.text} />}
      {d.condition && <Field label="Exit Condition" value={d.condition} />}
      {d.url && <Field label="URL" value={`${d.method || 'POST'} ${d.url}`} mono />}

      {d.extractVars?.length > 0 && (
        <div style={{ marginBottom: 10 }}>
          <div style={{ color: '#94a3b8', fontSize: 11, marginBottom: 4, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
            Extract Variables
          </div>
          {d.extractVars.map((v, i) => {
            const [name, type, desc, req] = Array.isArray(v) ? v : [v.name, v.type, v.description, v.required]
            return (
              <div key={i} style={{ fontSize: 12, color: '#cbd5e1', marginBottom: 3 }}>
                <span style={{ color: '#60a5fa', fontFamily: 'ui-monospace, monospace' }}>{name}</span>
                <span style={{ color: '#475569' }}> {type}</span>
                {req && <span style={{ color: '#f97316' }}> *</span>}
                {desc && <span style={{ color: '#94a3b8' }}> — {desc}</span>}
              </div>
            )
          })}
        </div>
      )}

      {d.routes?.length > 0 && (
        <div style={{ marginBottom: 10 }}>
          <div style={{ color: '#94a3b8', fontSize: 11, marginBottom: 4, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
            Routes
          </div>
          {d.routes.map((r, i) => (
            <div key={i} style={{ fontSize: 12, color: '#cbd5e1', marginBottom: 4 }}>
              <span style={{ color: '#fb923c', fontFamily: 'ui-monospace, monospace' }}>→ {r.targetNodeId}</span>
              {r.conditions?.map((c, j) => (
                <span key={j} style={{ color: '#64748b' }}> [{c.field} {c.operator} {c.value}]</span>
              ))}
            </div>
          ))}
          {d.fallbackNodeId && (
            <div style={{ fontSize: 12, color: '#64748b' }}>
              fallback → <span style={{ color: '#fb923c', fontFamily: 'ui-monospace, monospace' }}>{d.fallbackNodeId}</span>
            </div>
          )}
        </div>
      )}

      {d.tools?.length > 0 && (
        <div style={{ marginBottom: 10 }}>
          <div style={{ color: '#94a3b8', fontSize: 11, marginBottom: 4, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
            Tools ({d.tools.length})
          </div>
          {d.tools.map((t, i) => (
            <div key={i} style={{
              fontSize: 12, color: '#cbd5e1', marginBottom: 6,
              background: '#0f172a', padding: '6px 10px', borderRadius: 4, border: '1px solid #1e293b',
            }}>
              <span style={{ color: '#a78bfa', fontWeight: 600 }}>{t.name}</span>
              <span style={{ color: '#475569' }}> · {t.type}</span>
              {t.description && <div style={{ color: '#94a3b8', marginTop: 2 }}>{t.description}</div>}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

// ── canvas with pan/zoom ───────────────────────────────────────────────────────

function PathwayCanvas({ pathway }) {
  const containerRef = useRef(null)
  const svgRef = useRef(null)
  const vbRef = useRef(null)
  const [viewBox, setViewBoxState] = useState(null)
  const [selected, setSelected] = useState(null)
  const isDragging = useRef(false)
  const lastPt = useRef(null)

  const rawNodes = pathway?.nodes || []
  const edges = pathway?.edges || []
  const nodes = useMemo(() => assignPositions(rawNodes, edges), [rawNodes, edges])
  const nodeMap = useMemo(() => Object.fromEntries(nodes.map(n => [n.id, n])), [nodes])

  const setViewBox = useCallback((vb) => {
    vbRef.current = vb
    setViewBoxState(vb)
  }, [])

  // Reset viewBox when pathway changes
  useEffect(() => {
    if (!nodes.length) return
    const ivb = calcViewBox(nodes)
    setViewBox(ivb)
    setSelected(null)
  }, [nodes, setViewBox])

  // Attach wheel listener with passive:false to allow preventDefault
  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    const handler = (e) => {
      e.preventDefault()
      const svg = svgRef.current
      if (!svg) return
      const rect = svg.getBoundingClientRect()
      const vb = vbRef.current
      if (!vb) return
      const factor = e.deltaY > 0 ? 1.1 : 1 / 1.1
      const mx = vb.x + (e.clientX - rect.left) / rect.width * vb.w
      const my = vb.y + (e.clientY - rect.top) / rect.height * vb.h
      const newW = Math.max(200, Math.min(20000, vb.w * factor))
      const newH = newW * (vb.h / vb.w)
      const newX = mx - (mx - vb.x) * (newW / vb.w)
      const newY = my - (my - vb.y) * (newH / vb.h)
      setViewBox({ x: newX, y: newY, w: newW, h: newH })
    }
    el.addEventListener('wheel', handler, { passive: false })
    return () => el.removeEventListener('wheel', handler)
  }, [setViewBox])

  const onMouseDown = useCallback((e) => {
    if (e.button !== 0) return
    isDragging.current = true
    lastPt.current = { x: e.clientX, y: e.clientY }
    e.preventDefault()
  }, [])

  const onMouseMove = useCallback((e) => {
    if (!isDragging.current) return
    const svg = svgRef.current
    if (!svg) return
    const rect = svg.getBoundingClientRect()
    const vb = vbRef.current
    if (!vb) return
    const dx = (e.clientX - lastPt.current.x) * (vb.w / rect.width)
    const dy = (e.clientY - lastPt.current.y) * (vb.h / rect.height)
    lastPt.current = { x: e.clientX, y: e.clientY }
    setViewBox({ ...vb, x: vb.x - dx, y: vb.y - dy })
  }, [setViewBox])

  const onMouseUp = useCallback(() => { isDragging.current = false }, [])

  if (!pathway) {
    return (
      <div style={{
        flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center',
        color: '#475569', flexDirection: 'column', gap: 12,
      }}>
        <div style={{ fontSize: 32 }}>○</div>
        <div style={{ fontSize: 14 }}>Select a pathway from the sidebar</div>
      </div>
    )
  }

  const vb = viewBox || calcViewBox(nodes)
  const vbStr = `${vb.x} ${vb.y} ${vb.w} ${vb.h}`

  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', overflow: 'hidden', position: 'relative' }}>
      <div
        ref={containerRef}
        style={{ flex: 1, overflow: 'hidden', cursor: isDragging.current ? 'grabbing' : 'grab' }}
        onMouseDown={onMouseDown}
        onMouseMove={onMouseMove}
        onMouseUp={onMouseUp}
        onMouseLeave={onMouseUp}
      >
        <svg
          ref={svgRef}
          width="100%" height="100%"
          viewBox={vbStr}
          style={{ display: 'block', background: '#0f172a' }}
        >
          <defs>
            <marker id="arrowhead" markerWidth="8" markerHeight="6" refX="7" refY="3" orient="auto">
              <polygon points="0 0, 8 3, 0 6" fill="#475569" />
            </marker>
          </defs>
          {edges.map(e => (
            <FlowEdge key={e.id} edge={e} nodeMap={nodeMap} />
          ))}
          {nodes.map(n => (
            <FlowNode key={n.id} node={n}
              isSelected={selected?.id === n.id}
              onClick={setSelected}
            />
          ))}
        </svg>
      </div>

      {selected && <NodeDetail node={selected} onClose={() => setSelected(null)} />}
    </div>
  )
}

// ── sidebar ────────────────────────────────────────────────────────────────────

function Sidebar({ pathways, active, onSelect }) {
  return (
    <div style={{
      width: 220, borderRight: '1px solid #1e293b',
      background: '#080f1a', display: 'flex', flexDirection: 'column', flexShrink: 0,
    }}>
      <div style={{
        padding: '16px 16px 14px',
        borderBottom: '1px solid #1e293b',
        color: '#f8fafc', fontWeight: 700, fontSize: 15, letterSpacing: '0.03em',
      }}>
        Pathwalk
      </div>

      <div style={{ padding: '10px 0', flex: 1, overflowY: 'auto' }}>
        <div style={{
          color: '#475569', fontSize: 10, padding: '0 16px 8px',
          textTransform: 'uppercase', letterSpacing: '0.1em',
        }}>
          Pathways
        </div>

        {pathways.length === 0 && (
          <div style={{ color: '#334155', fontSize: 12, padding: '4px 16px' }}>
            No pathways found
          </div>
        )}

        {pathways.map(name => {
          const isActive = active === name
          return (
            <button
              key={name}
              onClick={() => onSelect(name)}
              style={{
                display: 'block', width: '100%', textAlign: 'left',
                padding: '8px 16px', border: 'none',
                background: isActive ? '#1e293b' : 'transparent',
                color: isActive ? '#93c5fd' : '#94a3b8',
                cursor: 'pointer', fontSize: 13,
                borderLeft: isActive ? '2px solid #3b82f6' : '2px solid transparent',
              }}
            >
              {name}
            </button>
          )
        })}
      </div>

      <div style={{ padding: '10px 16px', borderTop: '1px solid #1e293b' }}>
        <LegendItem color="#3b82f6" label="LLM" />
        <LegendItem color="#f97316" label="Route" />
        <LegendItem color="#8b5cf6" label="Webhook" />
        <LegendItem color="#ef4444" label="Terminal" />
      </div>
    </div>
  )
}

function LegendItem({ color, label }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 5 }}>
      <div style={{ width: 10, height: 10, borderRadius: 2, background: color, flexShrink: 0 }} />
      <span style={{ color: '#64748b', fontSize: 11 }}>{label}</span>
    </div>
  )
}

// ── root app ───────────────────────────────────────────────────────────────────

export default function App() {
  const [pathways, setPathways] = useState([])
  const [active, setActive] = useState(null)
  const [pathway, setPathway] = useState(null)
  const [error, setError] = useState(null)

  useEffect(() => {
    fetch('/api/pathways')
      .then(r => r.json())
      .then(setPathways)
      .catch(() => setError('Could not reach the server'))
  }, [])

  const selectPathway = useCallback((name) => {
    setActive(name)
    setError(null)
    fetch(`/api/pathway?file=${encodeURIComponent(name)}`)
      .then(r => r.json())
      .then(setPathway)
      .catch(() => setError(`Failed to load ${name}`))
  }, [])

  return (
    <div style={{ display: 'flex', height: '100vh', overflow: 'hidden' }}>
      <Sidebar pathways={pathways} active={active} onSelect={selectPathway} />
      <main style={{ flex: 1, overflow: 'hidden', display: 'flex', position: 'relative' }}>
        {error
          ? <div style={{ padding: 24, color: '#ef4444', fontSize: 14 }}>{error}</div>
          : <PathwayCanvas pathway={pathway} />
        }
      </main>
    </div>
  )
}
