import { App } from "./App.jsx";

const container = document.getElementById("root");
if (container && window.ReactDOM) {
  const root = window.ReactDOM.createRoot(container);
  root.render(<App />);
}