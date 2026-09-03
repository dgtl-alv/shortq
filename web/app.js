const $ = s => document.querySelector(s);
let currentUser = null;
let tenantCache = [];
let linkCache = [];

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

function confirmAction({ title, message, confirmLabel = 'Confirm', danger = false }) {
  const dialog = $('#confirmDialog');
  $('#confirmTitle').textContent = title;
  $('#confirmMessage').textContent = message;
  const confirmButton = $('#confirmButton');
  confirmButton.textContent = confirmLabel;
  confirmButton.classList.toggle('danger', danger);
  return new Promise(resolve => {
    const onClose = () => resolve(dialog.returnValue === 'confirm');
    dialog.addEventListener('close', onClose, { once: true });
    dialog.showModal();
  });
}

function renderDashboardMode() {
  const canSwitch = Boolean(currentUser && currentUser.can_switch_mode);
  $('#modeSwitcher').hidden = !canSwitch;
  $('#adminSection').hidden = !currentUser || currentUser.dashboard_mode !== 'admin';
  $('#adminModeButton').classList.toggle('active', currentUser && currentUser.dashboard_mode === 'admin');
  $('#userModeButton').classList.toggle('active', currentUser && currentUser.dashboard_mode === 'user');
}

async function setDashboardMode(mode) {
  if (!currentUser || currentUser.dashboard_mode === mode) return;
  showMsg('Switching dashboard mode...', 'ok');
  try {
    currentUser = await api('/api/v1/session/mode', { method: 'POST', body: JSON.stringify({ mode }) });
    await renderDashboard();
    showMsg('Now acting as ' + mode + '.', 'ok');
  } catch (x) { showMsg(x); }
}

async function createLink(e) {
  e.preventDefault();
  showMsg('Creating link...', 'ok');
  try {
    const link = await api('/api/v1/links', { method: 'POST', body: JSON.stringify(linkPayloadFromForm(e.target, true)) });
    e.target.reset();
    showMsg(`Created: ${link.short_url}`, 'ok');
    await Promise.all([loadLinks(), loadStats()]);
  } catch (x) { showMsg(x); }
}

async function loadLinks() {
  const page = await api('/api/v1/links/page?limit=100') || { items: [] };
  const xs = page.items || [];
  linkCache = xs;
  $('#links').innerHTML = xs.map(x => `
    <div class="row link-row">
      <div>
        <b>${esc(x.title || x.slug)}</b>
        <a href="${esc(x.short_url)}" target="_blank" rel="noopener">${esc(x.short_url)}</a>
        <div class="tiny">${esc(x.target_url)} · ${x.clicks} clicks · ${esc(x.visibility === "department" ? "shared with ALVA" : "private")}</div>
        ${x.creator_name ? `<div class="tiny">Created by ${esc(x.creator_name)} &middot; ${esc(x.creator_email)}</div>` : ''}
      </div>
      <button class="secondary" onclick="openLinkReport(${x.id})">Report</button>
      ${canManageLink(x) ? '<button class="secondary" onclick="editLink(' + x.id + ')">Edit</button>' : ''}
      <button class="secondary" onclick="qrForLink(${x.id})">QR</button>
      ${canManageLink(x) ? (currentUser && currentUser.can_delete ? `<button class="danger" onclick="delLink(${x.id})">Delete</button>` : '<button class="danger" disabled title="Deletion access must be activated by a superadmin">Delete locked</button>') : ''}
    </div>`).join('') || '<div class="empty">No links yet. Create first link above.</div>';
}

function linkPayloadFromForm(form, creating = false) {
  const raw = data({ target: form });
  const payload = { url: raw.url || '', title: raw.title || '', visibility: raw.visibility || 'private', redirect_code: Number(raw.redirect_code || 302), forward_query: form.elements.forward_query ? form.elements.forward_query.checked : true };
  if (creating && raw.slug) payload.slug = raw.slug;
  ['expired_url', 'ios_url', 'android_url', 'utm_source', 'utm_medium', 'utm_campaign', 'utm_term', 'utm_content'].forEach(key => { if (key in raw) payload[key] = raw[key] || ''; });
  if (raw.expires_at) payload.expires_at = new Date(raw.expires_at).toISOString(); else if (!creating) payload.expires_at = '';
  if (raw.max_clicks) payload.max_clicks = Number(raw.max_clicks); else if (!creating) payload.clear_max_clicks = true;
  payload.tags = raw.tags ? raw.tags.split(';').map(x => x.trim()).filter(Boolean) : [];
  try { payload.geo_targets = raw.geo_targets ? JSON.parse(raw.geo_targets) : []; } catch { throw { error: 'Country routes must be valid JSON.' }; }
  if (raw.password) payload.password = raw.password;
  if (form.elements.clear_password && form.elements.clear_password.checked) payload.clear_password = true;
  return payload;
}

