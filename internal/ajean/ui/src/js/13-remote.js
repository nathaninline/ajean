// Accès distant (ajean.link) — piloté depuis l'UI, sans terminal.
// « Connecter » ouvre une popup app.ajean.link/connect.html qui gère compte +
// abonnement, puis renvoie une clé de liaison par postMessage. On la POSTe à
// l'agent local (/api/link/connect) qui fait le `ajean link` (token + service).

const AJEAN_APP_ORIGIN = 'https://app.ajean.link';

function renderRemote(d){
  const off = document.getElementById('remote-off'), on = document.getElementById('remote-on');
  const badge = document.getElementById('remote-badge');
  if(!d || !d.linked){
    // Serveur jamais lié : on l'affiche explicitement plutôt que de laisser la
    // pastille vide, qui se lisait comme « je ne sais pas ».
    off.style.display=''; on.style.display='none';
    setBadge(badge, false, t('remote.not_connected'));
    return;
  }
  off.style.display='none'; on.style.display='';
  document.getElementById('remote-url').value = d.machineURL || '';
  const st = document.getElementById('remote-status');
  if(d.active){ st.textContent='● '+t('remote.online_status'); st.style.color='var(--accent)'; }
  else { st.textContent='○ '+t('remote.tunnel_stopped'); st.style.color='var(--warn)'; }
  const sb = document.getElementById('remote-start');
  if(sb){ sb.style.display = d.active ? 'none' : ''; }
  setBadge(badge, d.active ? true : 'warn', d.active ? t('remote.connected') : t('remote.stopped'));
}

async function loadRemote(){
  // Le panneau ne vaut qu'en LOCAL : il pilote /api/link/* que le tunnel bloque
  // (boîte noire). Sur le portail distant (app.ajean.link/server.html), on le cache.
  const det = document.getElementById('remote-details');
  if(location.hostname === 'app.ajean.link'){ if(det) det.style.display='none'; return; }
  // Statut injoignable : on retombe sur « non connecté » plutôt que de garder
  // l'affichage précédent, qui pourrait annoncer « connecté » à tort.
  try{ renderRemote(await jget('/api/link/status')); }catch(e){ renderRemote(null); }
}

// Ouvre la popup de connexion et attend la clé renvoyée par postMessage.
function remoteConnect(){
  const params = new URLSearchParams({ origin: window.location.origin, host: window.location.hostname || t('remote.this_server') });
  const url = AJEAN_APP_ORIGIN + '/connect.html?' + params.toString();
  const pop = window.open(url, 'ajean-connect', 'width=440,height=640');
  if(!pop){ toast(t('remote.allow_popups')); return; }

  async function onMsg(ev){
    // Anti-usurpation : n'accepte QUE des messages du portail ajean.link.
    if(ev.origin !== AJEAN_APP_ORIGIN) return;
    const d = ev.data || {};
    if(d.type !== 'ajean-link' || !d.token) return;
    window.removeEventListener('message', onMsg);
    try{
      const r = await jpost('/api/link/connect', { token: d.token });
      if(r && r.linked){
        toast('✓ '+t('remote.remote_access_connected'));
        if(r.serviceErr){ toast(t('remote.token_saved_service_prefix')+r.serviceErr+')'); }
        loadRemote();
      } else {
        toast((r && r.error) || t('remote.connection_failed'));
      }
    }catch(e){ toast(t('remote.error_prefix')+e); }
  }
  window.addEventListener('message', onMsg);
}

// Relance le tunnel avec la clé déjà enregistrée (sans repasser par la popup).
async function remoteStart(){
  const b = document.getElementById('remote-start');
  if(b){ b.disabled = true; b.textContent = t('remote.starting'); }
  try{
    const r = await jpost('/api/link/start', {});
    if(r && r.active){ toast('✓ '+t('remote.tunnel_started')); }
    else { toast((r && r.error) || t('remote.tunnel_did_not_start')); }
  }catch(e){ toast(t('remote.error_prefix')+e); }
  if(b){ b.disabled = false; b.textContent = t('remote.start_tunnel_btn'); }
  loadRemote();
}

async function remoteDisconnect(){
  const ok = await askConfirm(t('remote.confirm_disconnect'), {title:t('remote.remote_access_title'), okLabel:t('remote.disconnect_btn')});
  if(!ok) return;
  try{ await jpost('/api/link/disconnect', {}); toast(t('remote.remote_access_cut')); loadRemote(); }
  catch(e){ toast(t('remote.error_prefix')+e); }
}

async function remotePairCode(){
  const box = document.getElementById('remote-pair');
  box.style.display=''; box.textContent=t('remote.generating_code');
  try{
    const r = await jpost('/api/link/paircode', {});
    if(r && r.code){
      box.innerHTML = t('remote.fingerprint_label')+' <b>'+(r.fingerprint||'—')+'</b><br>'+t('remote.pairing_code_label')+' : <b>'+r.code+'</b>';
    } else {
      box.textContent = (r && r.error) || t('remote.code_unavailable');
    }
  }catch(e){ box.textContent=t('remote.error_prefix')+e; }
}
