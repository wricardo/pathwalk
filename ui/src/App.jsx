import { useState, useEffect, useCallback } from 'react'
import Chat from './Chat'
import Editor from './Editor'
import FlowCanvas from './FlowCanvas'
import SettingsModal from './SettingsModal'

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
    </div>
  )
}

// ── root app ───────────────────────────────────────────────────────────────────

export default function App() {
  const [pathways, setPathways] = useState([])
  const [active, setActive] = useState(null)
  const [pathway, setPathway] = useState(null)
  const [error, setError] = useState(null)
  const [tab, setTab] = useState('view')
  const [editorKey, setEditorKey] = useState(0)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [settings, setSettings] = useState(() => {
    const saved = localStorage.getItem('pathwalk_settings')
    return saved ? JSON.parse(saved) : { apiKey: '', model: 'qwen/qwen3.5-35b-a3b', baseUrl: '' }
  })

  useEffect(() => {
    fetch('/api/pathways')
      .then(r => r.json())
      .then(setPathways)
      .catch(() => setError('Could not reach the server'))
  }, [])

  const selectPathway = useCallback((name) => {
    setActive(name)
    setTab('view')
    setError(null)
    fetch(`/api/pathway?file=${encodeURIComponent(name)}`)
      .then(r => r.json())
      .then(pathwayData => {
        setPathway({
          fileName: name,
          content: JSON.stringify(pathwayData),
          data: pathwayData,
        })
      })
      .catch(() => setError(`Failed to load ${name}`))
  }, [])

  // Called by FlowCanvas or Editor after a successful save
  const handleSaved = useCallback((newContent) => {
    const pathwayData = JSON.parse(newContent)
    setPathway(prev => ({
      ...prev,
      content: newContent,
      data: pathwayData,
    }))
    // Force Editor to re-initialize from the new content
    setEditorKey(k => k + 1)
  }, [])

  const handleSaveSettings = (newSettings) => {
    setSettings(newSettings)
    localStorage.setItem('pathwalk_settings', JSON.stringify(newSettings))
    setSettingsOpen(false)
  }

  const TabButton = ({ id, label, emoji }) => {
    const isActive = tab === id
    return (
      <button
        onClick={() => setTab(id)}
        style={{
          padding: '8px 16px', border: 'none',
          background: isActive ? '#1e293b' : 'transparent',
          color: isActive ? '#93c5fd' : '#64748b',
          cursor: 'pointer', fontSize: 13, fontWeight: 600,
          borderBottom: isActive ? '2px solid #3b82f6' : '2px solid transparent',
          marginRight: 8,
        }}
      >
        {emoji} {label}
      </button>
    )
  }

  return (
    <div style={{ display: 'flex', height: '100vh', overflow: 'hidden' }}>
      <Sidebar pathways={pathways} active={active} onSelect={selectPathway} />
      <main style={{ flex: 1, overflow: 'hidden', display: 'flex', flexDirection: 'column' }}>
        {active && (
          <>
            {/* Tab bar */}
            <div style={{
              display: 'flex', alignItems: 'center',
              background: '#0f172a', borderBottom: '1px solid #1e293b',
              padding: '0 16px', height: 48,
            }}>
              <div style={{ display: 'flex', alignItems: 'center', flex: 1 }}>
                <TabButton id="view" label="Flow" emoji="▶" />
                <TabButton id="chat" label="Chat" emoji="💬" />
                <TabButton id="edit" label="Edit" emoji="✎" />
              </div>
              <button
                onClick={() => setSettingsOpen(true)}
                style={{
                  background: 'none', border: 'none', color: '#64748b',
                  cursor: 'pointer', fontSize: 18, padding: '4px 8px',
                }}
                title="Settings"
              >
                ⚙
              </button>
            </div>

            {/* Tab content */}
            {error && (
              <div style={{ padding: 16, color: '#ef4444', fontSize: 14 }}>
                {error}
              </div>
            )}

            {tab === 'view' && (
              <FlowCanvas pathway={pathway} onSave={handleSaved} />
            )}

            {tab === 'chat' && (
              <Chat pathway={pathway} settings={settings} />
            )}

            {tab === 'edit' && (
              <Editor key={editorKey} pathway={pathway} onSave={handleSaved} />
            )}
          </>
        )}

        {!active && (
          <div style={{
            flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center',
            color: '#475569', fontSize: 14,
          }}>
            Select a pathway from the sidebar
          </div>
        )}
      </main>

      <SettingsModal
        isOpen={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        settings={settings}
        onSave={handleSaveSettings}
      />
    </div>
  )
}
