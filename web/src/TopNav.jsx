export function TopNav({ activeTab, onSelectTab, currentUser }) {
  return (
    <header className="top-nav">
      <div className="nav-brand">
        <span className="brand-logo">⚡</span>
        <span className="brand-title">AI Executor</span>
      </div>
      <nav className="nav-links">
        <button
          className={`nav-btn ${activeTab === 'agents' ? 'active' : ''}`}
          onClick={() => onSelectTab('agents')}
        >
          🤖 Agents & Sandboxes
        </button>
      </nav>
      <div className="nav-user">
        {currentUser && (
          <span className="user-badge">
            👤 {currentUser.actor?.display_name || 'Admin'} ({currentUser.identity?.role || 'admin'})
          </span>
        )}
      </div>
    </header>
  );
}
