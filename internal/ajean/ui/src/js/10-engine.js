// Moteur AJEAN : un llama.cpp épinglé (version testée avec cette version
// d'AJEAN), en plus des trois moteurs de la 0.18. Le suivi d'installation passe
// par le job llama.cpp habituel (/api/llamacpp/job, actions engine / engine-repair).
let engineState = null;

async function loadEngine(){
  let e;
  try{ e = await jget('/api/engine'); }catch(_){ return; }
  engineState = e;
  const card = document.getElementById('lc-mode-ajean');
  const state = document.getElementById('lc-ajean-state');
  const title = document.getElementById('lc-ajean-title');
  if(!card || !state) return;
  if(title) title.textContent = t('engine.mode_ajean_title') + (e.version ? ' (' + e.version + ')' : '');
  card.classList.toggle('installed', !!e.installed);
  const esc = s => String(s == null ? '' : s).replace(/[<>&"]/g, '');
  let h = '';
  if(e.installed){
    h += '<span class="lc-mode-active-tag">' + t('llamacpp.installed_tag') + '</span>';
    if(e.label) h += '<span class="lc-mode-d">' + esc(e.label) + '</span> ';
    if(e.in_use) h += '<span class="lc-mode-active-tag">' + t('engine.ajean_in_use') + '</span>';
    h += '<span class="lc-update-link" onclick="event.stopPropagation();engineRepair()">' + t('engine.ajean_repair') + '</span>';
  } else {
    h += '<span class="lc-mode-go">' + t('llamacpp.click_to_install') + '</span>';
  }
  const v = Array.isArray(e.variants) ? e.variants : [];
  if(v.length > 1){
    h += ' <select id="lc-ajean-variant" onclick="event.stopPropagation()" onchange="engineRepair(this.value)">'
      + '<option value="">' + t('engine.ajean_variant_auto') + '</option>'
      + v.map(x => '<option value="' + esc(x) + '"' + (e.forced === x ? ' selected' : '') + '>' + esc(x) + '</option>').join('')
      + '</select>';
  }
  state.innerHTML = h;
}

async function enginePick(){
  const e = engineState || {};
  if(e.installed){ toast(t('llamacpp.already_installed')); return; }
  if(!await askConfirm(t('engine.ajean_confirm_body'), {title:t('engine.mode_ajean_title'), okText:t('llamacpp.install_btn')})) return;
  const r = await jpost('/api/engine/install', {});
  if(!r.ok){ toast(t('llamacpp.error_prefix') + (r.error || '')); return; }
  lcStartPolling();
}

async function engineRepair(variant){
  const body = {};
  if(typeof variant === 'string') body.variant = variant;
  else if(!await askConfirm(t('engine.ajean_repair_body'), {title:t('engine.ajean_repair'), okText:t('engine.ajean_repair')})) return;
  const r = await jpost('/api/engine/repair', body);
  if(!r.ok){ toast(t('llamacpp.error_prefix') + (r.error || '')); return; }
  lcStartPolling();
}
