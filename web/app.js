const $ = s => document.querySelector(s);
let currentUser = null;
let tenantCache = [];
function msg(x){ $('#msg').textContent = typeof x === 'string' ? x : JSON.stringify(x,null,2); }
async function api(path,opt={}){ opt.headers = {...(opt.headers||{}),'Content-Type':'application/json'}; opt.credentials = 'same-origin'; let r = await fetch(path,opt); if(r.status===204) return null; let j = await r.json().catch(()=>({})); if(r.status===401 && j.login_url){ location.href = j.login_url; return; } if(!r.ok) throw j; return j; }
function setAuthNav(loggedIn){ $('#loginTab').hidden = loggedIn; $('#registerTab').hidden = true; $('#forgotTab').hidden = true; $('#logoutTab').hidden = !loggedIn; }
function tab(){ setAuthNav(false); $('#dash').hidden = true; $('#forms').innerHTML = `<h2>Microsoft SSO</h2><p>Dashboard access uses Electraauto Microsoft tenant SSO.</p><button onclick="login()">Sign in with Microsoft</button>`; }
function data(e){ return Object.fromEntries(new FormData(e.target).entries()); }
function login(){ location.href = '/auth/microsoft/login'; }
function logout(){ location.href = '/auth/logout'; }
async function register(e){ e.preventDefault(); login(); }
async function forgot(e){ e.preventDefault(); login(); }
async function changePass(e){ e.preventDefault(); msg('Password auth disabled. Use Microsoft SSO.'); }
async function createLink(e){ e.preventDefault(); try{ await api('/api/v1/links',{method:'POST',body:JSON.stringify(data(e))}); e.target.reset(); await loadLinks(); await loadStats(); }catch(x){ msg(x); } }
async function loadLinks(){ let xs = await api('/api/v1/links'); $('#links').innerHTML = xs.map(x=>`<div class="row"><div><b>${esc(x.title||x.slug)}</b><br><a href="${x.short_url}" target="_blank">${x.short_url}</a><div class="tiny">${esc(x.target_url)} · ${x.clicks} clicks · user ${x.user_id}</div></div><button onclick="qr('${x.short_url}')">QR</button><button onclick="delLink(${x.id})">Delete</button></div>`).join('') || 'No links'; }
async function delLink(id){ await api('/api/v1/links/'+id,{method:'DELETE'}); await loadLinks(); await loadStats(); }
async function createKey(e){ e.preventDefault(); try{ let r = await api('/api/v1/api-keys',{method:'POST',body:JSON.stringify(data(e))}); $('#newKey').textContent = 'Copy now, shown once:\n'+r.key; e.target.reset(); await loadKeys(); }catch(x){ msg(x); } }
async function loadKeys(){ let xs = await api('/api/v1/api-keys'); $('#keys').innerHTML = xs.map(k=>`<div class="row"><div><b>${esc(k.name)}</b><div class="tiny">${k.prefix} · created ${new Date(k.created_at).toLocaleString()}</div></div><button onclick="delKey(${k.id})">Revoke</button></div>`).join('') || 'No keys'; }
async function delKey(id){ await api('/api/v1/api-keys/'+id,{method:'DELETE'}); await loadKeys(); }
async function loadStats(){ let a = await api('/api/v1/analytics'); $('#stats').innerHTML = Object.entries(a).filter(([k,v])=>v!==0).map(([k,v])=>`<div><b>${v}</b><span>${k.replaceAll('_',' ')}</span></div>`).join(''); }
async function loadTenants(){ if(currentUser.role !== 'superadmin') return; tenantCache = await api('/api/v1/tenants'); $('#tenantPanel').hidden = false; $('#tenants').innerHTML = tenantCache.map(t=>`<div class="row"><div><b>${esc(t.name)}</b><div class="tiny">id ${t.id} · ${esc(t.slug)}</div></div></div>`).join('') || 'No tenants'; }
async function createTenant(e){ e.preventDefault(); try{ await api('/api/v1/tenants',{method:'POST',body:JSON.stringify(data(e))}); e.target.reset(); await loadTenants(); setupCustomerForm(); await loadStats(); }catch(x){ msg(x); } }
function setupCustomerForm(){
  if(currentUser.role === 'customer') return;
  let role = $('#customerRole');
  let tenant = $('#customerTenant');
  if(currentUser.role === 'tenant'){
    role.innerHTML = '<option value="customer">customer</option>';
    role.disabled = true;
    tenant.innerHTML = `<option value="${currentUser.tenant_id||''}">tenant ${currentUser.tenant_id||'-'}</option>`;
    tenant.disabled = true;
    return;
  }
  role.disabled = false;
  role.innerHTML = '<option value="customer">customer</option><option value="tenant">tenant</option>';
  tenant.disabled = false;
  tenant.innerHTML = '<option value="">select tenant</option>' + tenantCache.map(t=>`<option value="${t.id}">${esc(t.name)} (${esc(t.slug)})</option>`).join('');
}
async function loadDomains(){ if(currentUser.role === 'customer') return; let xs = await api('/api/v1/domains'); $('#domainPanel').hidden = false; $('#domainTenant').innerHTML = currentUser.role === 'tenant' ? `<option value="${currentUser.tenant_id||''}">tenant ${currentUser.tenant_id||'-'}</option>` : '<option value="">select tenant</option>' + tenantCache.map(t=>`<option value="${t.id}">${esc(t.name)} (${esc(t.slug)})</option>`).join(''); $('#domains').innerHTML = xs.map(d=>`<div class="row"><div><b>${esc(d.domain)}</b><div class="tiny">${d.status} · tenant ${d.tenant_id}<br>CNAME ${esc(d.domain)} → ${location.hostname}<br>or TXT _shortq.${esc(d.domain)} = shortq-verify=${esc(d.verification_token)}</div></div><button onclick="verifyDomain(${d.id})">Verify</button><button onclick="delDomain(${d.id})">Delete</button></div>`).join('') || 'No custom domains'; }
async function createDomain(e){ e.preventDefault(); let d = data(e); if(currentUser.role === 'tenant') d.tenant_id = currentUser.tenant_id; d.tenant_id = Number(d.tenant_id); try{ await api('/api/v1/domains',{method:'POST',body:JSON.stringify(d)}); e.target.reset(); await loadDomains(); }catch(x){ msg(x); } }
async function verifyDomain(id){ try{ msg(await api('/api/v1/domains/'+id+'/verify',{method:'POST',body:'{}'})); await loadDomains(); await loadLinks(); }catch(x){ msg(x); } }
async function delDomain(id){ await api('/api/v1/domains/'+id,{method:'DELETE'}); await loadDomains(); await loadLinks(); }
async function loadCustomers(){ if(currentUser.role === 'customer') return; setupCustomerForm(); let xs = await api('/api/v1/customers'); $('#customerPanel').hidden = false; $('#customers').innerHTML = xs.map(u=>`<div class="row"><div><b>${esc(u.name)}</b><div class="tiny">${esc(u.email)} · ${u.role} · tenant ${u.tenant_id||'-'}</div></div></div>`).join('') || 'No users'; }
async function createCustomer(e){ e.preventDefault(); let d = data(e); if(currentUser.role === 'tenant'){ d.role = 'customer'; d.tenant_id = currentUser.tenant_id; } if(d.tenant_id) d.tenant_id = Number(d.tenant_id); else delete d.tenant_id; try{ await api('/api/v1/customers',{method:'POST',body:JSON.stringify(d)}); e.target.reset(); setupCustomerForm(); await loadCustomers(); await loadStats(); }catch(x){ msg(x); } }
function qr(t){ $('#qrText').value = t; makeQR(); scrollTo(0,0); }
function makeQR(){ let t = encodeURIComponent($('#qrText').value); $('#qrImg').src = '/api/v1/qr?text='+t+'&_='+Date.now(); }
async function showDash(){
  currentUser = await api('/api/v1/me');
  if(!currentUser) return;
  $('#forms').innerHTML='';
  $('#dash').hidden=false;
  setAuthNav(true);
  $('#me').textContent = `${currentUser.name} · ${currentUser.email} · ${currentUser.role} · tenant ${currentUser.tenant_id||'-'}`;
  await loadTenants();
  setupCustomerForm();
  let tasks = [loadStats(), loadLinks(), loadKeys(), loadCustomers(), loadDomains()];
  let results = await Promise.allSettled(tasks);
  let failed = results.find(r => r.status === 'rejected');
  if(failed) msg(failed.reason);
}
async function restoreSession(){
  try{ await showDash(); }
  catch(e){ tab(); if(e && e.error && e.error !== 'sso required') msg(e); }
}
function esc(s){ return String(s||'').replace(/[&<>"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c])); }
restoreSession();
