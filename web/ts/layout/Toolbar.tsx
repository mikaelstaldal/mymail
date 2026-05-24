import { useState } from 'preact/hooks';

export function Toolbar() {
  const [query, setQuery] = useState('');

  function handleSearch(e: SubmitEvent) {
    e.preventDefault();
    const q = query.trim();
    if (q) {
      window.location.hash = `#/search?q=${encodeURIComponent(q)}`;
    }
  }

  return (
    <footer class="bottombar">
      <button
        class="compose-btn"
        onClick={() => { window.location.hash = '#/compose'; }}
      >
        ✏ Compose
      </button>
      <form class="search-bar" onSubmit={handleSearch}>
        <span class="search-icon">🔍</span>
        <input
          type="search"
          placeholder="Search all mail…"
          value={query}
          onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
        />
      </form>
    </footer>
  );
}
