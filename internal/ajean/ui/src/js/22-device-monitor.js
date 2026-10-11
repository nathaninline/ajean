// 22-device-monitor.js — Monitoring mémoire avancé intégré dans la section Device.
//
// Complète les jauges RAM/VRAM existantes (05-status.js) avec :
// - Alertes de seuils (warning/critical)
// - Bouton de nettoyage manuel
// - Configuration des limites mémoire
// - Statistiques (conversations, nettoyages)
//
// Issue #133 : plantage OOM multi-GPU. Ce module surveille /api/memory/status
// et affiche les alertes dans la section Device (pas une section séparée).

(function(){
  'use strict';

  const POLL_INTERVAL = 10000; // 10 secondes
  let pollTimer = null;

  // Éléments DOM (préfixe dev-mon- pour éviter les conflits)
  const el = {
    details:   () => document.getElementById('device-details'),
    badge:     () => document.getElementById('dev-mon-badge'),
    alert:     () => document.getElementById('dev-mon-alert'),
    cleanupBtn:() => document.getElementById('dev-mon-cleanup-btn'),
    stats:     () => document.getElementById('dev-mon-stats'),
    // Limites
    limitRamWarn:     () => document.getElementById('dev-mon-ram-warn'),
    limitRamCrit:     () => document.getElementById('dev-mon-ram-crit'),
    limitVramWarn:    () => document.getElementById('dev-mon-vram-warn'),
    limitVramCrit:    () => document.getElementById('dev-mon-vram-crit'),
    limitMaxConv:     () => document.getElementById('dev-mon-max-conv'),
    limitMaxMsg:      () => document.getElementById('dev-mon-max-msg'),
  };

  // Démarre le polling quand la section Device est ouverte
  function init(){
    const d = el.details();
    if(!d) return;
    d.addEventListener('toggle', ()=>{
      if(d.open) startPolling(); else stopPolling();
    });
    if(d.open) startPolling();
    loadLimits();
    // Charge le journal mémoire au démarrage (lecture légère, pas de polling).
    window.devMonRefreshLog();
  }

  function startPolling(){
    stopPolling();
    fetchStatus();
    pollTimer = setInterval(fetchStatus, POLL_INTERVAL);
  }

  function stopPolling(){
    if(pollTimer){ clearInterval(pollTimer); pollTimer = null; }
  }

  // Récupère le snapshot mémoire
  async function fetchStatus(){
    try{
      const r = await fetch('/api/memory/status');
      if(!r.ok) return;
      const data = await r.json();
      if(!data.ok) return;
      render(data.snapshot, data.limits);
    }catch(e){ /* silencieux */ }
  }

  // Affiche le snapshot
  function render(snap, limits){
    if(!snap) return;

    const ramPct = snap.ram_used_percent || 0;
    const vramPct = snap.vram_used_percent || 0;
    const ramWarn = limits?.RAMWarnPercent || 80;
    const ramCrit = limits?.RAMCriticalPercent || 90;
    const vramWarn = limits?.VRAMWarnPercent || 85;
    const vramCrit = limits?.VRAMCriticalPercent || 95;

    // Déterminer le niveau d'alerte
    const isCritical = ramPct >= ramCrit || vramPct >= vramCrit;
    const isWarning  = ramPct >= ramWarn || vramPct >= vramWarn;

    // Alerte
    const alertEl = el.alert();
    if(isCritical){
      alertEl.style.display = 'block';
      alertEl.className = 'dev-mon-alert dev-mon-critical';
      alertEl.textContent = '🚨 Mémoire critique — nettoyage recommandé';
    } else if(isWarning){
      alertEl.style.display = 'block';
      alertEl.className = 'dev-mon-alert dev-mon-warning';
      alertEl.textContent = '⚠ Mémoire élevée — surveillance active';
    } else {
      alertEl.style.display = 'none';
    }

    // Badge dans le titre
    const badge = el.badge();
    if(isCritical){ badge.style.display = ''; badge.textContent = '!'; badge.className = 'sbadge dev-mon-critical'; }
    else if(isWarning){ badge.style.display = ''; badge.textContent = '⚠'; badge.className = 'sbadge dev-mon-warning'; }
    else { badge.style.display = 'none'; }

    // Stats
    const stats = [];
    stats.push(`${snap.conversations_in_ram || 0} conv.`);
    stats.push(`${snap.cleanup_count || 0} nettoyages`);
    if(snap.last_cleanup && snap.last_cleanup !== '0001-01-01T00:00:00Z'){
      stats.push(`dernier: ${new Date(snap.last_cleanup).toLocaleTimeString()}`);
    }
    el.stats().textContent = stats.join(' · ');
  }

  // Nettoyage manuel
  window.devMonCleanup = async function(){
    const btn = el.cleanupBtn();
    btn.disabled = true;
    const originalText = btn.textContent;
    btn.textContent = '⏳…';
    try{
      const r = await fetch('/api/memory/cleanup', {method:'POST'});
      const data = await r.json();
      if(data.ok){
        const s = data.stats;
        devMonToast(`🧹 ${s.conversations_evicted} conv. évincées, ${fmtMiB(s.ram_freed_mib)} libérés`);
        fetchStatus();
      } else {
        devMonToast('❌ Erreur: ' + (data.error || 'inconnue'));
      }
    }catch(e){
      devMonToast('❌ Erreur réseau');
    }
    btn.disabled = false;
    btn.textContent = originalText;
  };

  // Charge les limites actuelles
  async function loadLimits(){
    try{
      const r = await fetch('/api/memory/limits');
      const data = await r.json();
      if(!data.ok) return;
      const l = data.limits;
      el.limitRamWarn().value = l.RAMWarnPercent;
      el.limitRamCrit().value = l.RAMCriticalPercent;
      el.limitVramWarn().value = l.VRAMWarnPercent;
      el.limitVramCrit().value = l.VRAMCriticalPercent;
      el.limitMaxConv().value = l.MaxConversationsInRAM;
      el.limitMaxMsg().value = l.MaxConversationMessages;
    }catch(e){ /* silencieux */ }
  }

  // Sauvegarde les limites
  window.devMonSaveLimits = async function(){
    const body = {
      ram_warn_percent:         parseFloat(el.limitRamWarn().value),
      ram_critical_percent:     parseFloat(el.limitRamCrit().value),
      vram_warn_percent:        parseFloat(el.limitVramWarn().value),
      vram_critical_percent:    parseFloat(el.limitVramCrit().value),
      max_conversations_in_ram: parseInt(el.limitMaxConv().value),
      max_conversation_messages: parseInt(el.limitMaxMsg().value),
    };
    try{
      const r = await fetch('/api/memory/limits', {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(body),
      });
      const data = await r.json();
      if(data.ok){
        devMonToast('💾 Limites enregistrées');
        fetchStatus();
      } else {
        devMonToast('❌ Erreur: ' + (data.error || 'inconnue'));
      }
    }catch(e){
      devMonToast('❌ Erreur réseau');
    }
  };

  // Toast (réutilise le système existant)
  function devMonToast(msg){
    if(typeof window.showToast === 'function'){ window.showToast(msg); return; }
    if(typeof window.toast === 'function'){ window.toast(msg); return; }
    console.log(msg);
  }

  // Formate les MiB
  function fmtMiB(mib){
    if(mib >= 1024) return (mib/1024).toFixed(1) + ' GiB';
    return Math.round(mib) + ' MiB';
  }

  // Charge et affiche les dernières lignes du journal mémoire persistant.
  window.devMonRefreshLog = async function(){
    const box = document.getElementById('dev-mon-log');
    if(!box) return;
    try{
      const r = await fetch('/api/memory/log?n=100');
      const d = await r.json();
      if(!d || !d.ok){ box.textContent = t('device.mon_log_error'); return; }
      if(!d.lines || d.lines.length === 0){ box.textContent = t('device.mon_log_empty'); return; }
      // Les lignes sont déjà horodatées ; on les affiche telles quelles.
      box.textContent = d.lines.join('\n');
      box.scrollTop = box.scrollHeight;
    }catch(e){
      box.textContent = t('device.mon_log_error');
    }
  };

  // Démarrage
  if(document.readyState === 'loading'){
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
