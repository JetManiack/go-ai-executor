const { useState, useEffect } = React;

export function Agents({ onOpenTerminal }) {
  const [agents, setAgents] = useState([]);
  const [newAgentName, setNewAgentName] = useState('');
  const [loading, setLoading] = useState(true);
  const [issuedToken, setIssuedToken] = useState(null);

  const fetchAgents = async () => {
    try {
      const res = await fetch('/api/agents');
      if (res.ok) {
        const data = await res.json();
        setAgents(data || []);
      }
    } catch (err) {
      console.error('Failed to fetch agents:', err);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchAgents();
  }, []);

  const handleCreateAgent = async (e) => {
    e.preventDefault();
    if (!newAgentName.trim()) return;

    try {
      const res = await fetch('/api/agents', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ display_name: newAgentName.trim() }),
      });
      if (res.ok) {
        setNewAgentName('');
        fetchAgents();
      } else {
        const err = await res.json();
        alert('Error creating agent: ' + (err.error || 'Unknown error'));
      }
    } catch (err) {
      alert('Network error: ' + err.message);
    }
  };

  const handleIssueToken = async (agentID, agentName) => {
    try {
      const res = await fetch(`/api/agents/${agentID}/tokens`, {
        method: 'POST',
      });
      if (res.ok) {
        const data = await res.json();
        setIssuedToken({
          agentName,
          token: data.raw_token,
        });
        fetchAgents();
      } else {
        const err = await res.json();
        alert('Error issuing token: ' + (err.error || 'Unknown error'));
      }
    } catch (err) {
      alert('Network error: ' + err.message);
    }
  };

  const handleRevokeToken = async (agentID, credID) => {
    if (!confirm('Are you sure you want to revoke this token?')) return;
    try {
      const res = await fetch(`/api/agents/${agentID}/tokens/${credID}`, {
        method: 'DELETE',
      });
      if (res.ok) {
        fetchAgents();
      }
    } catch (err) {
      alert('Failed to revoke token: ' + err.message);
    }
  };

  return (
    <div className="agents-page">
      <div className="page-header">
        <div>
          <h1>🤖 Authorized Agents & Sandboxes</h1>
          <p className="subtitle">
            Manage AI agent credentials and inspect their dedicated execution sandboxes.
          </p>
        </div>
      </div>

      {issuedToken && (
        <div className="token-modal">
          <div className="token-card">
            <h3>🔑 New Bearer Token Issued for "{issuedToken.agentName}"</h3>
            <p>Copy this token now. It will not be displayed again!</p>
            <div className="token-box">
              <code>{issuedToken.token}</code>
              <button
                className="copy-btn"
                onClick={() => {
                  navigator.clipboard.writeText(issuedToken.token);
                  alert('Token copied to clipboard!');
                }}
              >
                Copy
              </button>
            </div>
            <button className="close-btn" onClick={() => setIssuedToken(null)}>
              Done
            </button>
          </div>
        </div>
      )}

      <div className="create-agent-card">
        <h3>Create New Agent</h3>
        <form onSubmit={handleCreateAgent} className="create-form">
          <input
            type="text"
            placeholder="Agent Name (e.g. Claude-Coder, RefactorBot)..."
            value={newAgentName}
            onChange={(e) => setNewAgentName(e.target.value)}
          />
          <button type="submit" className="primary-btn" disabled={!newAgentName.trim()}>
            + Create Agent
          </button>
        </form>
      </div>

      {loading ? (
        <div className="loading">Loading agents...</div>
      ) : agents.length === 0 ? (
        <div className="empty-state">No agents registered yet. Create one above!</div>
      ) : (
        <div className="agents-grid">
          {agents.map((agent) => (
            <div key={agent.id} className="agent-card">
              <div className="agent-header">
                <h3>🤖 {agent.display_name}</h3>
                <span className="agent-id">ID: {agent.id.slice(0, 8)}...</span>
              </div>

              <div className="sandbox-info">
                <span className="label">Personal Sandbox Jail:</span>
                <code>./scratch/agents/{agent.id}/</code>
              </div>

              <div className="tokens-section">
                <h4>Tokens ({agent.credentials?.length || 0})</h4>
                {agent.credentials && agent.credentials.length > 0 ? (
                  <ul className="tokens-list">
                    {agent.credentials.map((cred) => (
                      <li key={cred.id} className={cred.revoked_at ? 'revoked' : 'active'}>
                        <span className="cred-id">ID: {cred.id.slice(0, 8)}</span>
                        {cred.revoked_at ? (
                          <span className="badge revoked-badge">Revoked</span>
                        ) : (
                          <button
                            className="revoke-btn"
                            onClick={() => handleRevokeToken(agent.id, cred.id)}
                          >
                            Revoke
                          </button>
                        )}
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p className="no-tokens">No active tokens issued.</p>
                )}
              </div>

              <div className="card-actions">
                <button
                  className="secondary-btn"
                  onClick={() => handleIssueToken(agent.id, agent.display_name)}
                >
                  🔑 Issue Token
                </button>
                <button
                  className="primary-btn terminal-btn"
                  onClick={() => onOpenTerminal(agent)}
                >
                  📺 Live Terminal
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
