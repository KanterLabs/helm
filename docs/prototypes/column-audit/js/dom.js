/* DOM helpers. Fixture text is only ever inserted as text nodes, never parsed
   as HTML, so excerpts such as "<script>" render literally. */
(function () {
  const SVG = 'http://www.w3.org/2000/svg';
  const PATHS = {
    arrow: 'M5 12h14M13 6l6 6-6 6',
    check: 'M5 12.5l4.5 4.5L19 7.5',
    cross: 'M6 6l12 12M18 6L6 18',
    question: 'M9.1 9a3 3 0 1 1 4.2 2.7c-.8.4-1.3 1.1-1.3 2v.3M12 17.5v.01',
    dash: 'M6 12h12',
    lock: 'M7 11V8a5 5 0 0 1 10 0v3M6 11h12v9H6z',
    clock: 'M12 7v5l3 2M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0',
    branch: 'M6 4v12M6 16a2 2 0 1 0 0 4 2 2 0 0 0 0-4zM18 4a2 2 0 1 0 0 4 2 2 0 0 0 0-4zM18 8c0 5-8 3-12 8',
    pr: 'M6 4a2 2 0 1 0 0 4 2 2 0 0 0 0-4zM6 8v12M18 20a2 2 0 1 0 0-4 2 2 0 0 0 0 4zM18 16V9a3 3 0 0 0-3-3h-4M13 3l-3 3 3 3',
    retry: 'M4 4v6h6M20 20v-6h-6M5 15a8 8 0 0 0 14 2M19 9A8 8 0 0 0 5 7',
    sun: 'M12 4V2M12 22v-2M4 12H2M22 12h-2M5.6 5.6 4.2 4.2M19.8 19.8l-1.4-1.4M5.6 18.4l-1.4 1.4M19.8 4.2l-1.4 1.4M16 12a4 4 0 1 1-8 0 4 4 0 0 1 8 0',
    moon: 'M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z',
    file: 'M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8zM14 3v5h5',
    skip: 'M5 5l8 7-8 7zM17 5v14',
    alert: 'M12 8v5M12 16.5v.01M10.3 3.8 2.5 18a2 2 0 0 0 1.7 3h15.6a2 2 0 0 0 1.7-3L13.7 3.8a2 2 0 0 0-3.4 0z'
  };

  function icon(name, cls) {
    const svg = document.createElementNS(SVG, 'svg');
    svg.setAttribute('viewBox', '0 0 24 24');
    svg.setAttribute('aria-hidden', 'true');
    svg.setAttribute('class', 'icon' + (cls ? ' ' + cls : ''));
    const p = document.createElementNS(SVG, 'path');
    p.setAttribute('d', PATHS[name] || PATHS.dash);
    svg.appendChild(p);
    return svg;
  }

  // h('button.btn.primary', { onclick, 'aria-label': 'x' }, 'Text', child)
  function h(spec, attrs, ...children) {
    const [tagId, ...classes] = spec.split('.');
    const [tag, id] = tagId.split('#');
    const el = document.createElement(tag || 'div');
    if (id) el.id = id;
    if (classes.length) el.className = classes.join(' ');
    if (attrs) {
      for (const [k, v] of Object.entries(attrs)) {
        if (v === undefined || v === null || v === false) continue;
        if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
        else if (k === 'text') el.textContent = v;
        else if (k === 'checked' || k === 'value' || k === 'disabled' || k === 'selected') el[k] = v;
        else if (k === 'class') el.className += (el.className ? ' ' : '') + v;
        else el.setAttribute(k, v === true ? '' : String(v));
      }
    }
    append(el, children);
    return el;
  }

  function append(el, children) {
    for (const c of children.flat(Infinity)) {
      if (c === null || c === undefined || c === false) continue;
      el.appendChild(typeof c === 'string' || typeof c === 'number' ? document.createTextNode(String(c)) : c);
    }
    return el;
  }

  let toastTimer = 0;
  // Polite announcements for screen readers, mirrored as a visible toast.
  function announce(message, visible) {
    const live = document.getElementById('live');
    if (live) {
      live.textContent = '';
      requestAnimationFrame(() => { live.textContent = message; });
    }
    if (!visible) return;
    const toast = document.getElementById('toast');
    if (!toast) return;
    toast.textContent = message;
    toast.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => { toast.hidden = true; }, 4200);
  }

  function focusables(root) {
    return [...root.querySelectorAll('button, [href], input, select, textarea, summary, [tabindex]:not([tabindex="-1"])')]
      .filter((el) => !el.disabled && el.offsetParent !== null);
  }

  window.HelmDom = { h, icon, append, announce, focusables };
})();
