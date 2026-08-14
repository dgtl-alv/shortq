const $ = s => document.querySelector(s);
let currentUser = null;
let tenantCache = [];

function showMsg(x, type = 'error') {
  const el = $('#msg');
  if (!el) return;
  if (!x) { el.textContent = ''; el.className = 'notice'; return; }
  const text = typeof x === 'string' ? x : (x.error || JSON.stringify(x, null, 2));
  el.textContent = text;
  el.className = `notice ${type}`;
}

async function api(path, opt = {}) {
  opt.headers = { ...(opt.headers || {}), 'Content-Type': 'application/json' };
  opt.credentials = 'same-origin';
  const r = await fetch(path, opt);
  if (r.status === 204) return null;
  const contentType = r.headers.get('content-type') || '';
  const body = contentType.includes('application/json') ? await r.json().catch(() => ({})) : await r.text();
  if (r.status === 401 && body && body.login_url) { location.href = body.login_url; return; }
  if (!r.ok) throw body || { error: r.statusText };
  return body;
}

// All template innerHTML below uses esc() for user/API text and fixed markup for controls.
function esc(s) { return String(s || '').replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c])); }
function roleLabel(role) { return ({ superadmin: 'admin', tenant: 'department admin', customer: 'user' })[role] || role; }
function data(e) { return Object.fromEntries(new FormData(e.target).entries()); }
function login() { location.href = '/auth/microsoft/login'; }
function logout() { location.href = '/auth/logout'; }
function register(e) { e.preventDefault(); login(); }
function forgot(e) { e.preventDefault(); login(); }
function changePass(e) { e.preventDefault(); showMsg('Password auth disabled. Use Microsoft SSO.'); }
function setAuthNav(loggedIn) {
  $('#loginTab').hidden = loggedIn;
  $('#logoutTab').hidden = !loggedIn;
}
function showLogin() {
  setAuthNav(false);
  $('#dash').hidden = true;
  $('#forms').hidden = false;
}
function showApp() {
  setAuthNav(true);
  $('#forms').hidden = true;
  $('#dash').hidden = false;
}

async function createLink(e) {
  e.preventDefault();
  showMsg('Creating link...', 'ok');
  try {
    const link = await api('/api/v1/links', { method: 'POST', body: JSON.stringify(data(e)) });
    e.target.reset();
    showMsg(`Created: ${link.short_url}`, 'ok');
    await Promise.all([loadLinks(), loadStats()]);
  } catch (x) { showMsg(x); }
}

async function loadLinks() {
  const xs = await api('/api/v1/links') || [];
  $('#links').innerHTML = xs.map(x => `
    <div class="row">
      <div>
        <b>${esc(x.title || x.slug)}</b>
        <a href="${esc(x.short_url)}" target="_blank" rel="noopener">${esc(x.short_url)}</a>
        <div class="tiny">${esc(x.target_url)} · ${x.clicks} clicks</div>
      </div>
      <button class="secondary" onclick="editLink('${encodeURIComponent(JSON.stringify(x))}')">Edit</button>
      <button class="secondary" onclick="qr('${esc(x.short_url)}')">QR</button>
      <button class="danger" onclick="delLink(${x.id})">Delete</button>
    </div>`).join('') || '<div class="empty">No links yet. Create first link above.</div>';
}

async function editLink(encodedLink) {
  const link = JSON.parse(decodeURIComponent(encodedLink));
  const slug = prompt('Slug', link.slug);
  if (slug === null) return;
  const target_url = prompt('Destination URL', link.target_url);
  if (target_url === null) return;
  const title = prompt('Title', link.title || '');
  if (title === null) return;
  try {
    await api('/api/v1/links/' + link.id, { method: 'PUT', body: JSON.stringify({ slug, url: target_url, title }) });
    showMsg('Link updated.', 'ok');
    await Promise.all([loadLinks(), loadStats()]);
  } catch (x) { showMsg(x); }
}

async function delLink(id) {
  if (!confirm('Delete this short link?')) return;
  await api('/api/v1/links/' + id, { method: 'DELETE' });
  showMsg('Link deleted.', 'ok');
  await Promise.all([loadLinks(), loadStats()]);
}

async function createKey(e) {
  e.preventDefault();
  try {
    const r = await api('/api/v1/api-keys', { method: 'POST', body: JSON.stringify(data(e)) });
    $('#newKey').textContent = 'Copy now. Shown once:\n' + r.key;
    e.target.reset();
    await loadKeys();
  } catch (x) { showMsg(x); }
}

async function loadKeys() {
  const xs = await api('/api/v1/api-keys') || [];
  $('#keys').innerHTML = xs.map(k => `
    <div class="row one-action">
      <div><b>${esc(k.name)}</b><div class="tiny">${esc(k.prefix)} · ${new Date(k.created_at).toLocaleString()}</div></div>
      <button class="danger" onclick="delKey(${k.id})">Revoke</button>
    </div>`).join('') || '<div class="empty">No API keys.</div>';
}

async function delKey(id) {
  if (!confirm('Revoke this API key?')) return;
  await api('/api/v1/api-keys/' + id, { method: 'DELETE' });
  showMsg('API key revoked.', 'ok');
  await loadKeys();
}

async function loadStats() {
  const a = await api('/api/v1/analytics');
  const entries = Object.entries(a).filter(([, v]) => Number(v) !== 0);
  $('#stats').innerHTML = entries.map(([k, v]) => `<div><b>${v}</b><span>${k.replaceAll('_', ' ')}</span></div>`).join('') || '<div><b>0</b><span>No activity yet</span></div>';
}

