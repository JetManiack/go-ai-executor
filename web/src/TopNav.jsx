export default function TopNav({ active, role, displayName }) {
  function handleLogout() {
    fetch("/auth/logout", { method: "POST" }).then(() => {
      window.location.href = "/";
    });
  }

  return (
    <nav className="nav-tabs">
      <a href="#/sandboxes" className={active === "sandboxes" ? "active" : ""}>
        Sandboxes
      </a>
      {/* Hidden for a viewer because every request the screen makes would be
          refused; App also redirects a hand-typed #/agents. */}
      {role === "admin" && (
        <a href="#/agents" className={active === "agents" ? "active" : ""}>
          Agents
        </a>
      )}
      <span className="spacer" />
      <span className="whoami">
        {displayName} · {role}
      </span>
      <button type="button" className="logout-link" onClick={handleLogout}>
        Log out
      </button>
    </nav>
  );
}
