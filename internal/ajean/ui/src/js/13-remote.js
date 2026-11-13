// Accès distant (ajean.link) — piloté depuis l'UI, sans terminal.
// « Connecter » ouvre une popup app.ajean.link/connect.html qui gère compte +
// abonnement, puis renvoie une clé de liaison par postMessage. On la POSTe à
// l'agent local (/api/link/connect) qui fait le `ajean link` (token + service).

const AJEAN_APP_ORIGIN = 'https://app.ajean.link';

function renderRemote(d){
  const off = document.getElementById('remote-off'), on = document.getElementById('remote-on');
  const perks = document.getElementById('remote-perks-mini');
  const badge = document.getElementById('remote-badge');
  if(!d || !d.linked){
    // Serveur jamais lié : on l'affiche explicitement plutôt que de laisser la
    // pastille vide, qui se lisait comme « je ne sais pas ».
    off.style.display=''; on.style.display='none';
    if(perks) perks.style.display='none'; // le bloc d'avantages complet est déjà dans remote-off
    setBadge(badge, false, t('remote.not_connected'));
    return;
  }
  off.style.display='none'; on.style.display='';
  if(perks) perks.style.display=''; // rappel discret en footer, une fois connecté
  document.getElementById('remote-url').value = d.machineURL || '';
  const st = document.getElementById('remote-status');
  st.textContent = d.active ? t('remote.online_status') : t('remote.tunnel_stopped'); st.classList.toggle('on', !!d.active);
  const sb = document.getElementById('remote-start');
  if(sb){ sb.style.display = d.active ? 'none' : ''; }
  setBadge(badge, d.active ? true : 'warn', d.active ? t('remote.connected') : t('remote.stopped'));
}

async function loadRemote(){
  const det = document.getElementById('remote-details');
  // Sur le portail distant (app.ajean.link), /api/link/* est bloqué par le tunnel
  // (boîte noire), donc pas d'appel API. On n'efface plus la section pour autant :
  // on l'AFFICHE en état informatif « connecté » (on est forcément relié pour être
  // ici), avec l'adresse et « en ligne », mais SANS les contrôles purement locaux
  // (appairage, déconnexion, démarrage du tunnel, sauvegarde) qui ne
  // fonctionneraient pas à distance. Ainsi le menu AJEAN LINK reste visible comme
  // dans l'UI de base.
  if(location.hostname === 'app.ajean.link'){
    if(det) det.style.display='';
    const off=document.getElementById('remote-off'); if(off) off.style.display='none';
    const on=document.getElementById('remote-on'); if(on) on.style.display='';
    const url=document.getElementById('remote-url'); if(url) url.value=location.origin;
    const st=document.getElementById('remote-status');
    if(st){ st.textContent=t('remote.online_status'); st.classList.add('on'); }
    const sb=document.getElementById('remote-start'); if(sb) sb.style.display='none';
    const fc=document.getElementById('remote-firstconn'); if(fc) fc.style.display='none';
    const bk=document.getElementById('backup-block'); if(bk) bk.style.display='none';    const perks=document.getElementById('remote-perks-mini'); if(perks) perks.style.display='';
    setBadge(document.getElementById('remote-badge'), true, t('remote.connected'));
    return;
  }
  // Local : statut injoignable → on retombe sur « non connecté » plutôt que de
  // garder l'affichage précédent, qui pourrait annoncer « connecté » à tort.
  try{ const d = await jget('/api/link/status'); renderRemote(d); devSetLocal(d); }catch(e){ renderRemote(null); }
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

// Connecter un appareil : la fenêtre montre l'adresse, l'empreinte et un code
// frais (usage unique, 10 min) dans l'ordre où on s'en sert sur l'autre appareil.
async function openPairModal(){
  const err = document.getElementById('pair-err');
  const code = document.getElementById('pair-code'), fp = document.getElementById('pair-fp');
  document.getElementById('pair-url').textContent = document.getElementById('remote-url').value || '';
  err.hidden = true; code.textContent = '…'; fp.textContent = '…';
  showModal('pair-modal');
  try{
    const r = await jpost('/api/link/paircode', {});
    if(r && r.code){ code.textContent = r.code; fp.textContent = r.fingerprint || '-'; return; }
    err.textContent = (r && r.error) || t('remote.code_unavailable');
  }catch(e){ err.textContent = t('remote.error_prefix') + e; }
  err.hidden = false; code.textContent = '-'; fp.textContent = '-';
}
function closePairModal(){ hideModal('pair-modal'); }

