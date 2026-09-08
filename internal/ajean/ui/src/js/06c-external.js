// ─── Presets externes (API OpenAI-compatible distante) ───────────────────────
// Modale ouverte par le bouton wifi à gauche du « + » des presets. Crée/édite un
// preset qui, au lieu de lancer le moteur local, route le chat vers une API
// distante (OpenAI, Claude, Groq, OpenRouter, serveur local…). Backend :
// /api/preset/external{,/save,/test}. La bascule passe par le /api/switch normal.
let extEditing = null;   // id du preset en édition ('' = nouveau)
let extHasKey = false;   // une clé est déjà enregistrée (édition)
let extKeyTouched = false; // l'utilisateur a modifié le champ clé (posé par oninput)

async function openExternal(id){
  extEditing = id || '';
  extKeyTouched = false;
  extHasKey = false;
  let d = {name:'', url:'', model:'', hasKey:false};
  if(id){
    try{ d = await jget('/api/preset/external?id='+encodeURIComponent(id)); }catch(e){}
  }
  document.getElementById('ext-modal-title').textContent =
    id ? t('external.edit_prefix')+' « '+(d.name||id)+' »' : t('external.title');
  document.getElementById('ext-name').value = d.name || '';
  document.getElementById('ext-url').value = d.url || '';
  document.getElementById('ext-model').value = d.model || '';
  document.getElementById('ext-ctx').value = d.ctx || '';
  const key = document.getElementById('ext-key');
  extHasKey = !!d.hasKey;
  key.value = '';
  key.placeholder = extHasKey ? t('external.key_saved') : 'sk-…';
  document.getElementById('ext-del').style.display = id ? '' : 'none';
  const st = document.getElementById('ext-modal-status');
  st.textContent = ''; st.style.color = '';
  showModal('ext-modal');
}

function closeExternal(){ hideModal('ext-modal'); extEditing = null; }

// Corps commun envoyé au backend (save et test). keyTouched dit au serveur s'il
// doit conserver la clé existante (édition, champ non touché) ou prendre la
// nouvelle valeur.
function extBody(){
  return {
    id: extEditing || '',
    name: document.getElementById('ext-name').value.trim(),
    url: document.getElementById('ext-url').value.trim(),
    model: document.getElementById('ext-model').value.trim(),
    ctx: document.getElementById('ext-ctx').value.trim(),
    key: document.getElementById('ext-key').value,
    keyTouched: extKeyTouched,
  };
}

async function testExternal(){
  const b = extBody();
  if(!b.url || !b.model){ toast(t('external.url_model_required')); return; }
  const st = document.getElementById('ext-modal-status');
  st.textContent = t('external.testing'); st.style.color = '';
  try{
    const r = await jpost('/api/preset/external/test', b);
    if(r.ok){ st.innerHTML = '<span style="color:var(--accent)">✓</span> '+t('external.test_ok'); }
    else { st.textContent = '⚠ '+(r.error||t('external.test_failed')); st.style.color = 'var(--err)'; }
  }catch(e){ st.textContent = '⚠ '+t('external.test_failed'); st.style.color = 'var(--err)'; }
}

async function saveExternal(){
  const b = extBody();
  if(!b.name){ toast(t('external.name_required')); return; }
  if(!b.url){ toast(t('external.url_required')); return; }
  if(!b.model){ toast(t('external.model_required')); return; }
  const st = document.getElementById('ext-modal-status');
  st.textContent = t('external.saving'); st.style.color = '';
  const r = await jpost('/api/preset/external/save', b);
  if(!r.ok){ st.textContent = '⚠ '+(r.error||t('external.save_failed')); st.style.color = 'var(--err)'; return; }
  closeExternal();
  toast(t('external.saved'));
  loadPresets();
}

async function deleteExternal(){
  if(!extEditing) return;
  const nm = document.getElementById('ext-name').value.trim() || extEditing;
  if(!await askConfirm(t('external.confirm_delete_prefix')+' « '+nm+' » ?',
      {title:t('external.delete_title'), okText:t('external.delete_btn'), danger:true})) return;
  const r = await jpost('/api/preset/delete', {id: extEditing});
  if(r && r.ok === false){ toast(r.error||t('external.delete_failed')); return; }
  closeExternal();
  loadPresets();
  toast(t('external.deleted'));
}
