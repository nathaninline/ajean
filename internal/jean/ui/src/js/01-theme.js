// ===== Thèmes ================================================================
// Registre des thèmes disponibles. Pour en AJOUTER un : ajouter un bloc
// [data-theme="…"] dans le <style> puis une entrée {id,label} ici — le reste
// (menu déroulant, persistance localStorage, application) est automatique.
const THEMES=[{id:'dark',label:'sombre'},{id:'light',label:'clair'},{id:'soft',label:'doux'},{id:'soft-dark',label:'doux sombre'}];
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
  if(!THEMES.some(t=>t.id===id)) id='dark';
  if(id==='soft'||id==='soft-dark') ensureSoftFont();
  document.documentElement.setAttribute('data-theme', id);
  try{ localStorage.setItem('jean-theme', id); }catch(e){}
  const sel=document.getElementById('theme-select'); if(sel) sel.value=id;
}
function initTheme(){
  const sel=document.getElementById('theme-select');
  if(sel && !sel.options.length){
    THEMES.forEach(t=>{ const o=document.createElement('option'); o.value=t.id; o.textContent=t.label; sel.appendChild(o); });
  }
  let id='dark'; try{ id=localStorage.getItem('jean-theme')||'dark'; }catch(e){}
  applyTheme(id);
}
// ===== Mode d'affichage (complet / simple) ==================================
// "simple" masque raisonnement, appels d'outils, détails de vitesse et config
// active (via [data-display] + CSS). Persisté dans 'jean-display'.
function applyDisplay(mode){
  if(mode!=='simple') mode='full';
  document.documentElement.setAttribute('data-display', mode);
  try{ localStorage.setItem('jean-display', mode); }catch(e){}
  const sel=document.getElementById('display-select'); if(sel) sel.value=mode;
}
function initDisplay(){
  let m='full'; try{ m=localStorage.getItem('jean-display')||'full'; }catch(e){}
  applyDisplay(m);
}
