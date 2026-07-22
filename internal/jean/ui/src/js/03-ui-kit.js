function toast(m){ const t=document.getElementById('toast'); t.textContent=m; t.classList.add('show'); setTimeout(()=>t.classList.remove('show'),1800); }
// Modales natives (askConfirm/askPrompt/askAlert) — remplacent confirm()/prompt()/
// alert() par de vraies boîtes stylées. Chacune renvoie une Promise : askConfirm →
// bool, askPrompt → string|null (null si annulé), askAlert → void. Échap/clic dehors
// = annuler ; Entrée = valider (sur prompt aussi).
let _askResolver=null, _askKind='confirm';
function askResolve(ok){
  if(!_askResolver) return;
  const r=_askResolver; _askResolver=null;
  document.getElementById('ask-modal').style.display='none';
  document.removeEventListener('keydown', _askKey, true);
  if(_askKind==='prompt') r(ok ? document.getElementById('ask-input').value : null);
  else r(ok);
}
function _askKey(e){
  if(e.key==='Escape'){ e.preventDefault(); e.stopPropagation(); askResolve(false); }
  else if(e.key==='Enter'){ e.preventDefault(); e.stopPropagation(); askResolve(true); }
}
function _openAsk(kind, message, opts){
  // Compat 2 conventions : (message, opts) [UI jean] ET l'objet unique
  // {title,msg,yes,no,placeholder} passé par e2e.js sur ajean.link (server.html,
  // où cette fonction remplace le window.askConfirm du bootstrap boîte noire).
  if(message && typeof message==='object'){ const o=message; opts={title:o.title, okText:o.yes, cancelText:o.no, placeholder:o.placeholder, default:o.default, danger:o.danger}; message=o.msg||''; }
  opts=opts||{};
  _askKind=kind;
  document.getElementById('ask-title').textContent = opts.title || (kind==='alert'?'Info':kind==='prompt'?'Saisie':'Confirmation');
  document.getElementById('ask-msg').textContent = message||'';
  const inp=document.getElementById('ask-input');
  if(kind==='prompt'){ inp.style.display=''; inp.value=opts.default||''; inp.placeholder=opts.placeholder||''; }
  else inp.style.display='none';
  const cancel=document.getElementById('ask-cancel'), ok=document.getElementById('ask-ok');
  cancel.style.display = kind==='alert' ? 'none' : '';
  cancel.textContent = opts.cancelText || 'Annuler';
  ok.textContent = opts.okText || (kind==='alert'?'OK':kind==='prompt'?'Valider':'Confirmer');
  ok.classList.toggle('danger', !!opts.danger);
  document.getElementById('ask-modal').style.display='flex';
  document.addEventListener('keydown', _askKey, true);
  setTimeout(()=>{ const f = kind==='prompt'?inp:ok; f.focus(); if(kind==='prompt') inp.select(); }, 30);
  return new Promise(res=>{ _askResolver=res; });
}
function askConfirm(message,opts){ return _openAsk('confirm',message,opts); }
function askPrompt(message,opts){ return _openAsk('prompt',message,opts); }
function askAlert(message,opts){ return _openAsk('alert',message,opts); }
