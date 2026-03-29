import { useState, useEffect } from 'react'

export default function SettingsModal({ isOpen, onClose, settings, onSave }) {
  const [form, setForm] = useState(settings)

  useEffect(() => {
    setForm(settings)
  }, [isOpen])

  const handleChange = (key, val) => {
    setForm(prev => ({ ...prev, [key]: val }))
  }

  const handleSave = () => {
    onSave(form)
  }

  if (!isOpen) return null

  return (
    <div style={{
      position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.6)',
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      zIndex: 999,
    }} onClick={onClose}>
      <div style={{
        background: '#1e293b', borderRadius: 8, padding: 24,
        width: '90%', maxWidth: 400, border: '1px solid #334155',
        boxShadow: '0 20px 25px -5px rgba(0,0,0,0.5)',
      }} onClick={e => e.stopPropagation()}>
        <div style={{
          display: 'flex', justifyContent: 'space-between',
          alignItems: 'center', marginBottom: 20,
        }}>
          <h2 style={{ margin: 0, color: '#f8fafc', fontSize: 18, fontWeight: 700 }}>
            Settings
          </h2>
          <button onClick={onClose} style={{
            background: 'none', border: 'none', color: '#64748b',
            cursor: 'pointer', fontSize: 20, padding: '0 4px',
          }}>✕</button>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          {/* API Key */}
          <div>
            <label style={{
              display: 'block', color: '#94a3b8', fontSize: 12,
              marginBottom: 6, fontWeight: 600, textTransform: 'uppercase',
              letterSpacing: '0.05em',
            }}>
              OpenAI API Key
            </label>
            <input
              type="password"
              value={form.apiKey || ''}
              onChange={e => handleChange('apiKey', e.target.value)}
              placeholder="sk-..."
              style={{
                width: '100%', padding: '10px 12px', borderRadius: 6,
                background: '#0f172a', border: '1px solid #334155',
                color: '#e2e8f0', fontSize: 13, boxSizing: 'border-box',
                fontFamily: 'inherit',
              }}
            />
            <div style={{ fontSize: 11, color: '#64748b', marginTop: 6 }}>
              Required to run pathways. Stored locally in browser.
            </div>
          </div>

          {/* Model */}
          <div>
            <label style={{
              display: 'block', color: '#94a3b8', fontSize: 12,
              marginBottom: 6, fontWeight: 600, textTransform: 'uppercase',
              letterSpacing: '0.05em',
            }}>
              Model
            </label>
            <input
              type="text"
              value={form.model || ''}
              onChange={e => handleChange('model', e.target.value)}
              placeholder="qwen/qwen3.5-35b-a3b"
              style={{
                width: '100%', padding: '10px 12px', borderRadius: 6,
                background: '#0f172a', border: '1px solid #334155',
                color: '#e2e8f0', fontSize: 13, boxSizing: 'border-box',
                fontFamily: 'inherit',
              }}
            />
          </div>

          {/* Base URL */}
          <div>
            <label style={{
              display: 'block', color: '#94a3b8', fontSize: 12,
              marginBottom: 6, fontWeight: 600, textTransform: 'uppercase',
              letterSpacing: '0.05em',
            }}>
              Base URL (optional)
            </label>
            <input
              type="text"
              value={form.baseUrl || ''}
              onChange={e => handleChange('baseUrl', e.target.value)}
              placeholder="https://api.openai.com/v1"
              style={{
                width: '100%', padding: '10px 12px', borderRadius: 6,
                background: '#0f172a', border: '1px solid #334155',
                color: '#e2e8f0', fontSize: 13, boxSizing: 'border-box',
                fontFamily: 'inherit',
              }}
            />
            <div style={{ fontSize: 11, color: '#64748b', marginTop: 6 }}>
              For OpenAI-compatible APIs (leave blank for default)
            </div>
          </div>
        </div>

        {/* Actions */}
        <div style={{
          display: 'flex', gap: 10, marginTop: 24, justifyContent: 'flex-end',
        }}>
          <button onClick={onClose} style={{
            padding: '10px 16px', borderRadius: 6,
            background: '#334155', border: 'none', color: '#e2e8f0',
            cursor: 'pointer', fontSize: 13, fontWeight: 600,
          }}>
            Close
          </button>
          <button onClick={handleSave} style={{
            padding: '10px 16px', borderRadius: 6,
            background: '#3b82f6', border: 'none', color: '#f8fafc',
            cursor: 'pointer', fontSize: 13, fontWeight: 600,
          }}>
            Save
          </button>
        </div>
      </div>
    </div>
  )
}
