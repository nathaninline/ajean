async function loadStatus(){
  const s=await jget('/api/status');
  const el=document.getElementById('status-svc');
  // Trois états : service coupé (err) · service actif mais modèle pas encore
  // chargé (loading, llama-server renvoie 503) · modèle prêt (ok).
  let cls='err', txt=s.state;
  if(s.active && s.health){ cls='ok'; txt='prêt'; }
  else if(s.active){ cls='loading'; txt='chargement…'; }
  el.className='statuspill '+cls;
  el.innerHTML='<span class="dot"></span>'+txt+' <span class="port">:'+s.port+'</span>';
  MODEL_READY = !!(s.active && s.health);
  if(s.ctx){ CTX_MAX=s.ctx; updateCtxMeter(); }
  if(s.version){
    document.getElementById('ver').textContent='v'+s.version;
  }
}
async function checkUpdate(){
  const b=document.getElementById('upd-check'), msg=document.getElementById('upd-msg');
  b.disabled=true; msg.textContent='Vérification…';
  try{
    const r=await jget('/api/update');
    if(r.error){ msg.textContent='Erreur : '+r.error; }
    else if(r.available){
      msg.innerHTML='Nouvelle version <b>v'+r.latest+'</b> disponible. ';
      const btn=document.createElement('button'); btn.textContent='Mettre à jour'; btn.onclick=applyUpdate;
      msg.appendChild(btn);
    } else { msg.textContent='Jean est à jour ✓'; }
  }catch(e){ msg.textContent='Erreur réseau'; }
  b.disabled=false;
}
async function applyUpdate(){
  const msg=document.getElementById('upd-msg');
  msg.textContent='Téléchargement et installation…';
  try{
    const r=await jpost('/api/update/apply',{});
    if(r.ok){
      msg.innerHTML='✓ Installé en <b>v'+r.version+'</b>.<br>'+(r.restart||'');
      // Redémarrage auto du service côté serveur : le flux va se couper puis
      // reconnecter tout seul (connectStream boucle). On rafraîchit l'état après.
      if(r.restarting){ toast('mise à jour appliquée — reconnexion…'); setTimeout(loadAll, 6000); }
    }
    else { msg.textContent='Échec : '+(r.error||'inconnu'); }
  }catch(e){ msg.textContent='Erreur pendant la mise à jour (réessaie).'; }
}
// Compteur de contexte : CTX_USED estimé via les stats serveur (prefill+decode
// du dernier tour ≈ taille du prochain prompt). À 90% on propose de compacter.
let CTX_MAX=0, CTX_USED=0, MODEL_READY=false;
function setCtxUsed(n){ CTX_USED=n||0; updateCtxMeter(); }
function updateCtxMeter(){
  if(!CTX_MAX) return;
  const pct=Math.min(100, Math.round(CTX_USED*100/CTX_MAX));
  const fill=document.getElementById('ctx-fill');
  fill.style.width=pct+'%';
  fill.style.background = pct>=90 ? 'var(--err,#c44)' : pct>=70 ? 'var(--warn,#c93)' : 'var(--ok,#3a7)';
  document.getElementById('ctx-text').textContent='contexte '+CTX_USED+' / '+CTX_MAX+' ('+pct+'%)';
  // Bouton de compaction MANUELLE : visible dès la moitié du contexte pour qu'on
  // puisse compacter à la demande avant que l'auto-compaction (75%) ne s'en charge.
  document.getElementById('ctx-compact').style.display = (pct>=50 && CTX_USED>0) ? 'inline-block' : 'none';
}
async function loadVram(){
  const gpus=await jget('/api/vram');
  document.getElementById('vram').innerHTML = (gpus||[]).map(g=>{
    const pct=Math.round(g.used*100/g.total);
    return '<div style="margin:6px 0"><div style="font-size:12px">'+g.name+'</div>'+
      '<div class="bar"><div style="width:'+pct+'%"></div></div>'+
      '<div class="muted">'+(g.used/1024).toFixed(1)+' / '+(g.total/1024).toFixed(1)+' GiB · GPU '+g.util+'% · '+g.temp+'°C</div></div>';
  }).join('') || '<span class="muted">(pas de GPU)</span>';
}
async function loadRam(){
  const m=await jget('/api/ram');
  const box=document.getElementById('ram-details');
  if(!m || !m.total){ if(box) box.style.display='none'; return; }
  if(box) box.style.display='';
  const pct=Math.round(m.used*100/m.total);
  document.getElementById('ram').innerHTML =
    '<div style="margin:6px 0"><div class="bar"><div style="width:'+pct+'%"></div></div>'+
    '<div class="muted">'+(m.used/1024).toFixed(1)+' / '+(m.total/1024).toFixed(1)+' GiB · '+pct+'%</div></div>';
}
async function loadCfg(){
  // /api/llamacpp en parallèle : il indique si le BIN de la config correspond au
  // moteur rapide (prebuilt.in_use) ou optimisé (in_use) — sinon c'est un custom.
  const [c, lc] = await Promise.all([jget('/api/config'), jget('/api/llamacpp').catch(()=>null)]);
  const row=(k,v,title)=>'<div class="kv"><span>'+k+'</span><span title="'+String(title!=null?title:v).replace(/"/g,'&quot;')+'">'+String(v)+'</span></div>';
  const rows=[];
  if(c.BIN){
    // Moteur : rapide / optimisé / custom (avec le chemin). Le title garde
    // toujours le chemin complet, quel que soit le libellé.
    let v;
    if(lc && lc.prebuilt && lc.prebuilt.in_use) v='⚡ rapide';
    else if(lc && lc.in_use) v='🔧 optimisé';
    else v='custom : '+c.BIN;
    rows.push(row('MOTEUR', v, c.BIN));
  }
  ['MODEL','CTX','BATCH','UBATCH','NGL'].filter(k=>c[k]).forEach(k=>{
    let v=c[k]; if(k==='MODEL') v=v.split('/').pop();
    rows.push(row(k, v));
  });
  // n-cpu-moe : affiché seulement s'il est réellement présent dans EXTRA_ARGS.
  const m=(c.EXTRA_ARGS||'').match(/--n-cpu-moe\s+(\d+)/);
  if(m) rows.push(row('N-CPU-MOE', m[1]));
  document.getElementById('cfg').innerHTML = rows.join('');
}
