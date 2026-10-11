// 23-engine-mode.js — Mode core (allégé) : bascule l'UI d'Ajean entre le mode
// complet (chat + tout) et le mode core (moteur seul, optimisé pour un harness
// comme OpenFox/OpenCode qui gère lui-même le contexte).
//
// Le moteur llama-server n'est jamais touché : les harness s'y connectent
// directement sur /v1/*. Seules l'UI et les traitements de fond changent.
// Le changement est persisté côté serveur (clé ENGINE_ONLY) et nécessite un
// redémarrage de l'interface pour libérer réellement les ressources.

(function(){
  'use strict';

  // Éléments marqués data-engine-hide sont masqués en mode core.
  function applyEngineMode(engineOnly){
    document.body.classList.toggle('engine-only', engineOnly);
    const badge = document.getElementById('engine-only-badge');
    if(badge) badge.style.display = engineOnly ? '' : 'none';
    document.querySelectorAll('[data-engine-hide]').forEach(function(el){
      el.style.display = engineOnly ? 'none' : '';
    });
    // Le bouton historique de l'en-tête n'a plus de sens en mode core : on le
    // masque aussi (il vit dans #side-actions, déjà marqué, mais on sécurise).
    const histToggle = document.getElementById('hist-toggle');
    if(histToggle && engineOnly){ histToggle.style.display = 'none'; }
    else if(histToggle){ histToggle.style.display = ''; }
  }

  function setStatusText(engineOnly){
    const status = document.getElementById('engine-only-status');
    if(!status) return;
    status.textContent = engineOnly
      ? t('settings.engine_only_active')
      : t('settings.engine_only_inactive');
  }

  async function loadEngineMode(){
    try{
      const r = await fetch('/api/engine/mode');
      const d = await r.json();
      if(!d || !d.ok) return;
      const toggle = document.getElementById('engine-only-toggle');
      if(toggle) toggle.checked = !!d.engineOnly;
      applyEngineMode(!!d.engineOnly);
      setStatusText(!!d.engineOnly);
    }catch(e){ /* silencieux : pas de mode core si l'API ne répond pas */ }
  }

  window.toggleEngineOnly = async function(enable){
    const toggle = document.getElementById('engine-only-toggle');
    const msg = enable ? t('settings.engine_only_confirm_on') : t('settings.engine_only_confirm_off');
    if(!confirm(msg)){
      if(toggle) toggle.checked = !enable;
      return;
    }
    try{
      const r = await fetch('/api/engine/mode', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify({enable: !!enable}),
      });
      const d = await r.json();
      if(d && d.ok){
        // Applique tout de suite visuellement, puis recharge pour que le serveur
        // relance l'UI en mode core (libère conversation/mémoire/tâches).
        applyEngineMode(!!enable);
        setStatusText(!!enable);
        alert(t('settings.engine_only_reloading'));
        location.reload();
      } else {
        alert('Erreur: ' + ((d && d.error) || 'inconnue'));
        if(toggle) toggle.checked = !enable;
      }
    }catch(e){
      alert('Erreur réseau: ' + e.message);
      if(toggle) toggle.checked = !enable;
    }
  };

  function init(){
    loadEngineMode();
  }

  if(document.readyState === 'loading'){
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
