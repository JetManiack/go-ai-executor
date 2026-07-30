export function formatDuration(ms) {
  if (ms === null || ms === undefined) return "—";
  if (ms < 1) return "<1ms";
  if (ms < 1000) return ms + "ms";
  return (ms / 1000).toFixed(ms < 10000 ? 2 : 1) + "s";
}

export function formatTime(iso) {
  const date = new Date(iso);
  if (isNaN(date.getTime())) return iso;
  return date.toLocaleString();
}

export function formatClock(iso) {
  const date = new Date(iso);
  if (isNaN(date.getTime())) return "";
  return date.toLocaleTimeString();
}

// ANSI escape sequences are removed rather than rendered: this terminal exists to
// read what a command produced, and a full emulator (xterm.js) would be another
// vendored, checksum-pinned dependency for the sake of colour. Left in place the
// codes show up as literal garbage — "[0;32m" mid-sentence.
//
// Built from string parts with explicit  escapes rather than written as a
// regex literal: a literal ESC byte in a source file is invisible in every editor
// and diff, and is silently corrupted by anything that touches the file.
const ANSI_PATTERN = new RegExp(
  [
    "\\u001b\\[[0-9;?]*[ -/]*[@-~]", // CSI — colours, cursor movement, erase
    "\\u001b\\][^\\u0007\\u001b]*(?:\\u0007|\\u001b\\\\)", // OSC — window titles
    "\\u001b[@-Z\\\\-_]", // two-character escapes
    "\\r(?!\\n)", // a bare CR from a progress bar would hide the line before it
  ].join("|"),
  "g",
);

export function stripANSI(text) {
  if (!text) return "";
  return text.replace(ANSI_PATTERN, "");
}
