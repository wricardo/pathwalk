import { useCallback, useEffect, useState } from 'react'
import {
  ReactFlow, Background, Controls,
  Handle, Position, MarkerType,
  useNodesState, useEdgesState,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import dagre from '@dagrejs/dagre'

// ── type mapping ──────────────────────────────────────────────────────────────

const TYPE_MAP = {
  'Default':    'llm',
  'End Call':   'terminal',
  'Route':      'route',
  'Webhook':    'webhook',
  'Checkpoint': 'checkpoint',
  'Agent':      'agent',
  'Team':       'team',
}
const TYPE_REVERSE = Object.fromEntries(Object.entries(TYPE_MAP).map(([k, v]) => [v, k]))

const TYPE_COLORS = {
  llm:        { bg: '#1e3a5f', border: '#3b82f6', label: 'LLM' },
  terminal:   { bg: '#3b0f0f', border: '#ef4444', label: 'Terminal' },
  route:      { bg: '#431407', border: '#f97316', label: 'Route' },
  webhook:    { bg: '#2d1b69', border: '#8b5cf6', label: 'Webhook' },
  checkpoint: { bg: '#1a2e1a', border: '#22c55e', label: 'Checkpoint' },
  agent:      { bg: '#1a1a2e', border: '#a78bfa', label: 'Agent' },
  team:       { bg: '#0e2233', border: '#38bdf8', label: 'Team' },
}

const NODE_W = 210
const NODE_H = 64

// ── dagre layout ──────────────────────────────────────────────────────────────

function applyLayout(nodes, edges) {
  const g = new dagre.graphlib.Graph()
  g.setDefaultEdgeLabel(() => ({}))
  g.setGraph({ rankdir: 'TB', nodesep: 60, ranksep: 100 })
  nodes.forEach(n => g.setNode(n.id, { width: NODE_W, height: NODE_H }))
  edges.forEach(e => g.setEdge(e.source, e.target))
  dagre.layout(g)
  return nodes.map(n => {
    const { x, y } = g.node(n.id)
    return { ...n, position: { x: x - NODE_W / 2, y: y - NODE_H / 2 } }
  })
}

// ── pathway ↔ React Flow conversion ──────────────────────────────────────────

function pathwayToFlow(pathway) {
  const rfNodes = (pathway.nodes || []).map(n => ({
    id: n.id,
    type: TYPE_MAP[n.type] || 'llm',
    position: { x: 0, y: 0 },
    data: { ...n.data, _rawType: n.type },
  }))
  const rfEdges = (pathway.edges || []).map(e => ({
    id: e.id,
    source: e.source,
    target: e.target,
    label: e.data?.label || '',
    data: e.data || {},
    markerEnd: { type: MarkerType.ArrowClosed, color: '#475569' },
    style: { stroke: '#334155', strokeWidth: 1.5 },
    labelStyle: { fill: '#64748b', fontSize: 10 },
    labelBgStyle: { fill: '#0f172a' },
  }))
  return { nodes: applyLayout(rfNodes, rfEdges), edges: rfEdges }
}

function flowToPathway(rfNodes, rfEdges, meta) {
  return {
    ...meta,
    nodes: rfNodes.map(n => {
      const { _rawType, ...data } = n.data
      return { id: n.id, type: _rawType || TYPE_REVERSE[n.type] || 'Default', data }
    }),
    edges: rfEdges.map(e => ({
      id: e.id,
      source: e.source,
      target: e.target,
      data: { ...(e.data || {}), label: e.label || '' },
    })),
  }
}

// ── custom node components ────────────────────────────────────────────────────

function BaseNode({ rfType, data, selected }) {
  const c = TYPE_COLORS[rfType] || { bg: '#1e293b', border: '#64748b', label: rfType }
  const name = data.name || '(unnamed)'
  const short = name.length > 26 ? name.slice(0, 24) + '…' : name
  const model = data.modelOptions?.model
  const provider = data.modelOptions?.provider
  const sub = model || provider

  return (
    <div style={{
      width: NODE_W, height: NODE_H,
      background: c.bg,
      border: `${selected ? 2.5 : 1.5}px solid ${selected ? '#f8fafc' : c.border}`,
      borderRadius: rfType === 'terminal' ? 32 : 6,
      display: 'flex', flexDirection: 'column',
      alignItems: 'center', justifyContent: 'center',
      padding: '0 12px', boxSizing: 'border-box', position: 'relative',
    }}>
      <Handle type="target" position={Position.Top} style={{ background: c.border, width: 8, height: 8 }} />
      {data.isStart && (
        <div style={{
          position: 'absolute', top: 5, right: 7,
          width: 7, height: 7, borderRadius: '50%', background: '#22c55e',
        }} />
      )}
      <div style={{ color: '#f1f5f9', fontSize: 12, fontWeight: 600, textAlign: 'center', lineHeight: 1.3 }}>
        {short}
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 5, marginTop: 3 }}>
        <span style={{ color: c.border, fontSize: 9, textTransform: 'uppercase', letterSpacing: '0.08em' }}>
          {c.label}
        </span>
        {sub && (
          <span style={{ color: '#475569', fontSize: 9, fontFamily: 'ui-monospace, monospace' }}>
            · {sub.length > 18 ? sub.slice(0, 16) + '…' : sub}
          </span>
        )}
      </div>
      <Handle type="source" position={Position.Bottom} style={{ background: c.border, width: 8, height: 8 }} />
    </div>
  )
}

