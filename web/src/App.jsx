import { useHashRoute } from "./router.js";
import { useCurrentUser } from "./currentUser.js";
import TopNav from "./TopNav.jsx";
import SandboxList from "./SandboxList.jsx";
import Terminal from "./Terminal.jsx";
import Agents from "./Agents.jsx";

export default function App() {
  const route = useHashRoute();
  const { user, error } = useCurrentUser();

  if (error) {
    return (
      <div className="login-gate">
        <p>You need to log in to watch sandbox terminals.</p>
        <a href="/auth/login">Log in</a>
      </div>
    );
  }

  if (!user) {
    return <div className="empty-state">Loading…</div>;
  }

  const terminalMatch = route.path.match(/^\/sandboxes\/(.+)$/);
  const active = route.path === "/agents" ? "agents" : "sandboxes";

  let screen;
  if (route.path === "/agents") {
    // A viewer cannot manage agents and isn't offered the tab; a hand-typed
    // #/agents falls back to the sandbox list rather than rendering a screen
    // whose every request would 403.
    screen = user.role === "admin" ? <Agents /> : <SandboxList />;
  } else if (terminalMatch) {
    screen = <Terminal sandboxId={terminalMatch[1]} role={user.role} />;
  } else {
    screen = <SandboxList />;
  }

  return (
    <>
      <header className="site">
        <h1>go-ai-executor</h1>
        <TopNav active={active} role={user.role} displayName={user.display_name} />
      </header>
      <main>{screen}</main>
    </>
  );
}
