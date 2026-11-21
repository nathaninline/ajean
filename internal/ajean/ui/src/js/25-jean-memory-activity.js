// 25-jean-memory-activity.js : Jean range sa mémoire en arrière-plan (révision,
// consolidation : jean_activity.go). Une pastille en haut le dit pendant le
// travail, puis montre ce qui a changé quelques secondes. Mode Jean seulement.
let jeanMemSeen = null, jeanMemHideT = 0, jeanMemDone = false;
function jeanMemPill(){
  let p = document.getElementById('jean-mem-pill');
  if(!p){
    const main = document.querySelector('.main'); if(!main) return null;
    p = document.createElement('div'); p.id = 'jean-mem-pill'; p.setAttribute('role', 'status');
    p.innerHTML = '<svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><ellipse cx="12" cy="5" rx="8" ry="3"/><path d="M4 5v6c0 1.7 3.6 3 8 3s8-1.3 8-3V5"/><path d="M4 11v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6"/></svg><span class="jmp-txt"></span>';
    p.onclick = () => { if(typeof openJeanMem === 'function') openJeanMem(); };
    main.appendChild(p);
  }
  return p;
}
function jeanMemActivity(a){
  if(typeof MODE === 'undefined' || MODE !== 'jean' || !a) { const p = document.getElementById('jean-mem-pill'); if(p) p.classList.remove('show'); return; }
  const p = jeanMemPill(); if(!p) return;
  const txt = p.querySelector('.jmp-txt');
  if(a.running){
    clearTimeout(jeanMemHideT);
    const n = (a.changes || []).length;
    txt.textContent = tf('jeanmem.running', 'Mise à jour de la mémoire…') + (n ? ' (' + n + ')' : '');
    p.title = (a.changes || []).join('\n');
    p.classList.add('show', 'working');
    return;
  }
  p.classList.remove('working');
  // Premier relevé : on note seulement, pour ne pas rejouer un ancien résultat.
  if(jeanMemSeen === null){ jeanMemSeen = a.last || 0; p.classList.remove('show'); return; }
  if(a.last && a.last > jeanMemSeen && (a.changes || []).length){
    jeanMemSeen = a.last;
    txt.textContent = tf('jeanmem.done', 'Mémoire mise à jour') + ' : ' + a.changes.slice(0, 3).join(' · ') + (a.changes.length > 3 ? ' · +' + (a.changes.length - 3) : '');
    p.title = a.changes.join('\n');
    p.classList.add('show');
    jeanMemDone = true;
    clearTimeout(jeanMemHideT);
    jeanMemHideT = setTimeout(() => { jeanMemDone = false; p.classList.remove('show'); }, 8000);
  } else if(!jeanMemDone){
    p.classList.remove('show');
  }
}
