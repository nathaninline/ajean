// Paramètres > Sécurité : le mot de passe de l'interface (web_password.go).
//
// /api/auth est public : il dit seulement s'il y a un mot de passe et si CETTE
// page est entrée — de quoi choisir les lignes à montrer. Rien de secret ne passe
// par la page : le mot de passe part au serveur, la session revient dans un
// cookie HttpOnly.
let SEC_AUTH = null;
async function loadSecurity(){
  const box = document.getElementById('sec-details'); if(!box) return;
  let a; try{ a = await jget('/api/auth'); }catch(e){ return; }
  SEC_AUTH = a;
  const show = (id, on) => { const el = document.getElementById(id); if(el) el.style.display = on ? '' : 'none'; };
  const st = document.getElementById('sec-pw-state');
  let msg;
  if(a.password){
    msg = t('security.password_on');
    if(typeof a.sessions === 'number') msg += ' · ' + a.sessions + ' ' + t(a.sessions > 1 ? 'security.sessions_plural' : 'security.sessions_singular');
    if(a.key) msg += ' · ' + t('security.key_also');
    if(!a.https && !a.remote && !/^(localhost|127\.|\[::1\])/.test(location.host)) msg += '\n' + t('security.http_warning');
  } else if(a.key){
    msg = t('security.key_only');
  } else {
    msg = t('security.open_warning');
  }
  if(!a.password && !a.can_set) msg += '\n' + t('security.first_from_machine');
  st.textContent = msg; st.style.whiteSpace = 'pre-line';
  document.getElementById('sec-pw-set-lbl').textContent = t(a.password ? 'security.password_change' : 'security.password_set');
  show('sec-pw-set', a.password || a.can_set);
  show('sec-pw-remove', a.password);
  show('sec-pw-logout', a.password && a.session);
  show('sec-pw-others', a.password && a.session && a.sessions > 1);
  setBadge('sec-badge', a.password ? true : (a.key ? true : 'warn'), a.password ? t('security.badge_password') : (a.key ? t('security.badge_key') : t('security.badge_open')));
  if(a.allowed){ loadSecretsVault(); loadReview(); loadActionsLogState(); }
}
// Réponse d'une route /api/auth/* : message lisible pour un refus.
function secError(r, j){
  if(r.status === 429) return t('auth.login_wait_prefix') + ((j && j.wait) || 60) + t('auth.login_wait_suffix');
  return (j && j.error) || ('HTTP ' + r.status);
}
async function secPost(path, body){
  const r = await jfetch(path, {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(body||{})});
  let j = {}; try{ j = await r.json(); }catch(_){}
  return {r, j};
}
// Saisie d'un nouveau mot de passe, deux fois. Renvoie null si abandonné.
async function secAskNewPassword(){
  const min = (SEC_AUTH && SEC_AUTH.min) || 10;
  for(;;){
    const pw = await askPrompt(t('security.new_prompt_prefix') + min + t('security.new_prompt_suffix'), {title:t('security.password_subhead'), placeholder:t('auth.password_placeholder'), password:true, newPassword:true});
    if(!pw) return null;
    if([...pw].length < min){ await askAlert(t('security.too_short_prefix') + min + t('security.too_short_suffix')); continue; }
    const again = await askPrompt(t('security.confirm_prompt'), {title:t('security.password_subhead'), placeholder:t('auth.password_placeholder'), password:true, newPassword:true});
    if(again === null) return null;
    if(again !== pw){ await askAlert(t('security.mismatch')); continue; }
    return pw;
  }
}
async function secSetPassword(){
  const a = SEC_AUTH || {};
  let current = '';
  if(a.password){
    current = await askPrompt(t('security.current_prompt'), {title:t('security.password_change'), placeholder:t('auth.password_placeholder'), password:true});
    if(!current) return;
  }
  const pw = await secAskNewPassword();
  if(!pw) return;
  let res; try{ res = await secPost('/api/auth/password', {current, password:pw}); }catch(e){ await askAlert(t('common.failed_prefix') + e); return; }
  if(!res.r.ok){ await askAlert(secError(res.r, res.j), {title:t('settings.refused_title')}); return; }
  toast(t('security.password_saved'));
  loadSecurity();
}
async function secRemovePassword(){
  if(!await askConfirm(t('security.remove_confirm_msg'), {title:t('security.password_remove'), okText:t('security.password_remove'), danger:true})) return;
  const current = await askPrompt(t('security.current_prompt'), {title:t('security.password_remove'), placeholder:t('auth.password_placeholder'), password:true});
  if(!current) return;
  let res; try{ res = await secPost('/api/auth/password', {current, remove:true}); }catch(e){ await askAlert(t('common.failed_prefix') + e); return; }
  if(!res.r.ok){ await askAlert(secError(res.r, res.j), {title:t('settings.refused_title')}); return; }
  toast(t('security.password_removed'));
  loadSecurity();
}
async function secLogout(others){
  let res; try{ res = await secPost('/api/auth/logout', {others:!!others}); }catch(e){ await askAlert(t('common.failed_prefix') + e); return; }
  if(!res.r.ok){ await askAlert(secError(res.r, res.j)); return; }
  if(others){ toast((res.j.closed||0) + ' ' + t('security.sessions_closed')); loadSecurity(); return; }
  // Session fermée : on recharge, la page redemandera le mot de passe.
  location.reload();
}

