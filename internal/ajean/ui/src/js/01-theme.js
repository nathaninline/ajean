// ===== Apparence =============================================================
// Un seul thème, en deux variantes : claire (par défaut) et sombre. La variante
// est portée par l'attribut data-theme de <html> ('' ou 'dark') ; tout le reste
// est du CSS (voir la section « Palette »). Persistée dans localStorage pour
// être appliquée sans clignotement au chargement, puis alignée sur le serveur
// par loadPrefs() — voir 02-prefs.js.
function applyTheme(id){
  // Tolère les anciens identifiants (ajean, gpt-dark, soft…) : seule compte
  // désormais la variante, déduite du suffixe.
  const dark = id==='dark' || (typeof id==='string' && id.endsWith('-dark'));
  document.documentElement.setAttribute('data-theme', dark?'dark':'light');
  try{ localStorage.setItem('ajean-theme', dark?'dark':'light'); }catch(e){}
  const sw=document.getElementById('theme-dark'); if(sw) sw.checked=dark;
}
function toggleThemeDark(){
  applyTheme(document.getElementById('theme-dark').checked?'dark':'light');
  savePrefs();
}
function initTheme(){
  let id='light'; try{ id=localStorage.getItem('ajean-theme')||'light'; }catch(e){}
  applyTheme(id);
}
// ===== Affichage ============================================================
// Quatre interrupteurs indépendants. Chacun pose un attribut sur <html> ; le CSS
// fait le reste, sauf « replier les bulles » qui est un comportement (voir
// 08-chat-render).
const VIEW_OPTS=[
  {id:'hide-reasoning', label:'masquer le raisonnement'},
  {id:'hide-tools',     label:"masquer les appels d'outils"},
  {id:'fold-tools',     label:'garder les bulles repliées'},
  {id:'hide-side',      label:'barre latérale escamotable'},
  {id:'enter-newline',  label:'Entrée = retour à la ligne'},
];
function viewOn(id){ return document.documentElement.getAttribute('data-'+id)==='1'; }
function applyView(id, on){
  document.documentElement.setAttribute('data-'+id, on?'1':'0');
  try{ localStorage.setItem('ajean-'+id, on?'1':'0'); }catch(e){}
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
  VIEW_OPTS.forEach(o=>{
    let v=null; try{ v=localStorage.getItem('ajean-'+o.id); }catch(e){}
    applyView(o.id, v==='1');
  });
}
