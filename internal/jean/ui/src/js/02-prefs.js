// ===== Persistance côté serveur (partagée entre appareils) ==================
// L'apparence est aussi enregistrée sur la machine jean (/api/prefs) : ainsi le
// thème/affichage choisi sur un appareil se retrouve sur tous les autres. Le
// localStorage reste utilisé pour appliquer instantanément au chargement (sans
// flash), puis loadPrefs() aligne sur la valeur du serveur (source de vérité).
function savePrefs(){
  let theme='light', font='auto';
  try{
    theme=localStorage.getItem('jean-theme')||'light';
    font=localStorage.getItem('jean-font')||'auto';
  }catch(e){}
  const p={theme, font};
  VIEW_OPTS.forEach(o=>{ p[o.id.replace('-','_')] = viewOn(o.id)?'1':'0'; });
  jpost('/api/prefs', p).catch(()=>{});
}
async function loadPrefs(){
  try{
    const p=await jget('/api/prefs');
    if(p && p.ok && p.prefs){
      if(p.prefs.theme) applyTheme(p.prefs.theme);
      if(p.prefs.font) applyFont(p.prefs.font);
      VIEW_OPTS.forEach(o=>{
        const v=p.prefs[o.id.replace('-','_')];
        if(v!==undefined) applyView(o.id, v==='1');
      });
    }
  }catch(e){}
}
document.addEventListener('DOMContentLoaded', ()=>{ initTheme(); initFont(); initView(); document.getElementById('sysprompt').value = localStorage.getItem('jean.sys') || ''; loadSys(); restoreChat(); });
