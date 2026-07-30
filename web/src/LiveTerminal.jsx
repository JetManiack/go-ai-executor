const { useState, useEffect, useRef } = React;

export function LiveTerminal({ agent, onBack }) {
  const [logs, setLogs] = useState([]);
  const [command, setCommand] = useState('');
  const [workDir, setWorkDir] = useState('');
  const [executing, setExecuting] = useState(false);
  const [connected, setConnected] = useState(false);
  const terminalEndRef = useRef(null);

  useEffect(() => {
    // 1. Fetch initial logs history
    fetch(`/api/agents/${agent.id}/logs`)
      .then((res) => res.json())
      .then((data) => {
        if (Array.isArray(data)) {
          setLogs(data.reverse());
        }
      })
      .catch((err) => console.error('Failed to load logs history:', err));

    // 2. Connect to SSE Stream
    const eventSource = new EventSource(`/api/agents/${agent.id}/stream`);

    eventSource.onopen = () => {
      setConnected(true);
    };

    eventSource.onmessage = (e) => {
      try {
        const event = JSON.parse(e.data);
        setLogs((prev) => [...prev, event]);
      } catch (err) {
        console.error('Error parsing SSE event:', err);
      }
    };

    eventSource.onerror = () => {
      setConnected(false);
    };

    return () => {
      eventSource.close();
    };
  }, [agent.id]);

  useEffect(() => {
    terminalEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [logs]);

  const handleExec = async (e) => {
    e.preventDefault();
    if (!command.trim() || executing) return;

    setExecuting(true);
    try {
      const res = await fetch(`/api/agents/${agent.id}/exec`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ command: command.trim(), work_dir: workDir.trim() }),
      });
      if (res.ok) {
        setCommand('');
      } else {
        const err = await res.json();
        alert('Execution failed: ' + (err.error || 'Unknown error'));
      }
    } catch (err) {
      alert('Network error: ' + err.message);
    } finally {
      setExecuting(false);
    }
  };

  return (
    <div className="terminal-view">
      <div className="terminal-header">
        <button className="back-btn" onClick={onBack}>
          ← Back to Agents
        </button>
        <div className="agent-info">
          <h2>📺 Live Terminal: {agent.display_name}</h2>
          <span className="sandbox-path">Sandbox: ./scratch/agents/{agent.id}/</span>
        </div>
        <div className="terminal-status">
          <span className={`status-dot ${connected ? 'online' : 'offline'}`}></span>
          <span>{connected ? 'Live Stream Active' : 'Connecting...'}</span>
          <button className="clear-btn" onClick={() => setLogs([])}>
            Clear Output
          </button>
        </div>
      </div>

      <div className="terminal-body">
        {logs.length === 0 ? (
          <div className="terminal-empty">
            No executions recorded yet. Execute a command below or connect an MCP agent to see live output.
          </div>
        ) : (
          logs.map((log, idx) => (
            <div key={log.id || idx} className="terminal-block">
              <div className="command-line">
                <span className="prompt">$</span>
                <span className="cmd-text">{log.command}</span>
                {log.work_dir && <span className="workdir-tag">dir: {log.work_dir}</span>}
                <span className="timestamp">{new Date(log.created_at || log.timestamp).toLocaleTimeString()}</span>
                <span className={`exit-badge ${log.exit_code === 0 ? 'success' : 'error'}`}>
                  code: {log.exit_code} ({log.duration_ms}ms)
                </span>
              </div>
              {log.stdout && <pre className="stdout-output">{log.stdout}</pre>}
              {log.stderr && <pre className="stderr-output">{log.stderr}</pre>}
            </div>
          ))
        )}
        <div ref={terminalEndRef} />
      </div>

      <form className="terminal-input-bar" onSubmit={handleExec}>
        <span className="input-prompt">$</span>
        <input
          type="text"
          className="cmd-input"
          placeholder="Type a shell command to run in this agent's sandbox (e.g. ls -la, pwd, echo hello)..."
          value={command}
          onChange={(e) => setCommand(e.target.value)}
          disabled={executing}
        />
        <button type="submit" className="exec-btn" disabled={executing || !command.trim()}>
          {executing ? 'Running...' : 'Execute'}
        </button>
      </form>
    </div>
  );
}