function editLink(id) {
  const link = linkCache.find(item => item.id === id);
  if (!link) return;
  const form = $('#editLinkForm');
  form.reset();
  $('#editSlug').textContent = '/' + link.slug;
  const values = { id: link.id, url: link.target_url, title: link.title, visibility: link.visibility || 'private', redirect_code: link.redirect_code || 302, expired_url: link.expired_url, ios_url: link.ios_url, android_url: link.android_url, geo_targets: JSON.stringify(link.geo_targets || []), tags: (link.tags || []).join('; '), utm_source: link.utm_source, utm_medium: link.utm_medium, utm_campaign: link.utm_campaign };
  Object.entries(values).forEach(([key, value]) => { if (form.elements[key]) form.elements[key].value = value || ''; });
  if (link.expires_at) form.elements.expires_at.value = new Date(link.expires_at).toISOString().slice(0, 16);
  if (link.max_clicks) form.elements.max_clicks.value = link.max_clicks;
  form.elements.forward_query.checked = Boolean(link.forward_query);
  $('#linkEditor').showModal();
}

async function saveLink(e) {
  e.preventDefault();
  const form = e.target;
  try {
    const result = await api('/api/v1/links/' + form.elements.id.value, { method: 'PATCH', body: JSON.stringify(linkPayloadFromForm(form, false)) });
    $('#linkEditor').close();
    showMsg(`Updated /${result.slug}; the slug was not changed.`, 'ok');
    await Promise.all([loadLinks(), loadStats()]);
  } catch (x) { showMsg(x); }
}

function downloadLinks() { location.href = '/api/v1/links/export.csv'; }
async function importLinks(input) {
  if (!input.files || !input.files[0]) return;
  try {
    const form = new FormData(); form.append('file', input.files[0]);
    const response = await fetch('/api/v1/links/import', { method: 'POST', body: form, credentials: 'same-origin' });
    const result = await response.json(); if (!response.ok) throw result;
    showMsg(`Imported ${result.imported} links.`, result.errors && Object.keys(result.errors).length ? 'error' : 'ok');
    await Promise.all([loadLinks(), loadStats()]);
  } catch (x) { showMsg(x); } finally { input.value = ''; }
}

