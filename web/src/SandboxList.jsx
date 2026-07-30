import { formatTime } from "./format.js";

const { useCallback, useEffect, useState } = React;

// REFRESH_MS keeps the list roughly current without a stream of its own. The
// terminal is the live view; this is an index, and polling it a few times a
// minute is enough to notice a sandbox that started working.
const REFRESH_MS = 5000;

export default function SandboxList() {
  const [rows, setRows] = useState(null);
  const [error, setError] = useState(null);

  const load = useCallback(() => {
    fetch("/api/sandboxes")
      .then((res) => {
        if (!res.ok) throw new Error("failed to load sandboxes (" + res.status + ")");
        return res.json();
      })
      .then((data) => {
        setRows(data || []);
        setError(null);
      })
      .catch((err) => setError(String(err)));
  }, []);

  useEffect(() => {
    load();
    const timer = window.setInterval(load, REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [load]);

  if (error) return <div className="callout">{error}</div>;
  if (rows === null) return <div className="empty-state">Loading…</div>;

  return (
    <div>
      <h2 className="section-title">Sandboxes</h2>

      {rows.length === 0 ? (
        <div className="empty-state">
          No agents registered yet. Create one under Agents to give it a sandbox.
        </div>
      ) : (
        <div className="sandbox-grid">
          {rows.map((row) => (
            <a className="sandbox-card" key={row.actor_id} href={"#/sandboxes/" + row.actor_id}>
              <div className="beacon-row">
                <span
                  className={"beacon" + (row.running_commands > 0 ? " active" : "")}
                  aria-hidden="true"
                />
                <span className="name">{row.display_name}</span>
              </div>

              {row.block ? (
                <span className="status-badge error">blocked</span>
              ) : (
                <span className="status-badge ok">{row.live ? "live" : "idle"}</span>
              )}

              <span className="metric">
                {row.running_commands} running · {row.watchers} watching
              </span>

              {row.block ? (
                <span className="block-summary">
                  {row.block.reason} — {row.block.blocked_by_name},{" "}
                  {formatTime(row.block.blocked_at)}
                </span>
              ) : null}

              {/* A sandbox only exists once its agent has made a call, so
                  "not started" is ordinary rather than a fault. */}
              {!row.live && !row.block ? (
                <span className="metric dim">no sandbox yet this run</span>
              ) : null}
            </a>
          ))}
        </div>
      )}
    </div>
  );
}
