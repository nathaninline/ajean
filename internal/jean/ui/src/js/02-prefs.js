// ===== Persistance côté serveur (partagée entre appareils) ==================
// L'apparence est aussi enregistrée sur la machine jean (/api/prefs) : ainsi le
// thème/affichage choisi sur un appareil se retrouve sur tous les autres. Le
// localStorage reste utilisé pour appliquer instantanément au chargement (sans
// flash), puis loadPrefs() aligne sur la valeur du serveur (source de vérité).
function savePrefs(){
  let theme='dark', display='full';
  try{ theme=localStorage.getItem('jean-theme')||'dark'; display=localStorage.getItem('jean-display')||'full'; }catch(e){}
  jpost('/api/prefs', {theme, display}).catch(()=>{});
}
async function loadPrefs(){
  try{
    const p=await jget('/api/prefs');
    if(p && p.ok && p.prefs){
      if(p.prefs.theme) applyTheme(p.prefs.theme);
      if(p.prefs.display) applyDisplay(p.prefs.display);
    }
  }catch(e){}
}
document.addEventListener('DOMContentLoaded', ()=>{ initTheme(); initDisplay(); document.getElementById('sysprompt').value = localStorage.getItem('jean.sys') || ''; loadSys(); restoreChat(); });
