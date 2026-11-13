// Appareils — savoir sur quelle machine on travaille, et basculer de l'une à
// l'autre sans quitter l'app.
//
// Une même fenêtre sert deux origines :
//   - http://localhost:8090 : l'AJEAN de CE PC (« local ») ;
//   - https://app.ajean.link/server.html?m=<id> : cette même interface, branchée
//     par le tunnel chiffré sur une AUTRE machine du compte (« distant »).
// Basculer, c'est naviguer de l'une à l'autre. Ce fichier tourne des deux côtés
// (server.html est généré à partir de cette interface) et tient trois choses :
//   1. la puce d'en-tête, qui nomme l'appareil piloté ;
//   2. le menu, qui liste les appareils et fait la bascule ;
//   3. la mémoire du dernier appareil, pour y revenir au lancement suivant.
//
// Les deux origines ne partagent aucun stockage. Ce qu'elles doivent se dire
// voyage donc dans l'adresse, au moment de la bascule :
//   local → distant : ?home=<adresse locale>&hm=<id>&hn=<nom>   (retenu côté distant)
//   distant → local : #dev=<liste des appareils du compte>       (retenu côté local)
//
// Clés localStorage :
//   côté local    ajean.dev.last   '' = ce PC · <id> = cet appareil · '*' = le portail
//                 ajean.dev.list   appareils du compte, vus au dernier retour
//   côté distant  ajean.dev.home   {url,id,name} du PC qui héberge l'app
//                 ajk-home         dernier appareil ouvert (clé du portail, partagée)

const DEV_REMOTE = location.hostname === 'app.ajean.link';
// L'adresse de retour ne peut être que la boucle locale : un lien piégé ne doit
// pas pouvoir faire pointer « Ce PC » vers un site quelconque.
const DEV_HOME_RX = /^http:\/\/(localhost|127\.0\.0\.1)(:\d{1,5})?$/;
const DEV_ID_RX = /^[A-Za-z0-9_-]{1,64}$/;

let DEV_SELF = null; // côté local : {id, name} de ce PC
let DEV_LIST = [];   // côté distant : appareils du compte

function devLS(k){ try{ return localStorage.getItem(k) || ''; }catch(_){ return ''; } }
function devLSSet(k, v){ try{ if(v === '') localStorage.removeItem(k); else localStorage.setItem(k, v); }catch(_){} }
function devJSON(k, dflt){ try{ const v = JSON.parse(devLS(k)); return v == null ? dflt : v; }catch(_){ return dflt; } }
// Ne garde d'un appareil que ce que le menu affiche, borné.
function devClean(m){
  m = m || {};
  return { id: String(m.id || ''), name: String(m.name || '').slice(0, 64), hostname: String(m.hostname || '').slice(0, 64), online: !!m.online };
}
function devValidList(a){ return (Array.isArray(a) ? a : []).map(devClean).filter(m => DEV_ID_RX.test(m.id)).slice(0, 50); }
function devHome(){ const h = devJSON('ajean.dev.home', null); return (h && DEV_HOME_RX.test(h.url || '')) ? h : null; }
function devCanSwitch(){ return DEV_REMOTE || ajeanLocalUI(); }
function devLabel(m){ return m.name || m.hostname || m.id.slice(0, 8); }

// --- Puce d'en-tête ----------------------------------------------------------
function devRenderChip(name, remote){
  const chip = document.getElementById('dev-chip'), nm = document.getElementById('dev-chip-name');
  if(!chip || !nm || !name) return;
  nm.textContent = name;
  chip.classList.toggle('remote', !!remote);
  chip.classList.toggle('static', !devCanSwitch());
  chip.hidden = false;
  // Le titre de la fenêtre dit aussi l'appareil dès qu'il y en a plusieurs : il
  // reste lisible menu replié, et dans la barre des tâches.
  if(remote || devValidList(devJSON('ajean.dev.list', [])).length) document.title = 'AJEAN · ' + name;
}

// Côté local : appelé par loadRemote avec /api/link/status ({host, machine}).
function devSetLocal(d){
  if(DEV_REMOTE || !d) return;
  DEV_SELF = { id: d.machine || '', name: d.host || t('dev.this_pc') };
  devRenderChip(DEV_SELF.name, false);
}

async function devSelf(){
  if(!DEV_SELF){ try{ devSetLocal(await jget('/api/link/status')); }catch(_){} }
  return DEV_SELF || { id: '', name: '' };
}

// --- Bascule -----------------------------------------------------------------
// Le portail répond-il ? Rend son numéro de build (qui versionne server.html,
// pour ne pas charger une page périmée du cache), ou '' s'il est injoignable.
async function devPortalBuild(){
  const ac = new AbortController(), timer = setTimeout(() => ac.abort(), 5000);
  try{
    const r = await fetch(AJEAN_APP_ORIGIN + '/version.json?t=' + Date.now(), { cache: 'no-store', signal: ac.signal });
    if(!r.ok) return '';
    return String((await r.json()).build || '1');
  }catch(_){ return ''; }
  finally{ clearTimeout(timer); }
}
function devHomeParams(self){
  return 'home=' + encodeURIComponent(location.origin) + '&hm=' + encodeURIComponent(self.id) + '&hn=' + encodeURIComponent(self.name);
}