const LLMNode        = (p) => <BaseNode rfType="llm"        {...p} />
const TerminalNode   = (p) => <BaseNode rfType="terminal"   {...p} />
const WebhookNode    = (p) => <BaseNode rfType="webhook"    {...p} />
const CheckpointNode = (p) => <BaseNode rfType="checkpoint" {...p} />
const AgentNode      = (p) => <BaseNode rfType="agent"      {...p} />
const TeamNode       = (p) => <BaseNode rfType="team"       {...p} />

function RouteNode({ data, selected }) {
  const c = TYPE_COLORS.route
  const name = data.name || '(unnamed)'
  const short = name.length > 20 ? name.slice(0, 18) + '…' : name
  return (
    <div style={{ width: NODE_W, height: NODE_H, position: 'relative', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
      <Handle type="target" position={Position.Top} style={{ background: c.border, width: 8, height: 8, top: 2 }} />
      <div style={{
        position: 'absolute',
        width: NODE_W * 0.88, height: NODE_H * 0.88,
        background: c.bg,
        border: `${selected ? 2.5 : 1.5}px solid ${selected ? '#f8fafc' : c.border}`,
        transform: 'rotate(45deg)',
      }} />
      <div style={{ position: 'relative', zIndex: 1, textAlign: 'center' }}>
        <div style={{ color: '#f1f5f9', fontSize: 11, fontWeight: 600 }}>{short}</div>
        <div style={{ color: c.border, fontSize: 9, textTransform: 'uppercase', letterSpacing: '0.08em' }}>Route</div>
      </div>
      <Handle type="source" position={Position.Bottom} style={{ background: c.border, width: 8, height: 8, bottom: 2 }} />
    </div>
  )
}

const nodeTypes = { llm: LLMNode, terminal: TerminalNode, route: RouteNode, webhook: WebhookNode, checkpoint: CheckpointNode, agent: AgentNode, team: TeamNode }

// ── node edit panel ───────────────────────────────────────────────────────────

function NodePanel({ node, onChange, onClose }) {
  const c = TYPE_COLORS[node.type] || { border: '#64748b', label: node.type }
  const d = node.data

  const set = (key, val) => onChange({ ...node, data: { ...d, [key]: val } })
  const setMO = (key, val) => onChange({ ...node, data: { ...d, modelOptions: { ...(d.modelOptions || {}), [key]: val || undefined } } })

  const hasModelOptions = ['llm', 'checkpoint', 'agent', 'team', 'webhook'].includes(node.type)

  return (
    <div style={{ width: 300, borderLeft: '1px solid #1e293b', background: '#080f1a', display: 'flex', flexDirection: 'column', overflow: 'hidden', flexShrink: 0 }}>
      <div style={{ padding: '12px 16px', borderBottom: `2px solid ${c.border}`, display: 'flex', justifyContent: 'space-between', alignItems: 'center', background: '#0a1220' }}>
        <div>
          <span style={{ color: c.border, fontSize: 11, fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.06em' }}>{c.label}</span>
          <span style={{ color: '#475569', fontSize: 11, marginLeft: 8 }}>#{node.id}</span>
        </div>
        <button onClick={onClose} style={{ background: 'none', border: 'none', color: '#475569', cursor: 'pointer', fontSize: 16 }}>✕</button>
      </div>

      <div style={{ flex: 1, overflowY: 'auto', padding: '14px 16px' }}>
        <PF label="Name">
          <PI value={d.name || ''} onChange={v => set('name', v)} />
        </PF>

        {hasModelOptions && (
          <div style={{ border: '1px solid #1e293b', borderRadius: 6, padding: '10px 12px', marginBottom: 14, background: '#0a1628' }}>
            <div style={{ color: '#3b82f6', fontSize: 10, fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.08em', marginBottom: 10 }}>
              Model Options
            </div>
            <PF label="Model">
              <PI value={d.modelOptions?.model || ''} onChange={v => setMO('model', v)} placeholder="gpt-4o-mini, claude-sonnet-4-6…" mono />
            </PF>
            <PF label="Provider">
              <PI value={d.modelOptions?.provider || ''} onChange={v => setMO('provider', v)} placeholder="venu, openai, anthropic…" mono />
            </PF>
            <PF label="Temperature">
              <PI
                type="number"
                value={d.modelOptions?.newTemperature ?? ''}
                onChange={v => setMO('newTemperature', v === '' ? undefined : parseFloat(v))}
                placeholder="0–2 (blank = default)"
              />
            </PF>
          </div>
        )}

        {node.type === 'llm' && <>
          <PF label="Prompt"><PT value={d.prompt || ''} onChange={v => set('prompt', v)} rows={6} /></PF>
          <PF label="Exit Condition"><PT value={d.condition || ''} onChange={v => set('condition', v)} rows={2} /></PF>
        </>}

        {node.type === 'terminal' && (
          <PF label="Text"><PT value={d.text || ''} onChange={v => set('text', v)} rows={3} /></PF>
        )}

        {node.type === 'webhook' && <>
          <PF label="URL"><PI value={d.url || ''} onChange={v => set('url', v)} mono /></PF>
          <PF label="Method">
            <select value={d.method || 'POST'} onChange={e => set('method', e.target.value)} style={selStyle}>
              {['GET', 'POST', 'PUT', 'PATCH', 'DELETE'].map(m => <option key={m}>{m}</option>)}
            </select>
          </PF>
        </>}

        {node.type === 'route' && (
          <PF label="Fallback Node ID"><PI value={d.fallbackNodeId || ''} onChange={v => set('fallbackNodeId', v)} mono /></PF>
        )}

        {node.type === 'checkpoint' && <>
          <PF label="Mode">
            <select value={d.checkpointMode || 'human_input'} onChange={e => set('checkpointMode', e.target.value)} style={selStyle}>
              {['human_input', 'human_approval', 'llm_eval', 'auto', 'wait'].map(m => <option key={m}>{m}</option>)}
            </select>
          </PF>
          <PF label="Prompt"><PT value={d.checkpointPrompt || ''} onChange={v => set('checkpointPrompt', v)} rows={3} /></PF>
          {d.checkpointMode === 'llm_eval' && (
            <PF label="Criteria"><PT value={d.checkpointCriteria || ''} onChange={v => set('checkpointCriteria', v)} rows={3} /></PF>
          )}
        </>}

        {d.extractVars?.length > 0 && (
          <div style={{ marginTop: 6 }}>
            <div style={subLabelStyle}>Extract Variables ({d.extractVars.length})</div>
            {d.extractVars.map((v, i) => {
              const [name, type, , req] = Array.isArray(v) ? v : [v.name, v.type, v.description, v.required]
              return (
                <div key={i} style={{ fontSize: 11, color: '#94a3b8', marginBottom: 3 }}>
                  <span style={{ color: '#60a5fa', fontFamily: 'monospace' }}>{name}</span>
                  <span style={{ color: '#475569' }}> {type}</span>
                  {req && <span style={{ color: '#f97316' }}> *</span>}
                </div>
              )
            })}
          </div>
        )}

        <div style={{ marginTop: 14, display: 'flex', gap: 6, flexWrap: 'wrap' }}>
          <Flag label="Start" value={!!d.isStart} color="#22c55e" onChange={v => set('isStart', v)} />
          <Flag label="Global" value={!!d.isGlobal} color="#a78bfa" onChange={v => set('isGlobal', v)} />
        </div>
      </div>
    </div>
  )
}

// ── small form helpers ────────────────────────────────────────────────────────

const subLabelStyle = { color: '#475569', fontSize: 10, fontWeight: 700, textTransform: 'uppercase', letterSpacing: '0.06em', marginBottom: 6 }
const baseInput = { width: '100%', padding: '7px 10px', borderRadius: 4, background: '#1e293b', border: '1px solid #334155', color: '#e2e8f0', fontSize: 12, boxSizing: 'border-box' }
const selStyle = { ...baseInput, cursor: 'pointer', fontFamily: 'inherit' }

function PF({ label, children }) {
  return (
    <div style={{ marginBottom: 12 }}>
      <div style={subLabelStyle}>{label}</div>
      {children}
    </div>
  )
}

function PI({ value, onChange, placeholder, mono, type = 'text' }) {
  return (
    <input type={type} value={value ?? ''} onChange={e => onChange(e.target.value)} placeholder={placeholder}
      style={{ ...baseInput, fontFamily: mono ? 'ui-monospace, monospace' : 'inherit' }} />
  )
}

function PT({ value, onChange, rows = 4 }) {
  return (
    <textarea value={value ?? ''} onChange={e => onChange(e.target.value)} rows={rows}
      style={{ ...baseInput, resize: 'vertical', fontFamily: 'inherit', lineHeight: 1.5 }} />
  )
}

function Flag({ label, value, color, onChange }) {
  return (
    <button onClick={() => onChange(!value)} style={{
      padding: '3px 10px', borderRadius: 4, fontSize: 11, cursor: 'pointer', fontWeight: 600,
      border: `1px solid ${value ? color : '#334155'}`,
      background: value ? color + '22' : 'transparent',
      color: value ? color : '#475569',
    }}>{label}</button>
  )
}

// ── providers modal ───────────────────────────────────────────────────────────

function ProvidersModal({ providers, onChange, onClose }) {
  const [list, setList] = useState(() => providers.map(p => ({ ...p })))

  const upd = (i, key, val) => setList(l => l.map((p, j) => j === i ? { ...p, [key]: val } : p))
  const updModels = (i, val) => upd(i, 'models', val.split(',').map(s => s.trim()).filter(Boolean))

  const save = () => {
    onChange(list.filter(p => p.name?.trim()))
    onClose()
  }

  return (
    <div style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.75)', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', zIndex: 50, paddingTop: 48, overflowY: 'auto' }}
      onClick={onClose}>
      <div style={{ background: '#1e293b', borderRadius: 8, padding: 24, width: '90%', maxWidth: 640, border: '1px solid #334155', marginBottom: 48 }}
        onClick={e => e.stopPropagation()}>

        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 16 }}>
          <h2 style={{ margin: 0, color: '#f8fafc', fontSize: 18, fontWeight: 700 }}>Providers</h2>
          <button onClick={onClose} style={{ background: 'none', border: 'none', color: '#64748b', cursor: 'pointer', fontSize: 20 }}>✕</button>
        </div>

        <p style={{ color: '#64748b', fontSize: 12, marginTop: 0, marginBottom: 16, lineHeight: 1.6 }}>
          LLM providers for this pathway. <code style={{ background: '#0f172a', padding: '1px 5px', borderRadius: 3, color: '#93c5fd' }}>{'${ENV_VAR}'}</code> is expanded at runtime.
          Routing: explicit <code style={{ background: '#0f172a', padding: '1px 5px', borderRadius: 3, color: '#93c5fd' }}>provider</code> on node →
          model prefix match → <code style={{ background: '#0f172a', padding: '1px 5px', borderRadius: 3, color: '#93c5fd' }}>*</code> catch-all.
        </p>

        {list.map((p, i) => (
          <div key={i} style={{ background: '#0f172a', border: '1px solid #334155', borderRadius: 6, padding: '14px 16px', marginBottom: 10 }}>
            <div style={{ display: 'flex', gap: 8, marginBottom: 10, alignItems: 'flex-end' }}>
              <div style={{ flex: 1 }}>
                <div style={subLabelStyle}>Name</div>
                <input value={p.name || ''} onChange={e => upd(i, 'name', e.target.value)}
                  placeholder="venu, openai, anthropic…"
                  style={{ ...baseInput, fontFamily: 'ui-monospace, monospace' }} />
              </div>
              <div style={{ width: 120 }}>
                <div style={subLabelStyle}>Type</div>
                <select value={p.type || 'openai'} onChange={e => upd(i, 'type', e.target.value)}
                  style={{ ...baseInput, cursor: 'pointer', fontFamily: 'inherit' }}>
                  <option value="openai">openai</option>
                  <option value="anthropic">anthropic</option>
                </select>
              </div>
              <button onClick={() => setList(l => l.filter((_, j) => j !== i))}
                style={{ padding: '7px 12px', background: '#3b0f0f', border: '1px solid #7f1d1d', borderRadius: 4, color: '#fca5a5', cursor: 'pointer', fontSize: 12 }}>
                Remove
              </button>
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
              <div>
                <div style={subLabelStyle}>API Key</div>
                <input value={p.apiKey || ''} onChange={e => upd(i, 'apiKey', e.target.value)}
                  placeholder="${OPENAI_API_KEY}"
                  style={{ ...baseInput, fontFamily: 'ui-monospace, monospace' }} />
              </div>
              <div>
                <div style={subLabelStyle}>Base URL</div>
                <input value={p.baseURL || ''} onChange={e => upd(i, 'baseURL', e.target.value)}
                  placeholder="${LLM_BASE_URL}"
                  style={{ ...baseInput, fontFamily: 'ui-monospace, monospace' }} />
              </div>
              <div>
                <div style={subLabelStyle}>Default Model</div>
                <input value={p.defaultModel || ''} onChange={e => upd(i, 'defaultModel', e.target.value)}
                  placeholder="gpt-4o-mini"
                  style={{ ...baseInput, fontFamily: 'ui-monospace, monospace' }} />
              </div>
              <div>
                <div style={subLabelStyle}>Models (comma-separated)</div>
                <input value={(p.models || []).join(', ')} onChange={e => updModels(i, e.target.value)}
                  placeholder="gpt-4o, claude-, *"
                  style={{ ...baseInput, fontFamily: 'ui-monospace, monospace' }} />
              </div>
            </div>
          </div>
        ))}

        <button onClick={() => setList(l => [...l, { name: '', type: 'openai', baseURL: '', apiKey: '', defaultModel: '', models: ['*'] }])}
          style={{ width: '100%', padding: 10, border: '1px dashed #334155', borderRadius: 6, background: 'transparent', color: '#64748b', cursor: 'pointer', fontSize: 13, marginBottom: 16 }}>
          + Add Provider
        </button>

        <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end' }}>
          <button onClick={onClose} style={{ padding: '10px 16px', borderRadius: 6, background: '#334155', border: 'none', color: '#e2e8f0', cursor: 'pointer', fontSize: 13, fontWeight: 600 }}>Cancel</button>
          <button onClick={save} style={{ padding: '10px 16px', borderRadius: 6, background: '#3b82f6', border: 'none', color: '#f8fafc', cursor: 'pointer', fontSize: 13, fontWeight: 600 }}>Save</button>
        </div>
      </div>
    </div>
  )
}

// ── main canvas ───────────────────────────────────────────────────────────────

export default function FlowCanvas({ pathway, onSave }) {
  const [nodes, setNodes, onNodesChange] = useNodesState([])
  const [edges, , onEdgesChange] = useEdgesState([])
  const [selectedNode, setSelectedNode] = useState(null)
  const [showProviders, setShowProviders] = useState(false)
  const [meta, setMeta] = useState({})
  const [dirty, setDirty] = useState(false)
  const [saved, setSaved] = useState(false)
  const [saveError, setSaveError] = useState(null)
  // hold a stable ref to edges for serialization without adding to save deps
  const edgesRef = useState(() => ({ current: [] }))[0]

  useEffect(() => { edgesRef.current = edges }, [edges, edgesRef])

  useEffect(() => {
    if (!pathway?.data) return
    const { nodes: _n, edges: _e, ...rest } = pathway.data
    const { nodes: rfNodes, edges: rfEdges } = pathwayToFlow(pathway.data)
    setNodes(rfNodes)
    edgesRef.current = rfEdges
    // reset edges via onEdgesChange replace
    onEdgesChange(rfEdges.map(e => ({ type: 'add', item: e })))
    setMeta(rest)
    setSelectedNode(null)
    setDirty(false)
    setSaved(false)
  }, [pathway?.fileName]) // eslint-disable-line

  const markDirty = useCallback(() => { setDirty(true); setSaved(false) }, [])

  const handleNodeClick = useCallback((_, node) => setSelectedNode(node), [])

  const handleNodeChange = useCallback((updated) => {
    setNodes(ns => ns.map(n => n.id === updated.id ? updated : n))
    setSelectedNode(prev => prev?.id === updated.id ? updated : prev)
    markDirty()
  }, [setNodes, markDirty])

  const handleProvidersChange = useCallback((providers) => {
    setMeta(m => ({ ...m, providers: providers.length ? providers : undefined }))
    markDirty()
  }, [markDirty])

  const handleSave = useCallback(async () => {
    if (!pathway?.fileName) return
    const pathwayJSON = flowToPathway(nodes, edgesRef.current, meta)
    const content = JSON.stringify(pathwayJSON, null, 2)
    try {
      const res = await fetch('/api/pathway', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ file: pathway.fileName, content }),
      })
      const data = await res.json()
      if (data.ok) {
        setDirty(false)
        setSaved(true)
        setTimeout(() => setSaved(false), 2500)
        onSave?.(content)
      }
    } catch (_) {}
  }, [pathway?.fileName, nodes, meta, edgesRef, onSave])

  const providerCount = meta.providers?.length || 0

  return (
    <div style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>
      <div style={{ flex: 1, position: 'relative' }}>
        {/* Toolbar overlay */}
        <div style={{ position: 'absolute', top: 12, left: 12, zIndex: 10, display: 'flex', gap: 8 }}>
          <button
            onClick={() => setShowProviders(true)}
            style={{
              padding: '6px 14px', borderRadius: 6, fontSize: 12, cursor: 'pointer', fontWeight: 600,
              background: providerCount > 0 ? '#0f2140' : '#1e293b',
              border: `1px solid ${providerCount > 0 ? '#3b82f6' : '#334155'}`,
              color: providerCount > 0 ? '#93c5fd' : '#64748b',
            }}
          >
            {providerCount > 0 ? `⚡ ${providerCount} Provider${providerCount !== 1 ? 's' : ''}` : '+ Providers'}
          </button>

          {dirty && (
            <button onClick={handleSave} style={{
              padding: '6px 14px', borderRadius: 6, fontSize: 12, cursor: 'pointer', fontWeight: 600,
              background: saved ? '#15803d' : '#16a34a', border: 'none', color: '#f0fdf4',
            }}>
              {saved ? '✓ Saved' : 'Save'}
            </button>
          )}
        </div>

        <ReactFlow
          nodes={nodes}
          edges={edgesRef.current}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onNodeClick={handleNodeClick}
          onPaneClick={() => setSelectedNode(null)}
          nodeTypes={nodeTypes}
          fitView
          fitViewOptions={{ padding: 0.25 }}
          style={{ background: '#0f172a' }}
          deleteKeyCode={null}
        >
          <Background color="#1a2744" gap={24} size={1} />
          <Controls style={{ background: '#1e293b', border: '1px solid #334155', borderRadius: 6 }} />
        </ReactFlow>
      </div>

      {selectedNode && (
        <NodePanel node={selectedNode} onChange={handleNodeChange} onClose={() => setSelectedNode(null)} />
      )}

      {showProviders && (
        <ProvidersModal
          providers={meta.providers || []}
          onChange={handleProvidersChange}
          onClose={() => setShowProviders(false)}
        />
      )}
    </div>
  )
}
