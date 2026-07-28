// ===== Thèmes ================================================================
// Registre des thèmes disponibles. Pour en AJOUTER un : ajouter un bloc
// [data-theme="…"] dans le <style> puis une entrée {id,label} ici — le reste
// (menu déroulant, persistance localStorage, application) est automatique.
// Chaque FAMILLE de thème existe en clair et en sombre : le menu ne liste donc
// que les familles, et un interrupteur choisit la variante. Bien plus court que
// l'ancienne liste où chaque variante était une entrée séparée.
const THEME_FAMILIES=[
  {id:'classic', label:'Classique', light:'light',  dark:'dark'},
  {id:'ajean',   label:'AJEAN',     light:'ajean',  dark:'ajean-dark'},
  {id:'soft',    label:'Doux',      light:'soft',   dark:'soft-dark'},
  {id:'gpt',     label:'ChatGPT',   light:'gpt',    dark:'gpt-dark'},
];
// Tous les identifiants valides, à plat (persistance inchangée : on enregistre
// toujours un id de thème, pas une famille).
const THEMES=THEME_FAMILIES.flatMap(f=>[{id:f.light,label:f.label},{id:f.dark,label:f.label+' sombre'}]);
// Ancien identifiant (v0.5.7) → thème actuel.
const THEME_ALIASES={'gpt-light':'gpt'};
// familyOf renvoie {famille, sombre?} pour un id de thème.
function familyOf(id){
  for(const f of THEME_FAMILIES){
    if(f.light===id) return {fam:f, dark:false};
    if(f.dark===id)  return {fam:f, dark:true};
  }
  return {fam:THEME_FAMILIES[0], dark:false};
}
// Lexend (thème "doux") n'est chargé QUE si ce thème est choisi : aucune requête
// externe (Google Fonts) tant qu'on reste en sombre/clair — jean reste local par
// défaut. Fallback système sans-serif si hors ligne.
function ensureSoftFont(){
  if(document.getElementById('lexend-font')) return;
  const l=document.createElement('link');
  l.id='lexend-font'; l.rel='stylesheet';
  l.href='https://fonts.googleapis.com/css2?family=Lexend:wght@300;400;500;600;700&display=swap';
  document.head.appendChild(l);
}
function applyTheme(id){
  id=THEME_ALIASES[id]||id;
  if(!THEMES.some(t=>t.id===id)) id='light';
  if(id==='soft'||id==='soft-dark') ensureSoftFont();
  document.documentElement.setAttribute('data-theme', id);
  try{ localStorage.setItem('jean-theme', id); }catch(e){}
  const cur=familyOf(id);
  const sel=document.getElementById('theme-select'); if(sel) sel.value=cur.fam.id;
  const sw=document.getElementById('theme-dark'); if(sw) sw.checked=cur.dark;
}
// Bascule clair/sombre en gardant la famille courante.
function toggleThemeDark(){
  const id=document.documentElement.getAttribute('data-theme')||'light';
  const cur=familyOf(id);
  const dark=document.getElementById('theme-dark').checked;
  applyTheme(dark?cur.fam.dark:cur.fam.light);
  savePrefs();
}
// Changement de famille : on reste dans la même variante (clair ou sombre).
function pickThemeFamily(famID){
  const id=document.documentElement.getAttribute('data-theme')||'light';
  const dark=familyOf(id).dark;
  const f=THEME_FAMILIES.find(x=>x.id===famID)||THEME_FAMILIES[0];
  applyTheme(dark?f.dark:f.light);
  savePrefs();
}
function initTheme(){
  const sel=document.getElementById('theme-select');
  if(sel && !sel.options.length){
    THEME_FAMILIES.forEach(f=>{ const o=document.createElement('option'); o.value=f.id; o.textContent=f.label; sel.appendChild(o); });
  }
  let id='light'; try{ id=localStorage.getItem('jean-theme')||'light'; }catch(e){}
  applyTheme(id);
}
// ===== Police ===============================================================
// Réglage indépendant du thème : "auto" garde celle du thème, les autres la
// remplacent (voir les blocs [data-font=…] dans le <style>). Persisté dans
// 'jean-font' et partagé entre appareils via /api/prefs.
const FONTS=[
  {id:'auto',   label:'du thème'},
  {id:'mono',   label:'Mono'},
  {id:'system', label:'Système'},
  {id:'lexend', label:'Lexend'},
  {id:'serif',  label:'Serif'},
];
function applyFont(id){
  if(!FONTS.some(f=>f.id===id)) id='auto';
  if(id==='lexend') ensureSoftFont();
  document.documentElement.setAttribute('data-font', id);
  try{ localStorage.setItem('jean-font', id); }catch(e){}
  const sel=document.getElementById('font-select'); if(sel) sel.value=id;
}
function initFont(){
  const sel=document.getElementById('font-select');
  if(sel && !sel.options.length){
    FONTS.forEach(f=>{ const o=document.createElement('option'); o.value=f.id; o.textContent=f.label; sel.appendChild(o); });
  }
  let id='auto'; try{ id=localStorage.getItem('jean-font')||'auto'; }catch(e){}
  applyFont(id);
}
// ===== Affichage ============================================================
// Quatre interrupteurs indépendants (ils remplacent l'ancien mode « simple »,
// tout ou rien). Chacun pose un attribut sur <html> ; le CSS fait le reste,
// sauf « replier les bulles » qui est un comportement (voir 08-chat-render).
const VIEW_OPTS=[
  {id:'hide-reasoning', label:'masquer le raisonnement'},
  {id:'hide-tools',     label:"masquer les appels d'outils"},
  {id:'fold-tools',     label:'garder les bulles repliées'},
  {id:'hide-side',      label:'barre latérale escamotable'},
];
function viewOn(id){ return document.documentElement.getAttribute('data-'+id)==='1'; }
function applyView(id, on){
  document.documentElement.setAttribute('data-'+id, on?'1':'0');
  try{ localStorage.setItem('jean-'+id, on?'1':'0'); }catch(e){}
  const box=document.getElementById('view-'+id); if(box) box.checked=!!on;
  // La barre latérale peut rester ouverte quand on repasse en mode fixe.
  if(id==='hide-side' && !on){
    document.getElementById('side').classList.remove('open');
    document.getElementById('backdrop').classList.remove('open');
    document.body.classList.remove('drawer-open');
  }
}
function toggleView(id){ applyView(id, document.getElementById('view-'+id).checked); savePrefs(); }
function initView(){
  // Reprise de l'ancien réglage : « simple » masquait raisonnement ET outils.
  let legacy=null; try{ legacy=localStorage.getItem('jean-display'); }catch(e){}
  VIEW_OPTS.forEach(o=>{
    let v=null; try{ v=localStorage.getItem('jean-'+o.id); }catch(e){}
    if(v===null && legacy==='simple' && (o.id==='hide-reasoning'||o.id==='hide-tools')) v='1';
    applyView(o.id, v==='1');
  });
}