// Ouvre l'appareil <id>. Rend false si on est resté sur place (portail injoignable).
async function devGo(id, auto){
  if(DEV_REMOTE){
    devLSSet('ajk-home', id);
    location.href = 'server.html?m=' + encodeURIComponent(id);
    return true;
  }
  const build = await devPortalBuild();
  if(!build){ toast(t('dev.no_network')); return false; }
  const self = await devSelf();
  devLSSet('ajean.dev.last', id);
  location.href = AJEAN_APP_ORIGIN + '/server.html?m=' + encodeURIComponent(id) + '&v=' + encodeURIComponent(build)
    + '&' + devHomeParams(self) + (auto ? '&auto=1' : '');
  return true;
}

// Ouvre le portail : connexion au compte, liste complète, gestion des appareils.
async function devPortal(auto){
  if(DEV_REMOTE){ location.href = '/?pick=1'; return true; }
  if(!(await devPortalBuild())){ toast(t('dev.no_network')); return false; }
  const self = await devSelf();
  devLSSet('ajean.dev.last', '*');
  location.href = AJEAN_APP_ORIGIN + '/?' + (auto ? 'auto=1' : 'pick=1') + '&' + devHomeParams(self);
  return true;
}

// Retour au PC qui héberge l'app, en lui confiant la liste des appareils.
function devGoHome(){
  const h = devHome();
  if(!h) return;
  location.href = h.url + '/#dev=' + encodeURIComponent(JSON.stringify(DEV_LIST.map(devClean)));
}

// --- Menu --------------------------------------------------------------------
const DEV_IC_PC = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="2.5" y="4" width="19" height="13" rx="2"/><path d="M8 21h8M12 17v4"/></svg>';
const DEV_IC_NET = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18"/></svg>';
const DEV_IC_CK = '<svg class="dev-item-ck" viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>';

function devItem(icon, name, sub, opts){
  opts = opts || {};
  const b = document.createElement('button');
  b.type = 'button'; b.className = 'dev-item' + (opts.cur ? ' cur' : ''); b.setAttribute('role', 'menuitem');
  b.innerHTML = icon + '<span class="dev-item-t"><span class="dev-item-n"></span><span class="dev-item-s"></span></span>' + DEV_IC_CK;
  b.querySelector('.dev-item-n').textContent = name;
  const s = b.querySelector('.dev-item-s');
  if(sub) s.textContent = sub; else s.remove();
  if(opts.disabled) b.disabled = true;
  // L'appareil courant ne mène nulle part : le clic referme simplement le menu.
  b.onclick = () => { devMenuClose(); if(!opts.cur && opts.go) opts.go(); };
  return b;
}

function devMenuFill(menu){
  menu.textContent = '';
  const h = document.createElement('div'); h.className = 'dev-menu-h'; h.textContent = t('dev.menu_title'); menu.appendChild(h);
  let others, curId = '', skipId = '';
  if(DEV_REMOTE){
    const home = devHome();
    if(home){ menu.appendChild(devItem(DEV_IC_PC, home.name || t('dev.this_pc'), t('dev.local_tag'), { go: devGoHome })); skipId = home.id; }
    others = DEV_LIST;
    curId = (typeof MACHINE !== 'undefined' && MACHINE) ? MACHINE.id : '';
  } else {
    const self = DEV_SELF || { id: '', name: t('dev.this_pc') };
    menu.appendChild(devItem(DEV_IC_PC, self.name, t('dev.local_tag'), { cur: true }));
    others = devValidList(devJSON('ajean.dev.list', []));
    skipId = self.id;
  }
  others = others.filter(m => m.id !== skipId);
  for(const m of others){
    // L'état en ligne n'est fiable que côté distant (liste fraîche du compte) ;
    // côté local la liste date du dernier retour, on n'en déduit rien.
    const off = DEV_REMOTE && !m.online && m.id !== curId;
    const sub = off ? t('dev.offline') : (m.hostname && m.hostname !== m.name ? m.hostname : t('dev.remote_tag'));
    menu.appendChild(devItem(DEV_IC_NET, devLabel(m), sub, { cur: m.id === curId, disabled: off, go: () => devGo(m.id) }));
  }
  const sep = document.createElement('div'); sep.className = 'dev-sep'; menu.appendChild(sep);
  const more = document.createElement('button');
  more.type = 'button'; more.className = 'dev-item dev-more'; more.setAttribute('role', 'menuitem');
  more.textContent = (DEV_REMOTE || others.length) ? t('dev.others') : t('dev.signin');
  more.onclick = () => { devMenuClose(); devPortal(); };
  menu.appendChild(more);
}

async function devRefreshList(){
  if(!DEV_REMOTE) return;
  try{
    const r = await fetch('/app/machines'); // réécrit vers le relais par server.html
    if(!r.ok) return;
    DEV_LIST = devValidList((await r.json()).machines);
  }catch(_){}
}

