// Pastille d'état d'une section du menu (mode agent, internet, MCP, distant).
// `state` : true = actif (pilule pleine), false = inactif (contour), 'warn'/'err'
// = état signalé, null = pastille masquée. Un seul endroit décide de leur allure.
function setBadge(id, state, text){
  const el = typeof id==='string' ? document.getElementById(id) : id;
  if(!el) return;
  el.className = 'sbadge' + (state===true ? ' on' : (state==='warn' || state==='err') ? ' '+state : '');
  el.textContent = state===null || state===undefined ? '' : (text||'');
}
// Ouverture/fermeture des modales. Le display seul apparaissait d'un bloc : on
// pose la classe .show une frame APRÈS l'affichage pour que la transition CSS
// parte de son état initial (sans ce reflow forcé, le navigateur applique tout
// d'un coup et l'animation ne joue pas). À la fermeture on attend la fin de la
// transition avant de repasser en display:none.
function showModal(id){
  const el = typeof id==='string' ? document.getElementById(id) : id;
  if(!el) return;
  el.style.display='flex';
  void el.offsetHeight;
  el.classList.add('show');
}
function hideModal(id){
  const el = typeof id==='string' ? document.getElementById(id) : id;
  if(!el) return;
  el.classList.remove('show');
  // Rouverte entre-temps ? On ne la cache surtout pas.
  setTimeout(()=>{ if(!el.classList.contains('show')) el.style.display='none'; }, 180);
}
function toast(m){ const t=document.getElementById('toast'); t.textContent=m; t.classList.add('show'); setTimeout(()=>t.classList.remove('show'),1800); }
// Modales natives (askConfirm/askPrompt/askAlert) — remplacent confirm()/prompt()/
// alert() par de vraies boîtes stylées. Chacune renvoie une Promise : askConfirm →
// bool, askPrompt → string|null (null si annulé), askAlert → void. Échap/clic dehors
// = annuler ; Entrée = valider (sur prompt aussi).
let _askResolver=null, _askKind='confirm', _askCheck=false, _askMultiline=false;
// État de la case optionnelle de la DERNIÈRE confirmation (opts.check). Lu par
// l'appelant juste après le await — la Promise, elle, ne renvoie que oui/non.
function askChecked(){ return _askCheck; }
function askResolve(ok){
  if(!_askResolver) return;
  const r=_askResolver; _askResolver=null;
  _askCheck = ok && document.getElementById('ask-check-input').checked;
  hideModal('ask-modal');
  document.removeEventListener('keydown', _askKey, true);
  if(_askKind==='prompt') r(ok ? document.getElementById(_askMultiline?'ask-textarea':'ask-input').value : null);
  else r(ok);
}
function _askKey(e){
  if(e.key==='Escape'){ e.preventDefault(); e.stopPropagation(); askResolve(false); }
  // En multiligne, Entrée insère un saut de ligne (Ctrl/⌘+Entrée valide) ; sinon
  // Entrée valide directement.
  else if(e.key==='Enter'){
    if(_askKind==='prompt' && _askMultiline && !(e.ctrlKey||e.metaKey)) return;
    e.preventDefault(); e.stopPropagation(); askResolve(true);
  }
}
function _openAsk(kind, message, opts){
  // Compat 2 conventions : (message, opts) [UI ajean] ET l'objet unique
  // {title,msg,yes,no,placeholder} passé par e2e.js sur ajean.link (server.html,
  // où cette fonction remplace le window.askConfirm du bootstrap boîte noire).
  if(message && typeof message==='object'){ const o=message; opts={title:o.title, okText:o.yes, cancelText:o.no, placeholder:o.placeholder, default:o.default, danger:o.danger}; message=o.msg||''; }
  opts=opts||{};
  // Une modale déjà ouverte ne doit pas être écrasée en silence : son appelant
  // attend une réponse, et l'écraser laissait sa promesse pendante POUR
  // TOUJOURS (requête suspendue, bouton figé). On l'annule proprement d'abord.
  if(_askResolver){ const prev=_askResolver; _askResolver=null; prev(_askKind==='prompt' ? null : false); }
  _askKind=kind;
  document.getElementById('ask-title').textContent = opts.title || (kind==='alert'?t('uikit.info_title'):kind==='prompt'?t('uikit.prompt_title'):t('uikit.confirm_title'));
  document.getElementById('ask-msg').textContent = message||'';
  const inp=document.getElementById('ask-input'), ta=document.getElementById('ask-textarea');
  _askMultiline = kind==='prompt' && !!opts.multiline;
  if(kind==='prompt'){
    const el = _askMultiline ? ta : inp, other = _askMultiline ? inp : ta;
    el.style.display=''; el.value=opts.default||''; el.placeholder=opts.placeholder||'';
    other.style.display='none';
  } else { inp.style.display='none'; ta.style.display='none'; }
  // Case facultative (ex. « supprimer aussi le fichier .gguf »).
  const chk=document.getElementById('ask-check');
  chk.style.display = opts.check ? 'inline-flex' : 'none';
  document.getElementById('ask-check-label').textContent = opts.check || '';
  document.getElementById('ask-check-input').checked = !!opts.checkOn;
  _askCheck=!!opts.checkOn;
  const cancel=document.getElementById('ask-cancel'), ok=document.getElementById('ask-ok');
  cancel.style.display = kind==='alert' ? 'none' : '';
  cancel.textContent = opts.cancelText || t('uikit.cancel');
  ok.textContent = opts.okText || (kind==='alert'?t('uikit.ok'):kind==='prompt'?t('uikit.validate'):t('uikit.confirm'));
  ok.classList.toggle('danger', !!opts.danger);
  showModal('ask-modal');
  document.addEventListener('keydown', _askKey, true);
  setTimeout(()=>{ const el = _askMultiline?ta:inp; const f = kind==='prompt'?el:ok; f.focus(); if(kind==='prompt') el.select(); }, 30);
  return new Promise(res=>{ _askResolver=res; });
}
function askConfirm(message,opts){ return _openAsk('confirm',message,opts); }
function askPrompt(message,opts){ return _openAsk('prompt',message,opts); }
function askAlert(message,opts){ return _openAsk('alert',message,opts); }
