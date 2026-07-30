import { TopNav } from './TopNav.jsx';
import { Agents } from './Agents.jsx';
import { LiveTerminal } from './LiveTerminal.jsx';

const { useState, useEffect } = React;

export function App() {
  const [activeTab, setActiveTab] = useState('agents');
  const [selectedAgent, setSelectedAgent] = useState(null);
  const [currentUser, setCurrentUser] = useState(null);

  useEffect(() => {
    fetch('/api/me')
      .then((res) => res.json())
      .then((data) => setCurrentUser(data))
      .catch((err) => console.error('Failed to fetch user:', err));
  }, []);

  return (
    <div className="app-container">
      <TopNav
        activeTab={activeTab}
        onSelectTab={(tab) => {
          setActiveTab(tab);
          setSelectedAgent(null);
        }}
        currentUser={currentUser}
      />
      <main className="content-area">
        {selectedAgent ? (
          <LiveTerminal
            agent={selectedAgent}
            onBack={() => setSelectedAgent(null)}
          />
        ) : (
          <Agents
            onOpenTerminal={(agent) => setSelectedAgent(agent)}
          />
        )}
      </main>
    </div>
  );
}