// --- Coffre des clés d'API (secrets_vault.go) ----------------------------------
// L'interface ne voit jamais une valeur : seulement combien de clés sont au
// coffre, combien dorment encore en clair, et si la clé du coffre est lisible.
function renderSecretsVault(s){
  const tog = document.getElementById('sec-vault-toggle'); if(!tog || !s) return;
  tog.checked = !!s.on;
  const st = document.getElementById('sec-vault-status');
  let msg = '';
  if(s.count) msg = s.count + ' ' + t(s.count > 1 ? 'security.vault_count_plural' : 'security.vault_count_singular');
  if(s.plain) msg += (msg ? ' · ' : '') + s.plain + ' ' + t('security.vault_plain');
  if(s.key_ok === false) msg += (msg ? '\n' : '') + t('security.vault_key_missing');
  st.textContent = msg; st.style.whiteSpace = 'pre-line';
}
async function loadSecretsVault(){
  try{ renderSecretsVault(await jget('/api/secrets')); }catch(e){}
}
async function toggleSecretsVault(){
  const tog = document.getElementById('sec-vault-toggle');
  const on = tog.checked;
  if(!on && !await askConfirm(t('security.vault_off_confirm'), {title:t('security.vault_subhead'), okText:t('security.vault_off_ok')})){ tog.checked = true; return; }
  let r; try{ r = await jpost('/api/secrets', {on}); }catch(e){ tog.checked = !on; await askAlert(t('common.failed_prefix') + e); return; }
  renderSecretsVault(r);
  const skipped = (r.report && r.report.skipped) || [];
  if(!r.ok || skipped.length){
    await askAlert((r.error ? r.error + '\n\n' : '') + skipped.join('\n'), {title:t('security.vault_subhead')});
    return;
  }
  toast((r.report ? r.report.moved : 0) + ' ' + t(on ? 'security.vault_moved_in' : 'security.vault_moved_out'));
}
// --- Validation de ce que l'IA planifie (review.go) ----------------------------
// Éteinte par défaut. Allumée, ce que le modèle a créé, modifié ou supprimé parmi
// les tâches (et le contenu des scripts qu'elles lancent) s'affiche en tête de la
// section Tâches à la FIN de sa réponse, et ne part pas seul avant l'accord.
let REVIEW_ON = false, REVIEW = null;
function reviewItemEl(kindCls, kindText, name, detail, when){
  const d = document.createElement('details'); d.className = 'review-item';
  const s = document.createElement('summary');
  const k = document.createElement('span'); k.className = 'review-kind ' + kindCls; k.textContent = kindText;
  const n = document.createElement('span'); n.className = 'review-name'; n.textContent = name;
  s.appendChild(k); s.appendChild(n);
  if(when){ const w = document.createElement('span'); w.className = 'action-when'; w.textContent = when; s.appendChild(w); }
  const p = document.createElement('div'); p.className = 'review-detail'; p.textContent = detail || '';
  d.appendChild(s); d.appendChild(p);
  return d;
}
function renderReview(r){
  REVIEW_ON = !!(r && r.on);
  const prev = REVIEW ? REVIEW.id : '';
  REVIEW = (r && r.review) || null;
  const tog = document.getElementById('sec-review-toggle'); if(tog) tog.checked = REVIEW_ON;
  const box = document.getElementById('review-box'); if(!box) return;
  if(!REVIEW || !REVIEW.items || !REVIEW.items.length){ box.style.display = 'none'; return; }
  const list = document.getElementById('review-items'); list.textContent = '';
  REVIEW.items.forEach(it => {
    const el = reviewItemEl(it.change, t('review.kind_' + it.change), it.name, it.detail);
    if(REVIEW.items.length === 1) el.open = true;
    list.appendChild(el);
  });
  box.style.display = '';
  // Une liste NOUVELLE : on la met sous les yeux (section Tâches ouverte + rappel).
  if(REVIEW.id !== prev){
    const sec = document.getElementById('tasks-details'); if(sec) sec.open = true;
    toast(t('review.toast'));
  }
}
// retries : après un tour, le serveur peut être encore occupé (busy) ; on
// relit toutes les 1,5 s, au plus « retries » fois.
async function loadReview(retries){
  let r = null;
  try{ r = await jget('/api/review'); renderReview(r); }catch(e){}
  if(r && r.busy && retries > 0) setTimeout(()=>loadReview(retries - 1), 1500);
}
async function toggleReview(){
  const tog = document.getElementById('sec-review-toggle'); const on = tog.checked;
  let r; try{ r = await jpost('/api/review', {on}); }catch(e){ tog.checked = !on; await askAlert(t('common.failed_prefix') + e); return; }
  if(!r.ok){ tog.checked = !on; await askAlert(t('common.failed_prefix') + (r.error||t('common.unknown'))); return; }
  renderReview(r);
  toast(t(on ? 'review.enabled' : 'review.disabled'));
  if(typeof loadTasks === 'function') loadTasks();
}
async function answerReview(accept){
  if(!REVIEW) return;
  if(!accept && !await askConfirm(t('review.reject_confirm'), {title:t('review.reject'), okText:t('review.reject'), danger:true})) return;
  let r; try{ r = await jpost('/api/review', {id:REVIEW.id, accept}); }catch(e){ await askAlert(t('common.failed_prefix') + e); return; }
  // Liste changée entre-temps : on montre la nouvelle, sans rien appliquer.
  if(!r.ok){ renderReview(r); await askAlert(r.error || t('common.unknown'), {title:t('settings.refused_title')}); return; }
  renderReview(r);
  toast(t(accept ? 'review.accepted' : 'review.rejected'));
  if(typeof loadTasks === 'function') loadTasks();
}

