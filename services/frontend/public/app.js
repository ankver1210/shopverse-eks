/* ShopVerse single-page app — plain JavaScript, no build step needed. */
(() => {
  'use strict';

  const $ = (sel, root = document) => root.querySelector(sel);
  const app = $('#app');
  const dialog = $('#product-dialog');

  const store = {
    get token() { return localStorage.getItem('sv_token'); },
    set token(v) { v ? localStorage.setItem('sv_token', v) : localStorage.removeItem('sv_token'); },
    get user() { try { return JSON.parse(localStorage.getItem('sv_user')); } catch { return null; } },
    set user(v) { v ? localStorage.setItem('sv_user', JSON.stringify(v)) : localStorage.removeItem('sv_user'); },
    category: 'all',
    products: [],
    stock: {},
  };
  let platformTimer = null;

  // ------------------------------------------------------------- helpers
  const esc = (value) => String(value ?? '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
  const money = (cents) => (cents == null ? '—' : `$${(cents / 100).toFixed(2)}`);
  const catClass = (category) => `cat-${esc(category || 'home')}`;
  const when = (iso) => new Date(iso).toLocaleString();
  // form.elements.namedItem is explicit; `form.name` would return the form's own name attribute.
  const field = (form, name) => form.elements.namedItem(name);

  function toast(message, type = 'info') {
    const el = document.createElement('div');
    el.className = `toast ${type}`;
    el.textContent = message;
    $('#toasts').appendChild(el);
    setTimeout(() => el.remove(), 4200);
  }

  async function api(path, { method = 'GET', body, auth = false } = {}) {
    const headers = { Accept: 'application/json' };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (auth && store.token) headers.Authorization = `Bearer ${store.token}`;
    const res = await fetch(`/api${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
    let data = null;
    if (res.status !== 204) data = await res.json().catch(() => null);
    if (res.status === 401 && auth) {
      logout(false);
      toast('Please sign in to continue', 'error');
      location.hash = '#/login';
      throw new Error('unauthorized');
    }
    if (!res.ok) {
      const err = new Error((data && (data.error || data.detail)) || `Request failed (${res.status})`);
      err.status = res.status;
      throw err;
    }
    return data;
  }

  // ------------------------------------------------------------- session
  function renderSession() {
    const user = store.user;
    $('#user-chip').hidden = !user;
    $('#user-chip').textContent = user ? `Hi, ${user.name.split(' ')[0]}` : '';
    $('#login-link').hidden = Boolean(user);
    $('#logout-btn').hidden = !user;
  }

  function logout(notify = true) {
    store.token = null;
    store.user = null;
    setCartCount(0);
    renderSession();
    if (notify) toast('Signed out');
  }

  function setCartCount(n) { $('#cart-count').textContent = String(n); }

  async function refreshCartCount() {
    if (!store.token) return setCartCount(0);
    try {
      const cart = await api('/cart', { auth: true });
      setCartCount(cart.item_count);
    } catch { /* ignore */ }
    return undefined;
  }

  async function addToCart(productId, quantity = 1) {
    if (!store.token) {
      toast('Sign in to add items to your cart');
      location.hash = '#/login';
      return;
    }
    try {
      const cart = await api('/cart/items', { method: 'POST', body: { product_id: productId, quantity }, auth: true });
      setCartCount(cart.item_count);
      toast('Added to cart ✓');
    } catch (err) {
      if (err.message !== 'unauthorized') toast(err.message, 'error');
    }
  }

  // ------------------------------------------------------------- views
  function stockBadge(productId) {
    const s = store.stock[productId];
    if (s === undefined) return '<span class="stock">…</span>';
    if (s <= 0) return '<span class="stock out">Sold out</span>';
    if (s <= 5) return `<span class="stock low">Only ${s} left</span>`;
    return `<span class="stock">${s} in stock</span>`;
  }

  async function viewShop() {
    app.innerHTML = `
      <section class="hero">
        <div>
          <h1>Gear for builders.</h1>
          <p class="muted">Every click on this page travels through an AWS ALB, an API gateway and several microservices running on Kubernetes.</p>
        </div>
        <div class="chips" id="chips" role="group" aria-label="Filter by category"></div>
      </section>
      <section class="grid" id="grid">${'<div class="skeleton"></div>'.repeat(8)}</section>`;
    try {
      const [{ categories }, { products }] = await Promise.all([api('/categories'), api('/products')]);
      store.products = products;
      const chips = ['all', ...categories];
      $('#chips').innerHTML = chips.map((c) => `<button class="chip" data-cat="${esc(c)}" aria-pressed="${c === store.category}">${esc(c[0].toUpperCase() + c.slice(1))}</button>`).join('');
      renderGrid();
      const ids = products.map((p) => p.id).join(',');
      api(`/inventory?ids=${ids}`).then(({ items }) => {
        items.forEach((i) => { store.stock[i.product_id] = i.available; });
        renderGrid();
      }).catch(() => toast('Inventory service unavailable — stock levels hidden', 'error'));
    } catch (err) {
      $('#grid').innerHTML = `<div class="panel empty"><span class="e">🛠️</span>Could not load products: ${esc(err.message)}<br><span class="muted">Check the Platform page to see which service is down.</span></div>`;
    }
  }

  function renderGrid() {
    const grid = $('#grid');
    if (!grid) return;
    const list = store.category === 'all' ? store.products : store.products.filter((p) => p.category === store.category);
    grid.innerHTML = list.map((p) => `
      <article class="card">
        <button class="tile" data-open="${p.id}" aria-label="View ${esc(p.name)}">
          <div class="tile-art ${catClass(p.category)}" aria-hidden="true">${esc(p.emoji)}</div>
        </button>
        <div class="card-body">
          <div class="card-title">${esc(p.name)}</div>
          <div class="muted">${esc(p.short_description)}</div>
          <div class="card-meta">
            <span class="price">${money(p.price_cents)}</span>
            ${stockBadge(p.id)}
          </div>
          <button class="btn btn-small" data-add="${p.id}" ${store.stock[p.id] === 0 ? 'disabled' : ''}>Add to cart</button>
        </div>
      </article>`).join('');
  }

  async function openProduct(id) {
    const p = store.products.find((x) => x.id === Number(id));
    if (!p) return;
    dialog.innerHTML = `
      <button class="dialog-close" data-close aria-label="Close">✕</button>
      <div class="dialog-art ${catClass(p.category)}" aria-hidden="true">${esc(p.emoji)}</div>
      <div class="dialog-body">
        <h2 id="dialog-title">${esc(p.name)}</h2>
        <p class="muted">${esc(p.description)}</p>
        <div class="card-meta"><span class="price">${money(p.price_cents)}</span>${stockBadge(p.id)}</div>
        <div class="qty-row">
          <label for="qty" class="sr-only">Quantity</label>
          <input id="qty" type="number" min="1" max="10" value="1">
          <button class="btn" data-add-qty="${p.id}" ${store.stock[p.id] === 0 ? 'disabled' : ''}>Add to cart</button>
        </div>
        <h2>You might also like</h2>
        <div class="recs" id="recs"><span class="muted">Asking recommendation-service…</span></div>
      </div>`;
    if (!dialog.open) dialog.showModal();
    try {
      const { recommendations, strategy } = await api(`/recommendations/${p.id}?limit=4`);
      $('#recs').innerHTML = recommendations.map((r) => `
        <button class="rec" data-open="${r.id}">
          <div class="e" aria-hidden="true">${esc(r.emoji)}</div>
          <div>${esc(r.name)}</div><div class="muted">${money(r.price_cents)}</div>
        </button>`).join('') + `<div class="muted mono">strategy: ${esc(strategy)}</div>`;
    } catch {
      $('#recs').innerHTML = '<span class="muted">Recommendations unavailable right now (the rest of the store still works — that is graceful degradation).</span>';
    }
  }

  async function viewCart() {
    if (!store.token) { location.hash = '#/login'; return; }
    app.innerHTML = '<h1>Your cart</h1><div class="skeleton"></div>';
    let cart;
    try { cart = await api('/cart', { auth: true }); } catch (err) {
      if (err.message !== 'unauthorized') app.innerHTML = `<h1>Your cart</h1><div class="panel empty"><span class="e">🛠️</span>${esc(err.message)}</div>`;
      return;
    }
    setCartCount(cart.item_count);
    if (!cart.items.length) {
      app.innerHTML = '<h1>Your cart</h1><div class="panel empty"><span class="e">🛒</span>Your cart is empty.<br><br><a class="btn" href="#/">Browse products</a></div>';
      return;
    }
    const shipping = cart.subtotal_cents >= 10000 ? 0 : 799;
    app.innerHTML = `
      <h1>Your cart</h1>
      <p class="muted">Stored in Redis by cart-service. Prices come live from product-service.</p>
      <div class="layout-2">
        <section class="panel">
          ${cart.degraded ? '<p class="notice">product-service is unavailable, showing a degraded cart.</p>' : ''}
          ${cart.items.map((i) => `
            <div class="line-item">
              <div class="e" aria-hidden="true">${esc(i.emoji)}</div>
              <div><strong>${esc(i.name)}</strong><div class="muted">${money(i.price_cents)} each</div></div>
              <input type="number" min="0" max="10" value="${i.quantity}" data-qty="${i.product_id}" aria-label="Quantity for ${esc(i.name)}">
              <button class="btn btn-small btn-ghost" data-remove="${i.product_id}" aria-label="Remove ${esc(i.name)}">✕</button>
            </div>`).join('')}
          <div class="totals"><span>Subtotal</span><span>${money(cart.subtotal_cents)}</span></div>
          <div class="muted">Shipping ${shipping === 0 ? 'free (orders over $100)' : money(shipping)}</div>
        </section>
        <form class="panel" id="checkout">
          <h2>Checkout</h2>
          <label for="addr">Shipping address</label>
          <input id="addr" name="address" required value="1 Kubernetes Way, Seattle, WA">
          <label for="pm">Payment method (simulated — no real payments)</label>
          <select id="pm" name="payment_method">
            <option value="tok_visa">Test card · always approved</option>
            <option value="tok_declined">Test card · always declined</option>
          </select>
          <p class="notice">Placing an order runs a saga: order-service → inventory-service (reserve stock) → payment-service (charge) → events to RabbitMQ → notification-service.</p>
          <button class="btn btn-block" type="submit">Place order · ${money(cart.subtotal_cents + shipping)}</button>
        </form>
      </div>`;
  }

  async function placeOrder(form) {
    const button = form.querySelector('button[type=submit]');
    button.disabled = true;
    button.textContent = 'Placing order…';
    try {
      const order = await api('/orders', {
        method: 'POST', auth: true,
        body: { payment_method: field(form, 'payment_method').value, shipping_address: field(form, 'address').value },
      });
      setCartCount(0);
      toast(`Order ${order.id.slice(0, 8)} confirmed 🎉`);
      location.hash = '#/orders';
    } catch (err) {
      if (err.message !== 'unauthorized') toast(err.message, 'error');
      button.disabled = false;
      button.textContent = 'Try again';
    }
  }

  async function viewOrders() {
    if (!store.token) { location.hash = '#/login'; return; }
    app.innerHTML = '<h1>Orders</h1><div class="skeleton"></div>';
    try {
      const { orders } = await api('/orders', { auth: true });
      app.innerHTML = `<h1>Orders</h1><p class="muted">Stored in PostgreSQL by order-service.</p>
        <section class="panel">${orders.length ? orders.map((o) => `
          <div class="order">
            <div class="order-head">
              <div><strong class="mono">#${esc(o.id.slice(0, 8))}</strong> · <span class="muted">${when(o.created_at)}</span></div>
              <div><span class="status ${esc(o.status)}">${esc(o.status)}</span> <strong>${money(o.total_cents)}</strong></div>
            </div>
            <div class="muted">${o.items.map((i) => `${esc(i.quantity)} × ${esc(i.name)}`).join(' · ')}</div>
            ${o.failure_reason ? `<div class="muted">Reason: ${esc(o.failure_reason)}</div>` : ''}
          </div>`).join('') : '<div class="empty"><span class="e">📦</span>No orders yet.</div>'}
        </section>`;
    } catch (err) {
      if (err.message !== 'unauthorized') app.innerHTML = `<h1>Orders</h1><div class="panel empty">${esc(err.message)}</div>`;
    }
  }

  async function viewNotifications() {
    if (!store.token) { location.hash = '#/login'; return; }
    app.innerHTML = '<h1>Inbox</h1><div class="skeleton"></div>';
    try {
      const { notifications } = await api('/notifications', { auth: true });
      app.innerHTML = `<h1>Inbox</h1><p class="muted">Delivered asynchronously: services publish events to RabbitMQ, notification-service consumes them.</p>
        <section class="panel">${notifications.length ? notifications.map((n) => `
          <div class="order">
            <div class="order-head"><strong>${esc(n.subject)}</strong><span class="muted">${when(n.created_at)}</span></div>
            <div class="muted">${esc(n.body)}</div>
          </div>`).join('') : '<div class="empty"><span class="e">📭</span>No notifications yet. Place an order!</div>'}
        </section>`;
    } catch (err) {
      if (err.message !== 'unauthorized') app.innerHTML = `<h1>Inbox</h1><div class="panel empty">${esc(err.message)}</div>`;
    }
  }

  function viewLogin() {
    app.innerHTML = `
      <section class="panel auth-card">
        <div class="tabs">
          <button class="chip" data-mode="login" aria-pressed="true">Sign in</button>
          <button class="chip" data-mode="register" aria-pressed="false">Create account</button>
        </div>
        <form id="auth-form" data-mode="login">
          <div id="name-field" hidden>
            <label for="name">Full name</label>
            <input id="name" name="fullname" autocomplete="name">
          </div>
          <label for="email">Email</label>
          <input id="email" name="email" type="email" required autocomplete="email">
          <label for="password">Password (min 8 characters)</label>
          <input id="password" name="password" type="password" minlength="8" required autocomplete="current-password">
          <br><br>
          <button class="btn btn-block" type="submit">Sign in</button>
          <p class="notice">This is a demo. Use a made-up email like <span class="mono">you@example.com</span>. Passwords are hashed with bcrypt by user-service.</p>
        </form>
      </section>`;
  }

  async function submitAuth(form) {
    const mode = form.dataset.mode;
    const body = { email: field(form, 'email').value.trim(), password: field(form, 'password').value };
    if (mode === 'register') body.name = field(form, 'fullname').value.trim() || body.email.split('@')[0];
    try {
      const res = await api(`/auth/${mode}`, { method: 'POST', body });
      store.token = res.access_token;
      store.user = res.user;
      renderSession();
      refreshCartCount();
      toast(mode === 'register' ? `Welcome to ShopVerse, ${res.user.name}!` : 'Signed in ✓');
      location.hash = '#/';
    } catch (err) {
      toast(err.message, 'error');
    }
  }

  async function viewPlatform() {
    app.innerHTML = `
      <section class="hero">
        <div>
          <h1>Platform status</h1>
          <p class="muted">Live readiness of every microservice, polled through the api-gateway every 3 seconds. The <em>pod</em> field shows which replica answered — refresh and watch the load balancing.</p>
        </div>
        <span class="muted mono" id="checked"></span>
      </section>
      <section class="svc-grid" id="svcs">${'<div class="skeleton"></div>'.repeat(6)}</section>
      <section class="panel tip">
        <h2>Try this</h2>
        <p>Break something and watch this page react:</p>
        <p><code>kubectl -n shopverse scale deploy recommendation-service --replicas=0</code></p>
        <p class="muted">The store keeps working without recommendations (graceful degradation). Scale it back to 2 to recover. See docs/05-HANDS-ON-LABS.md for 15 more experiments.</p>
      </section>`;
    const poll = async () => {
      if (location.hash !== '#/platform') return;
      try {
        const status = await api('/status');
        const all = [{ name: 'api-gateway', status: 'up', version: status.gateway.version, pod: status.gateway.pod, latency_ms: 0, circuit: '—' }, ...status.services];
        $('#svcs').innerHTML = all.map((s) => `
          <article class="svc ${esc(s.status)}">
            <h3>${esc(s.name)} <span class="status ${esc(s.status)}">${esc(s.status)}</span></h3>
            <dl>
              <dt>version</dt><dd class="mono">${esc(s.version || '—')}</dd>
              <dt>pod</dt><dd class="mono">${esc(s.pod || '—')}</dd>
              <dt>latency</dt><dd>${esc(s.latency_ms)} ms</dd>
              <dt>circuit</dt><dd>${esc(s.circuit)}</dd>
              ${s.checks && Object.keys(s.checks).length ? `<dt>deps</dt><dd>${Object.entries(s.checks).map(([k, v]) => `${esc(k)}: ${esc(v)}`).join(', ')}</dd>` : ''}
              ${s.error ? `<dt>error</dt><dd>${esc(s.error)}</dd>` : ''}
            </dl>
          </article>`).join('');
        $('#checked').textContent = `checked ${new Date(status.checked_at).toLocaleTimeString()}`;
      } catch (err) {
        $('#svcs').innerHTML = `<div class="panel empty"><span class="e">🚨</span>api-gateway unreachable: ${esc(err.message)}</div>`;
      }
    };
    await poll();
    platformTimer = setInterval(poll, 3000);
  }

  // ------------------------------------------------------------- router
  const routes = {
    '': viewShop, '#/': viewShop, '#/cart': viewCart, '#/orders': viewOrders,
    '#/notifications': viewNotifications, '#/login': viewLogin, '#/platform': viewPlatform,
  };

  function route() {
    clearInterval(platformTimer);
    if (dialog.open) dialog.close();
    const view = routes[location.hash] || viewShop;
    const key = (location.hash.replace('#/', '') || 'shop');
    document.querySelectorAll('[data-nav]').forEach((a) => a.classList.toggle('active', a.dataset.nav === key));
    view();
    app.focus({ preventScroll: true });
  }

  // ------------------------------------------------------------- events
  document.addEventListener('click', (e) => {
    const t = e.target.closest('button, a');
    if (!t) return;
    if (t.dataset.cat) {
      store.category = t.dataset.cat;
      document.querySelectorAll('[data-cat]').forEach((c) => c.setAttribute('aria-pressed', String(c.dataset.cat === store.category)));
      renderGrid();
    } else if (t.dataset.open) {
      openProduct(t.dataset.open);
    } else if (t.dataset.add) {
      addToCart(Number(t.dataset.add));
    } else if (t.dataset.addQty) {
      addToCart(Number(t.dataset.addQty), Math.max(1, Math.min(10, Number($('#qty').value) || 1)));
    } else if (t.dataset.close !== undefined) {
      dialog.close();
    } else if (t.dataset.remove) {
      api(`/cart/items/${t.dataset.remove}`, { method: 'DELETE', auth: true }).then(viewCart).catch((err) => toast(err.message, 'error'));
    } else if (t.dataset.mode) {
      const form = $('#auth-form');
      form.dataset.mode = t.dataset.mode;
      document.querySelectorAll('[data-mode]').forEach((b) => b.setAttribute('aria-pressed', String(b === t)));
      $('#name-field').hidden = t.dataset.mode !== 'register';
      form.querySelector('button[type=submit]').textContent = t.dataset.mode === 'register' ? 'Create account' : 'Sign in';
    } else if (t.id === 'logout-btn') {
      logout();
      location.hash = '#/';
    }
  });

  document.addEventListener('change', (e) => {
    const input = e.target;
    if (input.dataset.qty) {
      api(`/cart/items/${input.dataset.qty}`, { method: 'PUT', auth: true, body: { quantity: Number(input.value) } })
        .then(viewCart).catch((err) => toast(err.message, 'error'));
    }
  });

  document.addEventListener('submit', (e) => {
    e.preventDefault();
    if (e.target.id === 'checkout') placeOrder(e.target);
    if (e.target.id === 'auth-form') submitAuth(e.target);
  });

  dialog.addEventListener('click', (e) => { if (e.target === dialog) dialog.close(); });
  window.addEventListener('hashchange', route);

  fetch('/config.json').then((r) => r.json()).then((cfg) => {
    $('#build-info').textContent = `frontend ${cfg.version} · pod ${cfg.pod} · ${cfg.environment}`;
  }).catch(() => {});

  renderSession();
  refreshCartCount();
  route();
})();