function devMenuClose(){
  const m = document.getElementById('dev-menu');
  if(m) m.remove();
  document.removeEventListener('click', devMenuOutside, true);
  document.removeEventListener('keydown', devMenuKey, true);
}
function devMenuOutside(e){ const m = document.getElementById('dev-menu'); if(m && !m.contains(e.target) && !e.target.closest('#dev-chip')) devMenuClose(); }
function devMenuKey(e){ if(e.key === 'Escape') devMenuClose(); }

function devMenuToggle(ev){
  if(ev) ev.stopPropagation();
  if(document.getElementById('dev-menu')){ devMenuClose(); return; }
  if(!devCanSwitch()) return;
  const chip = document.getElementById('dev-chip'), r = chip.getBoundingClientRect();
  const menu = document.createElement('div');
  menu.id = 'dev-menu'; menu.className = 'dev-menu'; menu.setAttribute('role', 'menu');
  menu.style.left = Math.max(8, Math.min(r.left, window.innerWidth - 268)) + 'px';
  menu.style.top = (r.bottom + 6) + 'px';
  devMenuFill(menu);
  document.body.appendChild(menu);
  document.addEventListener('click', devMenuOutside, true);
  document.addEventListener('keydown', devMenuKey, true);
  // Côté distant, la liste se rafraîchit à chaque ouverture (un appareil a pu
  // passer en ligne) ; le menu ouvert se remet à jour s'il l'est encore.
  if(DEV_REMOTE) devRefreshList().then(() => { const m = document.getElementById('dev-menu'); if(m) devMenuFill(m); });
}

// --- Reprise au lancement (côté local) ----------------------------------------
// L'app s'ouvre toujours sur ce PC ; si la dernière session était sur un autre
// appareil, on y retourne — après un court écran qui laisse le temps de rester
// ici. C'est aussi la sortie de secours : sans lui, un appareil mémorisé devenu
// injoignable renverrait à chaque lancement vers une page d'erreur.
function devResume(last){
  const known = devValidList(devJSON('ajean.dev.list', [])).find(m => m.id === last);
  const ov = document.createElement('div');
  ov.className = 'dev-splash';
  ov.innerHTML = '<div class="dev-splash-box"><div class="dev-splash-t"></div><button type="button" class="btn-primary"></button></div>';
  ov.querySelector('.dev-splash-t').textContent = known ? t('dev.resuming').replace('{name}', devLabel(known)) : t('dev.resuming_any');
  const btn = ov.querySelector('button');
  btn.textContent = t('dev.stay');
  let stay = false;
  btn.onclick = () => { stay = true; devLSSet('ajean.dev.last', ''); ov.remove(); };
  document.body.appendChild(ov);
  setTimeout(async () => {
    if(stay) return;
    const ok = (last === '*' || !DEV_ID_RX.test(last)) ? await devPortal(true) : await devGo(last, true);
    if(!ok && !stay) ov.remove(); // portail injoignable : on reste ici, la mémoire est gardée
  }, 1500);
}

// --- Démarrage ---------------------------------------------------------------
(function devBoot(){
  if(DEV_REMOTE){
    const sp = new URLSearchParams(location.search);
    const home = sp.get('home') || '', m = sp.get('m') || '', auto = sp.get('auto') === '1';
    if(DEV_HOME_RX.test(home)){
      const hm = sp.get('hm') || '';
      devLSSet('ajean.dev.home', JSON.stringify({ url: home, id: DEV_ID_RX.test(hm) ? hm : '', name: (sp.get('hn') || '').slice(0, 64) }));
    }
    // Reprise : le PC local ne connaît que l'appareil d'où l'on était parti de
    // chez lui. Si on a changé d'appareil depuis, ici on le sait (ajk-home).
    const last = devLS('ajk-home');
    if(auto && DEV_ID_RX.test(last) && last !== m){ location.replace('server.html?m=' + encodeURIComponent(last)); return; }
    if(DEV_ID_RX.test(m)) devLSSet('ajk-home', m);
    if(home || auto){
      ['home', 'hm', 'hn', 'auto'].forEach(k => sp.delete(k));
      try{ history.replaceState(null, '', location.pathname + '?' + sp.toString()); }catch(_){}
    }
    // MACHINE / MACHINE_READY : posés par le bootstrap de server.html.
    if(typeof MACHINE_READY !== 'undefined'){
      MACHINE_READY.then(() => {
        if(typeof MACHINE === 'undefined' || !MACHINE) return;
        devRenderChip(devLabel(devClean(MACHINE)), true);
        devRefreshList();
      }).catch(() => {});
    }
    return;
  }
  if(!ajeanLocalUI()) return;
  const back = /^#dev=(.*)$/.exec(location.hash || '');
  if(back){
    try{ devLSSet('ajean.dev.list', JSON.stringify(devValidList(JSON.parse(decodeURIComponent(back[1]))))); }catch(_){}
    devLSSet('ajean.dev.last', '');
    try{ history.replaceState(null, '', location.pathname + location.search); }catch(_){}
    return;
  }
  const last = devLS('ajean.dev.last');
  if(last) devResume(last);
})();
