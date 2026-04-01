import { useState } from 'react'

export default function Chat({ pathway, settings }) {
  const [messages, setMessages] = useState([])
  const [task, setTask] = useState('')
  const [running, setRunning] = useState(false)

  const runPathway = async () => {
    if (!task.trim() || !settings.apiKey) return

    setRunning(true)
    setMessages([
      { role: 'user', content: task },
      { role: 'status', content: 'Running pathway...' },
    ])

    try {
      const response = await fetch('/api/run', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          file: pathway.fileName,
          task,
          model: settings.model,
          api_key: settings.apiKey,
          base_url: settings.baseUrl,
          max_steps: 50,
        }),
      })

      const data = await response.json()

      if (data.error) {
        setMessages(prev => [
          ...prev.slice(0, -1),
          { role: 'error', content: `Error: ${data.error}` },
        ])
      } else {
        const newMessages = [{ role: 'user', content: task }]

        // Add a message per step
        for (const step of data.steps || []) {
          newMessages.push({
            role: 'step',
            content: step.output || '(no output)',
            nodeId: step.nodeId,
            nodeName: step.nodeName,
            toolCalls: step.toolCalls,
          })
        }

        // Add final output
        newMessages.push({
          role: 'assistant',
          content: data.output || '(pathway completed)',
          variables: data.variables,
          reason: data.reason,
        })

        setMessages(newMessages)
      }
    } catch (err) {
      setMessages(prev => [
        ...prev.slice(0, -1),
        { role: 'error', content: `Network error: ${err.message}` },
      ])
    } finally {
      setRunning(false)
    }
  }

  return (
    <div style={{
      flex: 1, display: 'flex', flexDirection: 'column',
      background: '#0f172a', padding: 16,
    }}>
      {/* Messages area */}
      <div style={{
        flex: 1, overflowY: 'auto', marginBottom: 16, display: 'flex',
        flexDirection: 'column', gap: 12,
      }}>
        {messages.length === 0 ? (
          <div style={{
            flex: 1, display: 'flex', alignItems: 'center', justifyContent: 'center',
            color: '#475569', fontSize: 14,
          }}>
            Enter a task and click Run to execute the pathway
          </div>
        ) : (
          messages.map((msg, i) => (
            <Message key={i} message={msg} />
          ))
        )}
      </div>

      {/* Input area */}
      <div style={{ display: 'flex', gap: 8 }}>
        <input
          type="text"
          value={task}
          onChange={e => setTask(e.target.value)}
          onKeyDown={e => {
            if (e.key === 'Enter' && !running) runPathway()
          }}
          placeholder="Enter task..."
          disabled={running || !settings.apiKey}
          style={{
            flex: 1, padding: '10px 12px', borderRadius: 6,
            background: '#1e293b', border: '1px solid #334155',
            color: '#e2e8f0', fontSize: 14,
            fontFamily: 'inherit',
          }}
        />
        <button
          onClick={runPathway}
          disabled={running || !task.trim() || !settings.apiKey}
          style={{
            padding: '10px 20px', borderRadius: 6,
            background: running || !settings.apiKey ? '#334155' : '#3b82f6',
            border: 'none', color: '#f8fafc', cursor: 'pointer',
            fontSize: 14, fontWeight: 600,
          }}
        >
          {running ? 'Running...' : 'Run'}
        </button>
      </div>

      {!settings.apiKey && (
        <div style={{
          marginTop: 10, padding: 10, background: '#3b0f0f', borderRadius: 4,
          color: '#fca5a5', fontSize: 12,
        }}>
          ⚠ Set API key in settings to run
        </div>
      )}
    </div>
  )
}

function Message({ message }) {
  const { role, content, nodeId, nodeName, toolCalls, variables, reason } = message

  if (role === 'user') {
    return (
      <div style={{
        alignSelf: 'flex-end', maxWidth: '70%',
        background: '#1e40af', padding: '10px 14px', borderRadius: 8,
        color: '#f8fafc', fontSize: 13, lineHeight: 1.5,
      }}>
        {content}
      </div>
    )
  }

  if (role === 'status') {
    return (
      <div style={{
        alignSelf: 'flex-start', maxWidth: '70%',
        background: '#1e293b', padding: '10px 14px', borderRadius: 8,
        color: '#94a3b8', fontSize: 13, fontStyle: 'italic',
      }}>
        {content}
      </div>
    )
  }

  if (role === 'error') {
    return (
      <div style={{
        alignSelf: 'flex-start', maxWidth: '100%',
        background: '#3b0f0f', padding: '10px 14px', borderRadius: 8,
        color: '#fca5a5', fontSize: 13,
      }}>
        {content}
      </div>
    )
  }

  if (role === 'step') {
    return (
      <div style={{
        alignSelf: 'flex-start', maxWidth: '100%',
        background: '#1e293b', padding: '12px 14px', borderRadius: 8,
        borderLeft: '3px solid #3b82f6',
        color: '#cbd5e1', fontSize: 13,
      }}>
        <div style={{ color: '#60a5fa', fontWeight: 600, marginBottom: 4 }}>
          [{nodeName}]
        </div>
        <div style={{ whiteSpace: 'pre-wrap', lineHeight: 1.5 }}>
          {content}
        </div>
        {toolCalls?.length > 0 && (
          <div style={{ marginTop: 8, fontSize: 11, color: '#94a3b8' }}>
            <div style={{ fontWeight: 600, marginBottom: 4 }}>Tools called:</div>
            {toolCalls.map((tc, i) => (
              <div key={i} style={{ marginBottom: 4, paddingLeft: 8 }}>
                <span style={{ color: '#a78bfa' }}>{tc.name}</span>
                {tc.error && <span style={{ color: '#f87171' }}> — Error: {tc.error}</span>}
              </div>
            ))}
          </div>
        )}
      </div>
    )
  }

  if (role === 'assistant') {
    return (
      <div style={{
        alignSelf: 'flex-start', maxWidth: '100%',
        background: '#1e293b', padding: '12px 14px', borderRadius: 8,
        borderLeft: '3px solid #22c55e',
        color: '#cbd5e1', fontSize: 13,
      }}>
        <div style={{ whiteSpace: 'pre-wrap', lineHeight: 1.5, marginBottom: 10 }}>
          {content}
        </div>

        {reason && (
          <div style={{ color: '#64748b', fontSize: 11, marginBottom: 8 }}>
            Status: <span style={{ color: '#94a3b8' }}>{reason}</span>
          </div>
        )}

        {variables && Object.keys(variables).length > 0 && (
          <div style={{
            marginTop: 10, padding: '10px 0',
            borderTop: '1px solid #334155',
          }}>
            <div style={{ fontSize: 11, fontWeight: 600, color: '#94a3b8', marginBottom: 6 }}>
              Variables
            </div>
            {Object.entries(variables).map(([key, val]) => (
              <div key={key} style={{ fontSize: 11, marginBottom: 3 }}>
                <span style={{ color: '#60a5fa' }}>{key}</span>
                <span style={{ color: '#64748b' }}> = </span>
                <span style={{ color: '#cbd5e1', fontFamily: 'ui-monospace, monospace' }}>
                  {typeof val === 'string' ? `"${val}"` : JSON.stringify(val)}
                </span>
              </div>
            ))}
          </div>
        )}
      </div>
    )
  }

  return null
}