// --- Journal des actions (actions_log.go) ---------------------------------------
async function loadActionsLogState(){
  try{ const r = await jget('/api/actions?limit=1'); const tog = document.getElementById('sec-actions-toggle'); if(tog) tog.checked = !!r.on; }catch(e){}
}
async function toggleActionsLog(){
  const tog = document.getElementById('sec-actions-toggle'); const on = tog.checked;
  try{ const r = await jpost('/api/actions', {on}); tog.checked = !!r.on; }catch(e){ tog.checked = !on; }
}
async function showActionsLog(){
  const box = document.getElementById('sec-actions-list'); if(!box) return;
  if(box.style.display !== 'none'){ box.style.display = 'none'; return; }
  let r; try{ r = await jget('/api/actions?limit=40'); }catch(e){ return; }
  box.textContent = '';
  const acts = (r && r.actions) || [];
  if(!acts.length){
    const p = document.createElement('div'); p.className = 'muted'; p.style.fontSize = '12px';
    p.textContent = t(r && r.locked ? 'security.actions_locked' : 'security.actions_empty');
    box.appendChild(p);
  }
  acts.forEach(a => {
    const who = a.task ? a.task : t('security.actions_scope_' + (a.scope === 'jean' ? 'jean' : 'chat'));
    const res = a.done ? (a.result || '') : t('security.actions_no_result');
    const d = new Date(a.ts);
    box.appendChild(reviewItemEl('', a.name, a.label || who, who + '\n' + res, isNaN(d) ? '' : d.toLocaleString()));
  });
  box.style.display = '';
}