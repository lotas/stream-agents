document.addEventListener('DOMContentLoaded', () => {
  // ── List page: project select navigates to a server-rendered URL ────────
  const projectSel = document.getElementById('project-filter');
  if (projectSel) {
    projectSel.addEventListener('change', () => {
      if (projectSel.value) window.location.href = projectSel.value;
    });
  }


  // ── Copy buttons on <pre> blocks ────────────────────────────────────────
  function attachCopyBtn(pre) {
    if (pre.querySelector('.copy-btn')) return;
    const btn = document.createElement('button');
    btn.className = 'copy-btn';
    btn.textContent = 'Copy';
    btn.addEventListener('click', () => {
      const code = pre.querySelector('code');
      navigator.clipboard.writeText(code ? code.textContent : pre.textContent).then(() => {
        btn.textContent = 'Copied!';
        setTimeout(() => { btn.textContent = 'Copy'; }, 1500);
      });
    });
    pre.style.position = 'relative';
    pre.appendChild(btn);
  }
  document.querySelectorAll('pre').forEach(pre => attachCopyBtn(pre));

  // ── Filter toolbar ───────────────────────────────────────────────────────
  const STORAGE_KEY = 'stream-agents.filters';

  function loadState() {
    try { return JSON.parse(localStorage.getItem(STORAGE_KEY)) || {}; } catch { return {}; }
  }
  function saveState(s) {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(s));
  }

  const state = loadState();
  if (!('user' in state)) state.user = true;
  if (!('assistant' in state)) state.assistant = true;
  if (!('hiddenTools' in state)) state.hiddenTools = [];

  function applyState() {
    document.body.classList.toggle('hide-user', !state.user);
    document.body.classList.toggle('hide-assistant', !state.assistant);
    document.body.dataset.hideTool = state.hiddenTools.join(' ');

    // Apply individual tool visibility via inline display (simpler than dynamic CSS).
    document.querySelectorAll('.tool-pair[data-tool]').forEach(el => {
      const tool = el.dataset.tool;
      el.style.display = state.hiddenTools.includes(tool) ? 'none' : '';
    });

    // Sync role chip aria-pressed states.
    ['user', 'assistant'].forEach(f => {
      const btn = document.querySelector(`.chip[data-filter="${f}"]`);
      if (btn) btn.setAttribute('aria-pressed', String(state[f] !== false));
    });

    // Sync tool checkboxes.
    document.querySelectorAll('.tool-filter-menu input[data-tool]').forEach(cb => {
      cb.checked = !state.hiddenTools.includes(cb.dataset.tool);
    });
  }

  // Wire role filter chips (User / Assistant).
  document.querySelectorAll('.chip[data-filter="user"], .chip[data-filter="assistant"]').forEach(btn => {
    btn.addEventListener('click', () => {
      const f = btn.dataset.filter;
      state[f] = !state[f];
      saveState(state);
      applyState();
    });
  });

  // Close the tool-filter dropdown when clicking outside of it.
  document.addEventListener('click', e => {
    document.querySelectorAll('.chip-dropdown[open]').forEach(dd => {
      if (!dd.contains(e.target)) dd.open = false;
    });
  });

  // Wire per-tool checkboxes.
  document.querySelectorAll('.tool-filter-menu input[data-tool]').forEach(cb => {
    cb.addEventListener('change', () => {
      const tool = cb.dataset.tool;
      if (cb.checked) {
        state.hiddenTools = state.hiddenTools.filter(t => t !== tool);
      } else {
        if (!state.hiddenTools.includes(tool)) state.hiddenTools.push(tool);
      }
      saveState(state);
      applyState();
    });
  });

  // "only" button: hide all other tools, show just this one.
  const allToolNames = [...document.querySelectorAll('.tool-filter-menu input[data-tool]')].map(cb => cb.dataset.tool);
  document.querySelectorAll('.tool-only').forEach(btn => {
    btn.addEventListener('click', () => {
      const tool = btn.dataset.tool;
      state.hiddenTools = allToolNames.filter(t => t !== tool);
      saveState(state);
      applyState();
    });
  });

  // ── Collapse / expand all tools ─────────────────────────────────────────
  const collapseBtn = document.getElementById('collapse-all');
  if (collapseBtn) {
    let allCollapsed = true; // tools start closed
    collapseBtn.textContent = 'Expand tools';
    collapseBtn.addEventListener('click', () => {
      allCollapsed = !allCollapsed;
      document.querySelectorAll('details.tool-pair').forEach(d => { d.open = !allCollapsed; });
      collapseBtn.textContent = allCollapsed ? 'Expand tools' : 'Collapse tools';
    });
  }

  // ── TOC smooth scroll ────────────────────────────────────────────────────
  document.querySelectorAll('.toc-list a').forEach(a => {
    a.addEventListener('click', e => {
      const id = a.getAttribute('href').slice(1);
      const target = document.getElementById(id);
      if (target) {
        e.preventDefault();
        target.scrollIntoView({ behavior: 'smooth', block: 'start' });
        history.replaceState(null, '', '#' + id);
      }
    });
  });

  applyState();

  // ── Session streaming (hot sessions only) ────────────────────────────────
  const transcript = document.querySelector('.transcript[data-stream]');
  if (transcript) {
    const url = `${transcript.dataset.stream}?offset=${transcript.dataset.streamOffset}`;
    const es = new EventSource(url);

    let atBottom = true;
    window.addEventListener('scroll', () => {
      const distFromBottom = document.documentElement.scrollHeight - window.scrollY - window.innerHeight;
      atBottom = distFromBottom < 150;
    }, { passive: true });

    function appendToTranscript(html) {
      const tmp = document.createElement('div');
      tmp.innerHTML = html.trim();
      const el = tmp.firstElementChild;
      if (!el) return;
      transcript.appendChild(el);
      el.querySelectorAll('pre').forEach(pre => attachCopyBtn(pre));
      if (el.classList.contains('tool-pair') && el.dataset.tool) {
        if (state.hiddenTools.includes(el.dataset.tool)) el.style.display = 'none';
      }
      if (atBottom) window.scrollTo({ top: document.documentElement.scrollHeight, behavior: 'smooth' });
    }

    es.addEventListener('append', e => appendToTranscript(e.data));

    es.addEventListener('patch', e => {
      const { id, html } = JSON.parse(e.data);
      const el = document.getElementById(id);
      if (!el) return;
      const tmp = document.createElement('div');
      tmp.innerHTML = html.trim();
      const newEl = tmp.firstElementChild;
      if (newEl) {
        el.replaceWith(newEl);
        newEl.querySelectorAll('pre').forEach(pre => attachCopyBtn(pre));
      }
    });

    es.onerror = () => es.close();
  }
});
