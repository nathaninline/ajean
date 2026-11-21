// 26-route.js — une adresse par écran.
//
// Chaque écran a son adresse : un rechargement rouvre le même, et les boutons
// Précédent / Suivant du navigateur passent de l'un à l'autre.
//
//   #/                 la conversation
//   #/history          l'historique des conversations (vue du menu)
//   #/apps             la liste des applications (vue du menu)
//   #/apps/<id>        une application ouverte
//   #/files            les fichiers de Jean
//   #/memory           la mémoire de Jean
//   #/trackers         les suivis du projet
//   #/models, #/tasks, #/settings, #/engine…   une section du menu (ROUTE_SECTIONS)
//
// ⚠️ L'adresse vit dans le FRAGMENT (#/…), jamais dans le chemin. La page n'est pas
// toujours servie à « / » : derrière le relais elle est sous /u/<id>, sur
// app.ajean.link c'est server.html?m=<machine>, et API_BASE (04-api.js) se déduit
// du chemin. Changer le chemin casserait les appels /api/… au rechargement, et le
// portail (pages statiques) n'a aucune route pour ces adresses. On n'écrit donc
// que « #/… » : chemin et paramètres restent ceux de la page, quels qu'ils soient.
//
// Rien n'est branché dans les fonctions d'ouverture : l'écran courant se LIT dans
// l'interface (fenêtre affichée, vue du menu, application lancée), et des
// observateurs préviennent quand il change. Un écran ouvert par n'importe quel
// chemin (bouton, raccourci, autre script) met donc l'adresse à jour.

// Fenêtres qui sont des écrans : nom dans l'adresse → id de la fenêtre.
const ROUTE_MODALS = {files:'jean-files-modal', memory:'jean-mem-modal', trackers:'tracker-modal'};
// Sections du menu : nom dans l'adresse → clé du titre de la section (data-i18n
// du .stitle dans index.tmpl.html ; elle sert d'identifiant, pas de texte).
const ROUTE_SECTIONS = {device:'device.title', config:'config.title', models:'presets.title', tasks:'tasks.title',
  appearance:'appearance.title', api:'api.title', link:'link.title', engine:'engine.title', settings:'settings.section_title'};

