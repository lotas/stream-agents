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

    const fmtTok = n => n >= 1000 ? (n / 1000).toFixed(1) + 'K' : String(n);

    es.addEventListener('stats', e => {
      try {
        const d = JSON.parse(e.data);
        const dur = document.getElementById('stat-duration');
        if (dur && d.duration) dur.textContent = d.duration;
        const inp = document.getElementById('stat-in');
        if (inp && d.inputTokens) inp.textContent = fmtTok(d.inputTokens);
        const out = document.getElementById('stat-out');
        if (out && d.outputTokens) out.textContent = fmtTok(d.outputTokens);
        const cache = document.getElementById('stat-cache');
        if (cache && d.cacheReadTokens) cache.textContent = fmtTok(d.cacheReadTokens);
      } catch (_) {}
    });

    es.onerror = () => es.close();
  }

  // ── Active sessions panel (poll /hot.json) ───────────────────────────
  (function () {
    const section = document.getElementById('hot-section');
    const list = document.getElementById('hot-list');
    if (!section || !list) return;

    const myAgent = document.body.dataset.sessionAgent;
    const myId    = document.body.dataset.sessionId;
    const LIMIT = 8;

    function shortPathJS(p) {
      return p.replace(/^\/(?:Users|home)\/[^/]+\//, '~/');
    }

    function renderHot(items) {
      const filtered = items
        .filter(it => !(it.agent === myAgent && it.id === myId))
        .slice(0, LIMIT);
      if (filtered.length === 0) {
        section.hidden = true;
        list.replaceChildren();
        return;
      }
      const frag = document.createDocumentFragment();
      for (const it of filtered) {
        const li = document.createElement('li');
        li.className = 'hot-item';

        const a = document.createElement('a');
        a.href = `/session/${encodeURIComponent(it.agent)}/${encodeURIComponent(it.id)}`;

        const dot = document.createElement('span');
        dot.className = 'hot-dot';
        dot.setAttribute('aria-hidden', 'true');

        const title = document.createElement('span');
        title.className = 'hot-title';
        title.textContent = it.title || '(no title)';

        const meta = document.createElement('span');
        meta.className = 'hot-meta';
        const badge = document.createElement('span');
        badge.className = 'badge badge-' + it.agent;
        badge.textContent = it.agent;
        const proj = document.createElement('span');
        proj.className = 'hot-project';
        proj.textContent = shortPathJS(it.project || '');
        meta.appendChild(badge);
        meta.appendChild(document.createTextNode(' · '));
        meta.appendChild(proj);

        a.appendChild(dot);
        a.appendChild(title);
        a.appendChild(meta);
        li.appendChild(a);
        frag.appendChild(li);
      }
      list.replaceChildren(frag);
      section.hidden = false;
    }

    async function refreshHot() {
      try {
        const r = await fetch('/hot.json', { cache: 'no-store' });
        if (!r.ok) return;
        renderHot(await r.json());
      } catch (_) { /* network blip — keep last render */ }
    }

    setInterval(refreshHot, 20_000);
  })();

  // ── Global /notify subscription ──────────────────────────────────────
  (function () {
    const notifyRoot = document.getElementById('notify-root');
    if (!notifyRoot) return;

    function shortPathJS(p) {
      return p.replace(/^\/(?:Users|home)\/[^/]+\//, '~/');
    }

    function showToast(data) {
      const bodyAgent = document.body.dataset.sessionAgent;
      const bodyId    = document.body.dataset.sessionId;
      if (bodyAgent === data.agent && bodyId === data.id) return;

      const notifyKey = data.agent + ':' + data.id;
      const existing  = notifyRoot.querySelector('[data-notify-key="' + CSS.escape(notifyKey) + '"]');
      if (existing) existing.remove();

      const toast = document.createElement('div');
      toast.className = 'notify-toast';
      toast.dataset.notifyKey = notifyKey;

      const link = document.createElement('a');
      link.className = 'notify-link';
      link.href = '/session/' + data.agent + '/' + data.id;

      const titleEl = document.createElement('span');
      titleEl.className = 'notify-title';
      const rawTitle = data.title || data.id;
      titleEl.textContent = rawTitle.length > 50 ? rawTitle.slice(0, 50) + '…' : rawTitle;
      link.appendChild(titleEl);

      if (data.project) {
        const projEl = document.createElement('span');
        projEl.className = 'notify-project';
        projEl.textContent = shortPathJS(data.project);
        link.appendChild(projEl);
      }

      const closeBtn = document.createElement('button');
      closeBtn.className = 'notify-close';
      closeBtn.setAttribute('aria-label', 'Dismiss');
      closeBtn.textContent = '×';
      toast.appendChild(link);
      toast.appendChild(closeBtn);
      notifyRoot.appendChild(toast);

      const fadeTimer   = setTimeout(() => toast.classList.add('notify-fade'), 7500);
      const removeTimer = setTimeout(() => toast.remove(), 8000);
      closeBtn.addEventListener('click', () => {
        clearTimeout(fadeTimer);
        clearTimeout(removeTimer);
        toast.remove();
      });
    }

    const notifyES = new EventSource('/notify');
    notifyES.addEventListener('session-updated', e => {
      try { showToast(JSON.parse(e.data)); } catch (_) {}
    });
    notifyES.onerror = () => {};
  })();
});