async function delLink(id) {
  const link = linkCache.find(item => item.id === id);
  if (!await confirmAction({ title: 'Delete short link?', message: `/${link ? link.slug : 'this link'} will stop redirecting immediately. Its analytics will be retained.`, confirmLabel: 'Delete link', danger: true })) return;
  try {
    await api('/api/v1/links/' + id, { method: 'DELETE' });
    showMsg('Link deleted.', 'ok');
    await Promise.all([loadLinks(), loadStats()]);
  } catch (x) { showMsg(x); }
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
      <div><b>${esc(k.name)}</b><div class="tiny">${esc(k.prefix)} · ${esc(k.scope)} scope · ${new Date(k.created_at).toLocaleString()}</div></div>
      ${currentUser && currentUser.can_delete ? `<button class="danger" onclick="delKey(${k.id})">Revoke</button>` : '<button class="danger" disabled title="Deletion access required">Revoke locked</button>'}
    </div>`).join('') || '<div class="empty">No API keys.</div>';
}

async function delKey(id) {
  if (!await confirmAction({ title: 'Revoke API key?', message: 'Any integration using this key will lose access immediately. This action cannot be undone.', confirmLabel: 'Revoke key', danger: true })) return;
  try {
    await api('/api/v1/api-keys/' + id, { method: 'DELETE' });
    showMsg('API key revoked.', 'ok');
    await loadKeys();
  } catch (x) { showMsg(x); }
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
      ${currentUser && currentUser.can_delete ? `<button class="danger" onclick="delDomain(${d.id})">Delete</button>` : '<button class="danger" disabled title="Deletion access required">Delete locked</button>'}
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
  if (!await confirmAction({ title: 'Delete custom domain?', message: 'Short links using this domain may stop working. This action cannot be undone.', confirmLabel: 'Delete domain', danger: true })) return;
  try {
    await api('/api/v1/domains/' + id, { method: 'DELETE' });
    showMsg('Domain deleted.', 'ok');
    await loadDomains();
  } catch (x) { showMsg(x); }
}

async function loadCustomers() {
  if (currentUser.role === 'customer') return;
  setupCustomerForm();
  const xs = await api('/api/v1/customers') || [];
  $('#customerPanel').hidden = false;
  $('#customers').innerHTML = xs.map(u => `<div class="row"><div><b>${esc(u.name)}</b><div class="tiny">${esc(u.email)} · ${esc(roleLabel(u.role))} · department ${u.tenant_id || '-'} · deletion ${u.role === 'superadmin' ? 'always allowed' : (u.deletion_access ? 'enabled' : 'locked')}</div></div><button class="secondary" onclick="openUserReport(${u.id})">Report</button>${currentUser.role === 'superadmin' && u.role !== 'superadmin' ? `<button class="${u.deletion_access ? 'danger' : 'secondary'}" onclick="setDeletionAccess(${u.id}, ${!u.deletion_access})">${u.deletion_access ? 'Disable deletion' : 'Enable deletion'}</button>` : ''}</div>`).join('') || '<div class="empty">No users.</div>';
}
async function setDeletionAccess(id, enabled) {
  if (!await confirmAction({ title: `${enabled ? 'Enable' : 'Disable'} deletion access?`, message: enabled ? 'This user will be able to delete links, domains, and API keys.' : 'This user will immediately lose deletion access.', confirmLabel: enabled ? 'Enable access' : 'Disable access', danger: enabled })) return;
  try { await api(`/api/v1/customers/${id}/deletion-access`, { method: 'PATCH', body: JSON.stringify({ enabled }) }); showMsg('Deletion access updated.', 'ok'); await Promise.all([loadCustomers(), loadAuditEvents()]); } catch (x) { showMsg(x); }
}

async function loadAuditEvents() {
  if (!currentUser || currentUser.role !== 'superadmin') return;
  $('#auditPanel').hidden = false;
  const query = new URLSearchParams(Object.fromEntries(new FormData($('#auditFilters')).entries())); query.set('limit', '100');
  const page = await api('/api/v1/audit-events?' + query.toString()) || { items: [] };
  $('#auditEvents').innerHTML = (page.items || []).map(e => `<div class="row no-actions"><div><b>${esc(e.action)} · ${esc(e.outcome)}</b><div class="tiny">${new Date(e.created_at).toLocaleString()} · ${esc(e.actor_email)} via ${esc(e.auth_type)} · ${esc(e.target_type)} ${esc(e.target_id)} · ${esc(e.ip_address || '-')}</div></div></div>`).join('') || '<div class="empty">No matching audit events.</div>';
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

function qrForLink(id) {
  const link = linkCache.find(item => item.id === id);
  if (!link) { showMsg('The selected link is no longer available.'); return; }
  $('#qrText').value = link.short_url;
  makeQR();
  $('#qrText').scrollIntoView({ behavior: 'smooth', block: 'center' });
}
function makeQR() {
  const t = ($('#qrText').value || '').trim();
  if (!t) { showMsg('Enter text or URL for QR.'); return; }
  const format = $('#qrFormat').value;
  if (format === 'pdf') { showMsg('PDF is ready to download.', 'ok'); return; }
  $('#qrImg').src = qrURL() + '&_=' + Date.now();
}
function qrURL() {
  return '/api/v1/qr?' + new URLSearchParams({ text: ($('#qrText').value || '').trim(), format: $('#qrFormat').value, size: $('#qrSize').value, foreground: $('#qrForeground').value, background: $('#qrBackground').value }).toString();
}
function downloadQR() {
  if (!($('#qrText').value || '').trim()) { showMsg('Enter text or URL for QR.'); return; }
  const link = document.createElement('a'); link.href = qrURL(); link.download = 'shortq-qr.' + $('#qrFormat').value; link.click();
}

async function loadClicks() {
  const [page, breakdown] = await Promise.all([api('/api/v1/clicks?limit=50'), api('/api/v1/analytics/breakdown?group_by=country')]);
  $('#breakdown').innerHTML = (breakdown || []).slice(0, 6).map(x => `<div><b>${x.clicks}</b><span>${esc(x.key || 'unknown')}</span></div>`).join('');
  $('#clicks').innerHTML = (page.items || []).map(x => `<div class="row no-actions"><div><b>/${esc(x.slug)}</b><div class="tiny">${new Date(x.created_at).toLocaleString()} · ${esc(x.country_code || 'unknown')} · ${esc(x.device)} · ${esc(x.browser)} · ${esc(x.route_type)} → ${x.status_code}</div></div></div>`).join('') || '<div class="empty">No click events yet.</div>';
}

async function renderDashboard() {
  showApp();
  renderDashboardMode();
  $('#me').textContent = currentUser.name + ' · ' + currentUser.email + ' · acting as ' + roleLabel(currentUser.role);
  tenantCache = [];
  $('#tenantPanel').hidden = true;
  $('#domainPanel').hidden = true;
  $('#customerPanel').hidden = true;
  $('#auditPanel').hidden = true;
  await loadTenants();
  setupCustomerForm();
  const tasks = [loadStats(), loadLinks(), loadKeys(), loadCustomers(), loadDomains(), loadClicks(), loadAuditEvents()];
  const results = await Promise.allSettled(tasks);
  const failed = results.find(r => r.status === 'rejected');
  if (failed) showMsg(failed.reason);
  await routeView();
}

async function showDash() {
  currentUser = await api('/api/v1/me');
  if (!currentUser) return;
  await renderDashboard();
}

async function restoreSession() {
  try { await showDash(); }
  catch (e) { showLogin(); if (e && e.error && e.error !== 'sso required') showMsg(e); }
}
restoreSession();