(function(){
  const OPEN = {files:()=>openJeanFiles(), memory:()=>openJeanMem(), trackers:()=>openTrackerHub('')};
  const CLOSE = {files:()=>closeJeanFiles(), memory:()=>closeJeanMem(), trackers:()=>closeTrackerModal()};
  let section = '';      // section du menu ouverte par l'utilisateur ou par l'adresse
  let applying = 0;      // l'adresse est en train d'être appliquée : elle ne bouge pas
  let started = false;   // faux pendant le démarrage : l'interface s'installe encore
  let chain = Promise.resolve();
  let clicked = null, clickedAt = 0;   // dernier titre de section cliqué

  const el = (id)=>document.getElementById(id);
  const shown = (id)=>{ const m = el(id); return !!m && m.classList.contains('show'); };
  const appRun = ()=>(typeof APP_RUN !== 'undefined' && APP_RUN) ? APP_RUN : null;
  const sideView = ()=>typeof SIDE_VIEW !== 'undefined' ? SIDE_VIEW : 'main';
  function sectionEl(name){
    const key = ROUTE_SECTIONS[name];
    const title = key && document.querySelector('#side-main > details > summary .stitle[data-i18n="'+key+'"]');
    return title ? title.closest('details') : null;
  }
  function sectionName(d){
    const title = d.querySelector(':scope > summary .stitle');
    const key = title && title.getAttribute('data-i18n');
    for(const n in ROUTE_SECTIONS) if(ROUTE_SECTIONS[n] === key) return n;
    return '';
  }

  // L'écran affiché, tel que l'interface le montre.
  function routeNow(){
    for(const n in ROUTE_MODALS) if(shown(ROUTE_MODALS[n])) return '/'+n;
    const run = appRun();
    if(run) return '/apps/'+encodeURIComponent(run.id);
    if(sideView() === 'hist') return '/history';
    if(sideView() === 'apps') return '/apps';
    const d = section && sectionEl(section);
    if(d && d.open) return '/'+section;
    return '/';
  }
  // L'écran demandé par l'adresse. null : le fragment n'est pas une de nos
  // adresses (ancre d'un lien de la réponse, autre usage) et on n'y touche pas.
  function routeRead(){
    const h = location.hash || '';
    if(h.charAt(1) !== '/') return null;
    const parts = h.slice(2).split('/').filter(Boolean);
    let arg = parts[1] || '';
    try{ arg = decodeURIComponent(arg); }catch(_){}
    return {name: parts[0] || '', arg};
  }

  // L'écran a peut-être changé : l'adresse suit (une entrée d'historique par écran).
  let queued = false;
  function routeSync(){
    if(queued) return;
    queued = true;
    // Après l'action en cours : changer d'écran ferme l'un puis ouvre l'autre.
    setTimeout(()=>{
      queued = false;
      if(!started || applying) return;
      const next = '#'+routeNow();
      if(next === location.hash) return;
      if(next === '#/' && !routeRead()) return;   // déjà sur la conversation, adresse nue
      try{ history.pushState(null, '', next); }catch(_){}
    }, 0);
  }

  // Menu en tiroir (téléphone, menu masqué) : on l'ouvre pour montrer la vue
  // demandée, et on le referme en quittant cette vue par l'adresse (Précédent).
  let drawerByRoute = false;
  const drawerOpen = ()=>document.body.classList.contains('drawer-open');
  function showSide(){
    const s = el('side');
    if(s && s.getBoundingClientRect().right <= 1 && !drawerOpen()){ toggleSide(); drawerByRoute = true; }
  }
  function hideSide(){
    if(drawerByRoute && drawerOpen()) toggleSide();
    drawerByRoute = false;
  }
  // Ouvre l'écran d'une adresse et referme les autres.
  async function routeApply(r){
    const name = r ? r.name : '', arg = r ? r.arg : '';
    applying++;
    try{
      for(const n in ROUTE_MODALS) if(n !== name && shown(ROUTE_MODALS[n])) CLOSE[n]();
      if(appRun() && !(name === 'apps' && arg)) closeAppRun();
      if(ROUTE_MODALS[name]){
        if(!shown(ROUTE_MODALS[name])) OPEN[name]();
      } else if(name === 'history'){
        showSide();
        if(sideView() !== 'hist'){ showSideView('hist'); loadHistory(); }
      } else if(name === 'apps' && document.body.classList.contains('no-apps')){
        if(sideView() !== 'main') showSideView('main');
      } else if(name === 'apps'){
        showSide();
        if(sideView() !== 'apps') await openAppsView();
        if(arg && !(appRun() && appRun().id === arg)) await openApp(arg);
      } else {
        if(sideView() !== 'main') showSideView('main');
        const d = sectionEl(name), old = section && section !== name ? sectionEl(section) : null;
        if(old) old.open = false;
        section = d ? name : '';
        if(d){ showSide(); d.open = true; d.scrollIntoView({block:'nearest'}); }
        else hideSide();
      }
    }catch(_){ /* écran impossible à ouvrir (fonction absente, réseau) : on reste où l'on est */ }
    applying--;
    // L'écran obtenu n'est pas toujours celui demandé (application supprimée, nom
    // inconnu) : l'adresse dit ce qui est affiché, sans ajouter d'entrée.
    const real = '#'+routeNow();
    if(real !== location.hash && (routeRead() || real !== '#/')){
      try{ history.replaceState(null, '', real); }catch(_){}
    }
  }

  function start(){
    const mo = new MutationObserver(routeSync);
    for(const n in ROUTE_MODALS){ const m = el(ROUTE_MODALS[n]); if(m) mo.observe(m, {attributes:true, attributeFilter:['class']}); }
    for(const id of ['side-main', 'side-hist', 'side-apps', 'app-run']){ const v = el(id); if(v) mo.observe(v, {attributes:true, attributeFilter:['hidden']}); }
    // Sections du menu : seule une ouverture voulue (clic sur le titre) change
    // d'adresse, pas une section ouverte d'office ou par un script.
    document.addEventListener('click', (e)=>{
      const sum = e.target.closest && e.target.closest('#side-main > details > summary');
      if(sum){ clicked = sum.parentElement; clickedAt = Date.now(); }
    }, true);
    document.addEventListener('toggle', (e)=>{
      const d = e.target;
      if(!d || d.parentElement !== el('side-main')) return;
      const n = sectionName(d);
      if(!n) return;
      if(d.open){
        if(d === clicked && Date.now() - clickedAt < 1500){ section = n; routeSync(); }
      } else if(section === n){ section = ''; routeSync(); }
    }, true);
    addEventListener('popstate', ()=>{
      // Fragment étranger (ancre d'un lien, « # » nu) : ce n'est pas un changement d'écran.
      if(!routeRead() && (location.hash || /#$/.test(location.href))) return;
      chain = chain.then(()=>routeApply(routeRead()));
    });
    const first = routeRead();
    if(first && first.name) chain = chain.then(()=>routeApply(first));
    chain = chain.then(()=>{ started = true; });
  }
  // Après le chargement : les écrans s'ouvrent avec les fonctions de l'interface,
  // qui doit avoir fini de s'installer.
  if(document.readyState === 'complete') setTimeout(start, 0);
  else addEventListener('load', ()=>setTimeout(start, 0));
})();
