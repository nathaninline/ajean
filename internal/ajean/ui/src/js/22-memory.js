// 22-memory.js — Monitoring mémoire centralisé (issue #133).
//
// Affiche les jauges RAM/VRAM en temps réel, les alertes de seuils,
// permet de déclencher un nettoyage manuel et de configurer les limites.
// Polling léger (10s) sur /api/memory/status.

(function(){
  'use strict';

  const POLL_INTERVAL = 10000; // 10 secondes
  let pollTimer = null;
  let lastSnapshot = null;

  // Éléments DOM
  const el = {
    details:   () => document.getElementById('memory-details'),
    badge:     () => document.getElementById('memory-badge'),
    ramVal:    () => document.getElementById('mem-ram-val'),
    ramBar:    () => document.getElementById('mem-ram-bar'),
    vramVal:   () => document.getElementById('mem-vram-val'),
    vramBar:   () => document.getElementById('mem-vram-bar'),
    alert:     () => document.getElementById('mem-alert'),
    cleanupBtn:() => document.getElementById('mem-cleanup-btn'),
    stats:     () => document.getElementById('mem-stats'),
    // Limites
    limitRamWarn:      () => document.getElementById('mem-limit-ram-warn'),
    limitRamCritical:  () => document.getElementById('mem-limit-ram-critical'),
    limitVramWarn:     () => document.getElementById('mem-limit-vram-warn'),
    limitVramCritical: () => document.getElementById('mem-limit-vram-critical'),
    limitMaxConv:      () => document.getElementById('mem-limit-max-conv'),
    limitMaxMsg:       () => document.getElementById('mem-limit-max-msg'),
  };

  // Démarre le polling quand la section est ouverte
  function init(){
    const d = el.details();
    if(!d) return;
    d.addEventListener('toggle', ()=>{
      if(d.open) startPolling(); else stopPolling();
    });
    if(d.open) startPolling();
    loadLimits();
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
      lastSnapshot = data.snapshot;
      render(data.snapshot, data.limits);
    }catch(e){ /* silencieux */ }
  }

  // Affiche le snapshot
  function render(snap, limits){
    if(!snap) return;

    // RAM
    const ramPct = snap.ram_used_percent || 0;
    const ramText = `${fmtMiB(snap.ram_used_mib)} / ${fmtMiB(snap.ram_total_mib)} (${ramPct.toFixed(0)}%)`;
    el.ramVal().textContent = ramText;
    el.ramBar().style.width = Math.min(100, ramPct) + '%';
    el.ramBar().className = 'mem-bar-fill ' + barClass(ramPct, limits?.RAMWarnPercent, limits?.RAMCriticalPercent);

    // VRAM
    const vramPct = snap.vram_used_percent || 0;
    const vramText = snap.vram_total_mib > 0
      ? `${fmtMiB(snap.vram_used_mib)} / ${fmtMiB(snap.vram_total_mib)} (${vramPct.toFixed(0)}%)`
      : 'N/A';
    el.vramVal().textContent = vramText;
    el.vramBar().style.width = snap.vram_total_mib > 0 ? Math.min(100, vramPct) + '%' : '0%';
    el.vramBar().className = 'mem-bar-fill ' + barClass(vramPct, limits?.VRAMWarnPercent, limits?.VRAMCriticalPercent);

    // Alerte
    const alertEl = el.alert();
    const isCritical = ramPct >= (limits?.RAMCriticalPercent || 90) || vramPct >= (limits?.VRAMCriticalPercent || 95);
    const isWarning  = ramPct >= (limits?.RAMWarnPercent || 80) || vramPct >= (limits?.VRAMWarnPercent || 85);
    if(isCritical){
      alertEl.style.display = 'block';
      alertEl.className = 'mem-alert mem-critical';
      alertEl.textContent = '🚨 Mémoire critique — nettoyage recommandé';
    } else if(isWarning){
      alertEl.style.display = 'block';
      alertEl.className = 'mem-alert mem-warning';
      alertEl.textContent = '⚠ Mémoire élevée — surveillance active';
    } else {
      alertEl.style.display = 'none';
    }

    // Badge
    const badge = el.badge();
    if(isCritical){ badge.style.display = ''; badge.textContent = '!'; badge.className = 'sbadge mem-critical'; }
    else if(isWarning){ badge.style.display = ''; badge.textContent = '⚠'; badge.className = 'sbadge mem-warning'; }
    else { badge.style.display = 'none'; }

    // Stats
    const stats = [];
    stats.push(`Conversations: ${snap.conversations_in_ram || 0}`);
    stats.push(`Nettoyages: ${snap.cleanup_count || 0}`);
    if(snap.last_cleanup && snap.last_cleanup !== '0001-01-01T00:00:00Z'){
      stats.push(`Dernier: ${new Date(snap.last_cleanup).toLocaleTimeString()}`);
    }
    if(snap.warnings_emitted > 0){
      stats.push(`Alertes: ${snap.warnings_emitted}`);
    }
    el.stats().textContent = stats.join(' · ');
  }

  // Classe CSS selon le niveau
  function barClass(pct, warn, critical){
    if(pct >= (critical || 95)) return 'mem-critical';
    if(pct >= (warn || 85)) return 'mem-warning';
    return 'mem-ok';
  }

  // Formate les MiB en lisible
  function fmtMiB(mib){
    if(mib >= 1024) return (mib/1024).toFixed(1) + ' GiB';
    return Math.round(mib) + ' MiB';
  }

  // Nettoyage manuel
  window.memCleanup = async function(){
    const btn = el.cleanupBtn();
    btn.disabled = true;
    btn.textContent = '⏳ Nettoyage…';
    try{
      const r = await fetch('/api/memory/cleanup', {method:'POST'});
      const data = await r.json();
      if(data.ok){
        const s = data.stats;
        toast(`🧹 Nettoyage effectué : ${s.conversations_evicted} conversations évincées, ${fmtMiB(s.ram_freed_mib)} libérés`);
        fetchStatus(); // refresh
      } else {
        toast('❌ Erreur: ' + (data.error || 'inconnue'));
      }
    }catch(e){
      toast('❌ Erreur réseau');
    }
    btn.disabled = false;
    btn.textContent = '🧹 Nettoyer la mémoire';
  };

  // Charge les limites actuelles
  async function loadLimits(){
    try{
      const r = await fetch('/api/memory/limits');
      const data = await r.json();
      if(!data.ok) return;
      const l = data.limits;
      el.limitRamWarn().value = l.RAMWarnPercent;
      el.limitRamCritical().value = l.RAMCriticalPercent;
      el.limitVramWarn().value = l.VRAMWarnPercent;
      el.limitVramCritical().value = l.VRAMCriticalPercent;
      el.limitMaxConv().value = l.MaxConversationsInRAM;
      el.limitMaxMsg().value = l.MaxConversationMessages;
    }catch(e){ /* silencieux */ }
  }

  // Sauvegarde les limites
  window.memSaveLimits = async function(){
    const body = {
      ram_warn_percent:        parseFloat(el.limitRamWarn().value),
      ram_critical_percent:    parseFloat(el.limitRamCritical().value),
      vram_warn_percent:       parseFloat(el.limitVramWarn().value),
      vram_critical_percent:   parseFloat(el.limitVramCritical().value),
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
        toast('💾 Limites enregistrées');
        fetchStatus();
      } else {
        toast('❌ Erreur: ' + (data.error || 'inconnue'));
      }
    }catch(e){
      toast('❌ Erreur réseau');
    }
  };

  // Toast simple (réutilise le système existant si disponible)
  function toast(msg){
    if(typeof window.showToast === 'function'){ window.showToast(msg); return; }
    if(typeof window.toast === 'function'){ window.toast(msg); return; }
    console.log(msg);
    alert(msg);
  }

  // Démarrage
  if(document.readyState === 'loading'){
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
