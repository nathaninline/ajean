// ===== Persistance côté serveur (partagée entre appareils) ==================
// L'apparence est aussi enregistrée sur la machine ajean (/api/prefs) : ainsi le
// thème/affichage choisi sur un appareil se retrouve sur tous les autres. Le
// localStorage reste utilisé pour appliquer instantanément au chargement (sans
// flash), puis loadPrefs() aligne sur la valeur du serveur (source de vérité).
function savePrefs(){
  let theme='light';
  try{ theme=localStorage.getItem('ajean-theme')||'light'; }catch(e){}
  const p={theme, lang: currentLang()};
  VIEW_OPTS.forEach(o=>{ p[o.id.replace('-','_')] = viewOn(o.id)?'1':'0'; });
  jpost('/api/prefs', p).catch(()=>{});
}
async function loadPrefs(){
  try{
    const p=await jget('/api/prefs');
    if(p && p.ok && p.prefs){
      // Un thème enregistré par une version précédente (ajean, gpt-dark…) est
      // normalisé par applyTheme ; on réenregistre pour purger le serveur.
      if(p.prefs.theme){
        applyTheme(p.prefs.theme);
        if(p.prefs.theme!=='light' && p.prefs.theme!=='dark') savePrefs();
      }
      if(p.prefs.lang && I18N[p.prefs.lang]){
        try{ localStorage.setItem('ajean-lang', p.prefs.lang); }catch(e){}
        applyI18n();
        const sel=document.getElementById('lang-select');
        if(sel) sel.value=p.prefs.lang;
      }
      VIEW_OPTS.forEach(o=>{
        const v=p.prefs[o.id.replace('-','_')];
        if(v!==undefined) applyView(o.id, v==='1');
      });
    }
  }catch(e){}
}
document.addEventListener('DOMContentLoaded', ()=>{ initTheme(); initView(); restoreChat(); });
