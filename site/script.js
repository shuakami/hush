// hush — landing page interactions
//
// Three small things:
//   1. tab groups (install snippet + language samples)
//   2. clipboard copy on the hero install snippet
//   3. a rolling audit feed that streams a fixed sequence of records,
//      with a hash chain that updates each tick — proof-of-life for the
//      audit.section without committing to a server-rendered tail.

(() => {
  // ─── tab groups ──────────────────────────────────────────────────────
  function bindTabs(tabSelector, tabAttr, paneAttr) {
    const tabs = document.querySelectorAll(tabSelector);
    if (!tabs.length) return;
    const root = tabs[0].closest('section, div');
    const all = root ? root.querySelectorAll(`[${paneAttr}]`) : [];

    tabs.forEach((tab) => {
      tab.addEventListener('click', () => {
        const key = tab.getAttribute(tabAttr);
        tabs.forEach((t) => {
          const active = t === tab;
          t.classList.toggle('is-active', active);
          t.setAttribute('aria-selected', active ? 'true' : 'false');
        });
        all.forEach((pane) => {
          const match = pane.getAttribute(paneAttr) === key;
          pane.hidden = !match;
          pane.classList.toggle('is-active', match);
        });
      });
    });
  }

  bindTabs('.ht-tab', 'data-tab', 'data-tab-pane');
  bindTabs('.lang-tab', 'data-lang', 'data-lang-pane');

  // ─── clipboard copy ──────────────────────────────────────────────────
  document.querySelectorAll('[data-copy]').forEach((btn) => {
    btn.addEventListener('click', async () => {
      const body = btn.closest('.hero-install-body');
      if (!body) return;
      const cmd = body.querySelector('.ht-cmd');
      if (!cmd || !navigator.clipboard) return;
      try {
        await navigator.clipboard.writeText(cmd.textContent.trim());
        btn.textContent = 'copied';
        btn.classList.add('is-copied');
        setTimeout(() => {
          btn.textContent = 'copy';
          btn.classList.remove('is-copied');
        }, 1400);
      } catch (_) {
        /* clipboard blocked; nothing to do. */
      }
    });
  });

  // ─── rolling audit feed ──────────────────────────────────────────────
  const feed = document.getElementById('audit-feed');
  if (feed) {
    const records = [
      ['host.exec',   'hk1',          'systemctl status nginx',   'exit=0'],
      ['secret.read', 'hk1-root-password', 'caller=claude-code',  ''],
      ['host.exec',   'hk2',          'uptime',                   'exit=0'],
      ['host.put',    'sg-1',         'build.tar.gz → /opt/app',  '12.4 MB'],
      ['secret.read', 'sg-1-key',     'caller=ci-runner',         ''],
      ['host.exec',   'sg-1',         'systemctl restart app',    'exit=0'],
      ['audit.verify',  '—',           'records=1284, last sha=7f3a…', 'ok'],
      ['apikey.mint', 'claude-code',  'scope=host:exec,secret:read', ''],
      ['host.exec',   'hk1',          'tail -n 80 /var/log/app',  'exit=0'],
      ['host.exec',   '--tag prod',   'systemctl is-active nginx',  '3/3 active'],
    ];
    const hashes = ['7f3a…d901', '8b21…04ce', 'a4d9…1f17', 'c0e2…39a8', '2bff…5c10',
                    '64a1…9d22', 'fe07…11b6', 'ab3c…c0d8', '90ee…7785', '13ca…22b4'];

    const ts = () => {
      const d = new Date();
      const hh = String(d.getHours()).padStart(2, '0');
      const mm = String(d.getMinutes()).padStart(2, '0');
      const ss = String(d.getSeconds()).padStart(2, '0');
      return `${hh}:${mm}:${ss}`;
    };

    let cursor = 0;
    const headEl = document.querySelector('.audit-meta strong');

    function render() {
      const visible = 9;
      feed.innerHTML = '';
      for (let i = 0; i < visible; i++) {
        const idx = (cursor + i) % records.length;
        const [cat, host, arg, tail] = records[idx];
        const line = document.createElement('span');
        line.className = 'a-line';
        line.innerHTML =
          `<span class="a-ts">${ts()}</span>  ` +
          `<span class="a-cat">${cat.padEnd(13, ' ')}</span> ` +
          `<span class="a-host">${(host || '').padEnd(18, ' ')}</span> ` +
          `<span class="a-arg">${arg}</span>` +
          (tail ? `   <span class="a-hash">${tail}</span>` : '');
        feed.appendChild(line);
        // stagger the in-animation
        setTimeout(() => line.classList.add('is-in'), 40 * i);
      }
      if (headEl) headEl.textContent = hashes[cursor % hashes.length];
      cursor++;
    }

    render();
    setInterval(render, 2400);
  }

  // ─── footer year ─────────────────────────────────────────────────────
  const y = document.getElementById('year');
  if (y) y.textContent = new Date().getFullYear();
})();
