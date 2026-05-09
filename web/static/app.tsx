import { render } from 'preact';

function App() {
  return <div>mymail loading…</div>;
}

const root = document.getElementById('app');
if (root) {
  render(<App />, root);
}
