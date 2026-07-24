// Accès distant (ajean.link) — piloté depuis l'UI, sans terminal.
// « Connecter » ouvre une popup app.ajean.link/connect.html qui gère compte +
// abonnement, puis renvoie une clé de liaison par postMessage. On la POSTe à
// l'agent local (/api/link/connect) qui fait le `jean link` (token + service).

const AJEAN_APP_ORIGIN = 'https://app.ajean.link';

function renderRemote(d){
  const off = document.getElementById('remote-off'), on = document.getElementById('remote-on');
  const badge = document.getElementById('remote-badge');
  if(!d || !d.linked){
    off.style.display=''; on.style.display='none';
    if(badge){ badge.textContent=''; }
    return;
  }
  off.style.display='none'; on.style.display='';
  document.getElementById('remote-url').value = d.machineURL || '';
  const st = document.getElementById('remote-status');
  if(d.active){ st.textContent='● en ligne — accessible à distance'; st.style.color='var(--accent)'; }
  else { st.textContent='○ service arrêté (l\'accès distant ne répondra pas)'; st.style.color='var(--warn)'; }
  if(badge){ badge.textContent = d.active ? '● connecté' : '○ arrêté'; badge.style.color = d.active?'var(--accent)':'var(--warn)'; }
}

async function loadRemote(){
  // Le panneau ne vaut qu'en LOCAL : il pilote /api/link/* que le tunnel bloque
  // (boîte noire). Sur le portail distant (app.ajean.link/server.html), on le cache.
  const det = document.getElementById('remote-details');
  if(location.hostname === 'app.ajean.link'){ if(det) det.style.display='none'; return; }
  try{ renderRemote(await jget('/api/link/status')); }catch(e){}
}

// Ouvre la popup de connexion et attend la clé renvoyée par postMessage.
function remoteConnect(){
  const params = new URLSearchParams({ origin: window.location.origin, host: window.location.hostname || 'ce serveur' });
  const url = AJEAN_APP_ORIGIN + '/connect.html?' + params.toString();
  const pop = window.open(url, 'ajean-connect', 'width=440,height=640');
  if(!pop){ toast('autorisez les popups pour connecter ajean.link'); return; }

  async function onMsg(ev){
    // Anti-usurpation : n'accepte QUE des messages du portail ajean.link.
    if(ev.origin !== AJEAN_APP_ORIGIN) return;
    const d = ev.data || {};
    if(d.type !== 'ajean-link' || !d.token) return;
    window.removeEventListener('message', onMsg);
    try{
      const r = await jpost('/api/link/connect', { token: d.token });
      if(r && r.linked){
        toast('✓ accès distant connecté');
        if(r.serviceErr){ toast('token enregistré (service : '+r.serviceErr+')'); }
        loadRemote();
      } else {
        toast((r && r.error) || 'échec de la connexion');
      }
    }catch(e){ toast('erreur : '+e); }
  }
  window.addEventListener('message', onMsg);
}

async function remoteDisconnect(){
  const ok = await askConfirm('Couper l\'accès distant et oublier la clé de liaison de ce serveur ?', {title:'Accès distant', okLabel:'Déconnecter'});
  if(!ok) return;
  try{ await jpost('/api/link/disconnect', {}); toast('accès distant coupé'); loadRemote(); }
  catch(e){ toast('erreur : '+e); }
}

async function remotePairCode(){
  const box = document.getElementById('remote-pair');
  box.style.display=''; box.textContent='génération du code…';
  try{
    const r = await jpost('/api/link/paircode', {});
    if(r && r.code){
      box.innerHTML = 'Empreinte : <b>'+(r.fingerprint||'—')+'</b><br>Code d\'appairage (valable 10 min, usage unique) : <b>'+r.code+'</b>';
    } else {
      box.textContent = (r && r.error) || 'code indisponible';
    }
  }catch(e){ box.textContent='erreur : '+e; }
}
