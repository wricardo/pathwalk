import { useState, useEffect } from 'react'

export default function Editor({ pathway, onSave }) {
  const [content, setContent] = useState('')
  const [dirty, setDirty] = useState(false)
  const [error, setError] = useState(null)
  const [saved, setSaved] = useState(false)

  // Load pathway JSON
  useEffect(() => {
    if (pathway?.content) {
      setContent(JSON.stringify(JSON.parse(pathway.content), null, 2))
      setDirty(false)
      setSaved(false)
    }
  }, [pathway?.fileName])

  const handleChange = (e) => {
    const val = e.target.value
    setContent(val)
    setDirty(true)
    setSaved(false)

    // Validate JSON
    try {
      JSON.parse(val)
      setError(null)
    } catch (e) {
      setError(e.message)
    }
  }

  const handleFormat = () => {
    try {
      const obj = JSON.parse(content)
      const formatted = JSON.stringify(obj, null, 2)
      setContent(formatted)
      setError(null)
    } catch (e) {
      setError('Cannot format invalid JSON: ' + e.message)
    }
  }

  const handleSave = async () => {
    if (!pathway?.fileName || error) return

    try {
      const res = await fetch('/api/pathway', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          file: pathway.fileName,
          content,
        }),
      })

      const data = await res.json()
      if (data.ok) {
        setDirty(false)
        setSaved(true)
        setTimeout(() => setSaved(false), 2000)
      } else {
        setError('Save failed: ' + (data.error || 'unknown error'))
      }
    } catch (err) {
      setError('Network error: ' + err.message)
    }
  }

  return (
    <div style={{
      flex: 1, display: 'flex', flexDirection: 'column',
      background: '#0f172a', padding: 16,
    }}>
      {/* Toolbar */}
      <div style={{
        display: 'flex', gap: 8, marginBottom: 12, alignItems: 'center',
      }}>
        <button
          onClick={handleFormat}
          disabled={error}
          style={{
            padding: '8px 14px', borderRadius: 4,
            background: error ? '#334155' : '#1e293b',
            border: '1px solid #334155', color: '#e2e8f0',
            cursor: error ? 'default' : 'pointer', fontSize: 12,
          }}
        >
          Format
        </button>

        <button
          onClick={handleSave}
          disabled={!dirty || error || !pathway?.fileName}
          style={{
            padding: '8px 14px', borderRadius: 4,
            background: dirty && !error ? '#16a34a' : '#334155',
            border: 'none', color: '#f8fafc',
            cursor: dirty && !error ? 'pointer' : 'default',
            fontSize: 12, fontWeight: 600,
          }}
        >
          {saved ? '✓ Saved' : dirty ? 'Save' : 'Saved'}
        </button>

        {error && (
          <div style={{
            flex: 1, padding: '6px 12px', background: '#3b0f0f',
            borderRadius: 4, color: '#fca5a5', fontSize: 11,
          }}>
            {error}
          </div>
        )}
      </div>

      {/* Editor */}
      <textarea
        value={content}
        onChange={handleChange}
        style={{
          flex: 1, padding: 12, borderRadius: 4,
          background: '#1e293b', border: '1px solid #334155',
          color: '#e2e8f0', fontSize: 12,
          fontFamily: 'ui-monospace, Menlo, Monaco, monospace',
          lineHeight: 1.5, resize: 'none',
        }}
        spellCheck="false"
      />
    </div>
  )
}
