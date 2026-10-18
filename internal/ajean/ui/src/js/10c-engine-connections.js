// Named OpenAI-compatible connections use the original preset editor and chat.
// Saving does not start, install or reload an engine. Selection is explicit.
let engineConnections=[], engineConnectionEditor=null, engineConnectionOpenSeq=0;
function engineConnectionLabel(engine){ return {radiance:'Radiance',paiton:'Paiton'}[engine]||''; }
function resetEngineConnectionEditor(){
  ++engineConnectionOpenSeq;
  engineConnectionEditor=null;
  document.getElementById('modal').classList.remove('engine-connection');
  const key=document.getElementById('s-ext-key');if(key)key.placeholder='sk-…';
  for(const id of ['engine-connection-picker','engine-connection-note','engine-connection-select-row','engine-connection-effort-row']){
    const el=document.getElementById(id); if(el) el.hidden=true;
  }
}
async function loadEngineConnections(){
  try{
    const r=await jget('/api/engine/connections');
    if(!r.ok || !Array.isArray(r.connections)) return;
    engineConnections=r.connections;
    for(const engine of ['radiance','paiton']){
      const rows=engineConnections.filter(c=>c.engine===engine);
      const active=rows.find(c=>c.active);
      const status=document.getElementById('lc-'+engine+'-state');
      if(status) status.textContent=active ? t('moe.active')+' · '+active.model
        : rows.length ? t('engine.connection_count').replace('{n}',rows.length) : t('engine.connection_unconfigured');
    }
  }catch(_){
    for(const engine of ['radiance','paiton']){
      const status=document.getElementById('lc-'+engine+'-state');
      if(status) status.textContent=t('engine.connection_unavailable');
    }
  }
}
async function openEngineConnection(engine,id){
  if(!['radiance','paiton'].includes(engine)) return;
  const requestSequence=++engineConnectionOpenSeq;
  await loadEngineConnections();
  if(requestSequence!==engineConnectionOpenSeq)return;
  const connections=engineConnections.filter(c=>c.engine===engine);
  const connection=id===''?null:connections.find(c=>c.id===id)||connections.find(c=>c.active)||connections[0];
  // All fields, layout and modal keyboard behavior remain the original editor.
  const opening=openItem('preset','');
  const modalSequence=openSeq, connectionSequence=engineConnectionOpenSeq;
  await opening;
  if(openSeq!==modalSequence || engineConnectionOpenSeq!==connectionSequence)return;
  engineConnectionEditor={engine,id:connection?.id||'',keyTouched:false,dirty:false,models:[]};
  const modal=document.getElementById('modal');modal.classList.add('engine-connection');
  const label=engineConnectionLabel(engine);
  document.getElementById('modal-title').textContent=label;
  document.getElementById('m-name').value=connection?.name||label;
  document.getElementById('m-content').value='';
  cfgWriteKey('EXTERNAL','1');
  cfgWriteKey('EXTERNAL_URL',connection?.url||'');
  cfgWriteKey('EXTERNAL_MODEL',connection?.model||'');
  cfgWriteKey('CTX',connection?.ctx||'');
  cfgWriteKey('EXTERNAL_VISION',connection?.vision?'1':'');
  syncRunMode();
  const key=document.getElementById('s-ext-key');
  key.value=''; key.placeholder=connection?.hasKey?t('engine.connection_key_saved'):t('external.key_hint');
  document.getElementById('engine-connection-picker').hidden=false;
  document.getElementById('engine-connection-note').hidden=false;
  document.getElementById('engine-connection-effort-row').hidden=false;
  renderEngineConnectionEfforts(connection?.effort||'');
  const picker=document.getElementById('engine-connection-id');picker.replaceChildren();
  for(const c of connections){const option=document.createElement('option');option.value=c.id;option.textContent=c.name;picker.appendChild(option);}
  const fresh=document.createElement('option');fresh.value='';fresh.textContent=t('engine.connection_new');picker.appendChild(fresh);
  picker.value=engineConnectionEditor.id;
  document.getElementById('engine-connection-select-row').hidden=!connection;
  document.getElementById('engine-connection-select').disabled=!!connection?.active;
  document.getElementById('s-ext-status').textContent=connection?.active?t('moe.active'):'';
  document.getElementById('s-ext-model-list').replaceChildren();
  for(const id of ['m-name','s-ext-url','s-ext-model','s-ext-key','s-ext-ctx','s-ext-vision']){
    document.getElementById(id).addEventListener('input',markEngineConnectionDirty);
    document.getElementById(id).addEventListener('change',markEngineConnectionDirty);
  }
}
function markEngineConnectionDirty(){
  if(!engineConnectionEditor)return;
  if(engineConnectionEditor.models.length && this?.id==='s-ext-model' && engineConnectionEditor.effortModel!==this.value.trim()) renderEngineConnectionEfforts('');
  engineConnectionEditor.dirty=true;
  document.getElementById('engine-connection-select').disabled=true;
  document.getElementById('s-ext-status').textContent=t('engine.connection_save_first');
}
function renderEngineConnectionEfforts(current){
  const select=document.getElementById('s-ext-effort');select.replaceChildren();
  const option=document.createElement('option');option.value='';option.textContent=t('engine.connection_effort_default');select.appendChild(option);
  engineConnectionEditor.effortModel=document.getElementById('s-ext-model').value.trim();
  const model=engineConnectionEditor.models.find(m=>m.id===engineConnectionEditor.effortModel);
  const levels=(model?.capabilities?.reasoning_efforts||[]).filter(e=>/^(none|minimal|low|medium|high|xhigh)$/.test(e) && (engineConnectionEditor.engine==='radiance'||!['none','minimal'].includes(e)));
  for(const level of [...new Set(levels)]){const opt=document.createElement('option');opt.value=level;opt.textContent=level;select.appendChild(opt);}
  if(current && !levels.includes(current)){const saved=document.createElement('option');saved.value=current;saved.textContent=current+' · '+t('engine.connection_effort_saved');select.appendChild(saved);}
  select.value=current;
}
function engineConnectionPayload(){
  return {id:engineConnectionEditor.id,engine:engineConnectionEditor.engine,
    name:document.getElementById('m-name').value.trim(),
    url:document.getElementById('s-ext-url').value.trim(),model:document.getElementById('s-ext-model').value.trim(),
    key:document.getElementById('s-ext-key').value,keyTouched:engineConnectionEditor.keyTouched,
    ctx:document.getElementById('s-ext-ctx').value,effort:document.getElementById('s-ext-effort').value,vision:document.getElementById('s-ext-vision').checked};
}
async function testEngineConnection(){
  if(!engineConnectionEditor) return;
  const status=document.getElementById('s-ext-status');status.textContent=t('external.testing');
  try{
    const payload=engineConnectionPayload();
    const r=await jpost('/api/preset/external/test',payload);
    const list=document.getElementById('s-ext-model-list');list.replaceChildren();
    if(r.ok && Array.isArray(r.models)){engineConnectionEditor.models=r.models;renderEngineConnectionEfforts(document.getElementById('s-ext-effort').value);}
    if(r.ok && Array.isArray(r.models))for(const model of r.models){
      const option=document.createElement('option');option.value=model.id;list.appendChild(option);
    }
    status.textContent=r.ok && (!payload.model || r.model_present) ? '✓ '+t('engine.connection_test_ok')+(r.models?.length?' · '+r.models.map(m=>m.id).join(', '):'')
      : t('common.error_prefix')+(r.error||t('external.test_failed'));
  }catch(e){status.textContent=t('common.error_prefix')+(e.message||t('external.test_failed'));}
}
async function saveEngineConnection(){
  if(!engineConnectionEditor) return;
  const engine=engineConnectionEditor.engine;
  const status=document.getElementById('s-ext-status');
  try{
    const r=await jpost('/api/preset/external/save',engineConnectionPayload());
    if(!r.ok){status.textContent=t('common.error_prefix')+(r.error||'');return;}
    toast(t('models.saved'));
    await loadPresets();
    await openEngineConnection(engine,r.id);
  }catch(e){status.textContent=t('common.error_prefix')+e.message;}
}
async function selectEngineConnection(){
  if(!engineConnectionEditor?.id || engineConnectionEditor.dirty) return;
  const selected=engineConnections.find(c=>c.id===engineConnectionEditor.id);
  if(!selected) return;
  // Only the saved connection is selected; unsaved edits never change the chat.
  if(!await askConfirm(t('engine.connection_confirm').replace('{name}',selected.name),{title:t('models.switch_title'),okText:t('engine.connection_select')})) return;
  const button=document.getElementById('engine-connection-select');button.disabled=true;
  try{
    const r=await jpost('/api/engine/connections/select',{id:selected.id});
    if(!r.ok){document.getElementById('s-ext-status').textContent=t('common.error_prefix')+(r.error||'');return;}
    closeModal();resetEngineConnectionEditor();
    await loadAll();
  }catch(e){document.getElementById('s-ext-status').textContent=t('common.error_prefix')+e.message;}
  finally{button.disabled=false;}
}