async function loadTenants() {
  if (currentUser.role !== 'superadmin') return;
  tenantCache = await api('/api/v1/tenants') || [];
  $('#tenantPanel').hidden = false;
  $('#tenants').innerHTML = tenantCache.map(t => `<div class="row no-actions"><div><b>${esc(t.name)}</b><div class="tiny">id ${t.id} · ${esc(t.slug)}</div></div></div>`).join('') || '<div class="empty">No departments.</div>';
}

async function createTenant(e) {
  e.preventDefault();
  try {
    await api('/api/v1/tenants', { method: 'POST', body: JSON.stringify(data(e)) });
    e.target.reset();
    showMsg('Department created.', 'ok');
    await Promise.all([loadTenants(), loadStats()]);
    setupCustomerForm();
    await loadDomains();
  } catch (x) { showMsg(x); }
}

function setupCustomerForm() {
  if (!currentUser || currentUser.role === 'customer') return;
  const role = $('#customerRole');
  const tenant = $('#customerTenant');
  if (currentUser.role === 'tenant') {
    role.innerHTML = '<option value="customer">user</option>';
    role.disabled = true;
    tenant.innerHTML = `<option value="${currentUser.tenant_id || ''}">department ${currentUser.tenant_id || '-'}</option>`;
    tenant.disabled = true;
    return;
  }
  role.disabled = false;
  role.innerHTML = '<option value="customer">user</option><option value="tenant">department admin</option>';
  tenant.disabled = false;
  tenant.innerHTML = '<option value="">select department</option>' + tenantCache.map(t => `<option value="${t.id}">${esc(t.name)} (${esc(t.slug)})</option>`).join('');
}

async function loadDomains() {
  if (currentUser.role === 'customer') return;
  const xs = await api('/api/v1/domains') || [];
  $('#domainPanel').hidden = false;
  $('#domainTenant').innerHTML = currentUser.role === 'tenant'
    ? `<option value="${currentUser.tenant_id || ''}">department ${currentUser.tenant_id || '-'}</option>`
    : '<option value="">select department</option>' + tenantCache.map(t => `<option value="${t.id}">${esc(t.name)} (${esc(t.slug)})</option>`).join('');
  $('#domains').innerHTML = xs.map(d => `
    <div class="row">
      <div><b>${esc(d.domain)}</b><div class="tiny">${esc(d.status)} · department ${d.tenant_id}<br>TXT _shortq.${esc(d.domain)} = shortq-verify=${esc(d.verification_token)}</div></div>
      <button class="secondary" onclick="verifyDomain(${d.id})">Verify</button>
      <button class="danger" onclick="delDomain(${d.id})">Delete</button>
    </div>`).join('') || '<div class="empty">No custom domains.</div>';
}

async function createDomain(e) {
  e.preventDefault();
  const d = data(e);
  if (currentUser.role === 'tenant') d.tenant_id = currentUser.tenant_id;
  d.tenant_id = Number(d.tenant_id);
  try {
    await api('/api/v1/domains', { method: 'POST', body: JSON.stringify(d) });
    e.target.reset();
    showMsg('Domain added. Add DNS TXT/CNAME then verify.', 'ok');
    await loadDomains();
  } catch (x) { showMsg(x); }
}
async function verifyDomain(id) {
  try { showMsg(await api('/api/v1/domains/' + id + '/verify', { method: 'POST', body: '{}' }), 'ok'); await loadDomains(); }
  catch (x) { showMsg(x); }
}
async function delDomain(id) {
  if (!confirm('Delete this domain?')) return;
  await api('/api/v1/domains/' + id, { method: 'DELETE' });
  showMsg('Domain deleted.', 'ok');
  await loadDomains();
}

async function loadCustomers() {
  if (currentUser.role === 'customer') return;
  setupCustomerForm();
  const xs = await api('/api/v1/customers') || [];
  $('#customerPanel').hidden = false;
  $('#customers').innerHTML = xs.map(u => `<div class="row no-actions"><div><b>${esc(u.name)}</b><div class="tiny">${esc(u.email)} · ${esc(roleLabel(u.role))} · department ${u.tenant_id || '-'}</div></div></div>`).join('') || '<div class="empty">No users.</div>';
}
async function createCustomer(e) {
  e.preventDefault();
  const d = data(e);
  if (currentUser.role === 'tenant') { d.role = 'customer'; d.tenant_id = currentUser.tenant_id; }
  if (d.tenant_id) d.tenant_id = Number(d.tenant_id); else delete d.tenant_id;
  try {
    await api('/api/v1/customers', { method: 'POST', body: JSON.stringify(d) });
    e.target.reset();
    showMsg('User created.', 'ok');
    setupCustomerForm();
    await Promise.all([loadCustomers(), loadStats()]);
  } catch (x) { showMsg(x); }
}

function qr(t) {
  $('#qrText').value = t;
  makeQR();
  $('#qrText').scrollIntoView({ behavior: 'smooth', block: 'center' });
}
function makeQR() {
  const t = ($('#qrText').value || '').trim();
  if (!t) { showMsg('Enter text or URL for QR.'); return; }
  $('#qrImg').src = '/api/v1/qr?text=' + encodeURIComponent(t) + '&_=' + Date.now();
}

async function showDash() {
  currentUser = await api('/api/v1/me');
  if (!currentUser) return;
  showApp();
  $('#me').textContent = `${currentUser.name} · ${currentUser.email} · ${roleLabel(currentUser.role)}`;
  await loadTenants();
  setupCustomerForm();
  const tasks = [loadStats(), loadLinks(), loadKeys(), loadCustomers(), loadDomains()];
  const results = await Promise.allSettled(tasks);
  const failed = results.find(r => r.status === 'rejected');
  if (failed) showMsg(failed.reason);
}

async function restoreSession() {
  try { await showDash(); }
  catch (e) { showLogin(); if (e && e.error && e.error !== 'sso required') showMsg(e); }
}
restoreSession();
