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
