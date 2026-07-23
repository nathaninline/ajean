// ===== Conversation SERVEUR (source de vérité, partagée entre appareils) =====
// L'historique et la génération vivent sur le serveur jean. Le client ouvre un
// flux d'ABONNEMENT permanent (SSE) qui rejoue le journal depuis lastSeq puis
// suit le direct. Fermer l'onglet n'arrête plus la génération (détachée côté
// serveur) ; se reconnecter rejoue tout le fil, détails compris.
let lastSeq=0, streamAbort=null;
// REPLAYING = on est dans le replay initial (rejeu du journal au chargement).
// Pendant ce temps, les bulles raisonnement/outil sont créées DÉJÀ repliées →
// pas d'animation d'ouverture/fermeture au refresh. Le serveur envoie {caught_up}
// quand le replay est fini, on repasse alors en direct.
let REPLAYING=true;
// État de rendu du tour courant, délimité par les événements user / turn_done.
let T=null;
function newTurn(){ T={ reasonEl:null, contentEl:null, pendingToolEl:null, typingEl:null, fullContent:'', fullReason:'', turnCollapsibles:[], serverStats:null, reasonTok:0, contentTok:0, reasonFirstTs:0, reasonLastTs:0, contentFirstTs:0, contentLastTs:0 }; }
newTurn();
const simpleMode=()=>document.documentElement.getAttribute('data-display')==='simple';
function removeTyping(){ if(T.typingEl){ T.typingEl.remove(); T.typingEl=null; } }
function killTyping(){ if(!T.typingEl) return; if(simpleMode()) return; removeTyping(); }
function showTyping(){ if(!simpleMode()) return; const c=document.getElementById('chat');
  if(!T.typingEl){ T.typingEl=addTyping(); } else if(c.lastElementChild!==T.typingEl){ c.appendChild(T.typingEl); } }
// Label de vitesse rendu depuis les valeurs (fonctionne aussi bien en direct
// qu'au replay — pas de timer performance.now, qui n'a pas de sens hors-ligne).
function renderStats(el, s){
  if(!el||!s) return;
  const role=(el===T.contentEl)?'assistant':'reasoning';
  const parts=[role];
  const pt=s.prompt_tokens||s.prompt_tokens_total;
  if(pt) parts.push('prefill '+pt+' tok · '+(s.prompt_per_second||0).toFixed(0)+' tok/s');
  if(s.gen_tokens) parts.push('decode '+s.gen_tokens+' tok · '+(s.gen_per_second||0).toFixed(1)+' tok/s');
  setLabel(el, parts.join('  ·  '));
}
// Label d'une bulle : nombre de tokens + vitesse. La vitesse est calculée à
// partir des HORODATAGES SERVEUR (firstTs→lastTs) : le temps réel de génération,
// donc correct aussi bien en direct qu'au replay (où les deltas arrivent d'un
// bloc côté client, mais leurs ts serveur restent espacés du vrai temps écoulé).
function labelTokens(el, role, n, firstTs, lastTs){
  if(!el) return;
  const secs=(lastTs-firstTs)/1000;
  if(secs>0.05 && n>1){ setLabel(el, role+'  ·  '+n+' tok  ·  '+(n/secs).toFixed(1)+' tok/s'); }
  else { setLabel(el, role+'  ·  '+n+' tok'); }
}
// Pendant le replay on met à jour l'état `busy` mais on NE touche PAS aux boutons
// (sinon user→stop puis turn_done→send à chaque tour rejoué = flottement visible).
// L'état final est appliqué une seule fois au caught_up via syncSendBtn().
function setBusy(on){ busy=on; if(!REPLAYING) syncSendBtn(); }
function syncSendBtn(){
  document.getElementById('send').style.display=busy?'none':'inline-block';
  document.getElementById('stop').style.display=busy?'inline-block':'none';
}
// Traite UN événement du flux — même sémantique que l'ancien switch inline, mais
// piloté par le serveur et rejouable à l'identique.
function handleDelta(d){
  if(typeof d.seq==='number' && d.seq>lastSeq) lastSeq=d.seq;
  if(d.caught_up){
    // Fin du replay initial : on saute en bas puis on révèle (une seule fois — pas
    // sur les reconnexions, pour ne pas te ramener en bas si tu lisais plus haut).
    if(REPLAYING){ REPLAYING=false; jumpBottom(); syncSendBtn(); const c=chatEl(); c.style.transition='opacity .15s'; c.style.opacity='1'; }
    return; }
  if(d.reset!==undefined){ document.getElementById('chat').innerHTML=''; newTurn(); setCtxUsed(0); lastSeq=0; setBusy(false); const cb=document.getElementById('compact-banner'); if(cb) cb.style.display='none'; return; }
  if(d.user!==undefined){ newTurn(); addMsg('user', d.user); setBusy(true); T.typingEl=addTyping(); return; }
  if(d.turn_done){ removeTyping(); collapseAll(T.turnCollapsibles); if(T.serverStats) renderStats(T.contentEl||T.reasonEl, T.serverStats); setBusy(false); return; }
  if(d.error){ removeTyping(); T.contentEl=null; T.reasonEl=null; const eb=addMsg('assistant',''); eb.classList.add('errmsg'); renderBody(eb, d.error); return; }
  if(d.compacting!==undefined){ const b=document.getElementById('compact-banner'); if(b) b.style.display = d.compacting ? 'flex' : 'none'; return; }
  if(d.compacted){ const b=document.getElementById('compact-banner'); if(b) b.style.display='none'; toast('contexte compacté — les vieux tours ont été résumés'); return; }
  if(d.compact_noop){ const b=document.getElementById('compact-banner'); if(b) b.style.display='none'; toast('rien à compacter (contexte déjà minimal)'); return; }
  if(d.ctx_used!==undefined){ setCtxUsed(d.ctx_used); return; }
  if(d.stats){ T.serverStats=d.stats;
    if(d.stats.prompt_tokens_total){ setCtxUsed((d.stats.prompt_tokens_total||0)+(d.stats.gen_tokens||0)); }
    if(T.contentEl||T.reasonEl) renderStats(T.contentEl||T.reasonEl, d.stats); return; }
  if(d.tool_used){
    killTyping(); T.contentEl=null; T.reasonEl=null; const tu=d.tool_used;
    if(!T.pendingToolEl){ collapseAll(T.turnCollapsibles); T.pendingToolEl=addMsg('tool',''); if(REPLAYING) collapseInstant(T.pendingToolEl); T.turnCollapsibles.push(T.pendingToolEl); }
    renderToolMsg(T.pendingToolEl, tu);
    if(!tu.done) showTyping();
    if(tu.done){ T.pendingToolEl=null; if(tu.name==='mem_add'||tu.name==='mem_edit') loadMem(); }
    return; }
  if(d.drop_reasoning){
    if(T.reasonEl){ const i=T.turnCollapsibles.indexOf(T.reasonEl); if(i>=0) T.turnCollapsibles.splice(i,1); T.reasonEl.remove(); T.reasonEl=null; T.fullReason=''; }
    return; }
  if(d.reasoning_content){
    killTyping();
    if(!T.reasonEl){ collapseAll(T.turnCollapsibles); T.reasonEl=addMsg('reasoning',''); if(REPLAYING) collapseInstant(T.reasonEl); T.fullReason=''; T.turnCollapsibles.push(T.reasonEl); }
    showTyping(); T.fullReason+=d.reasoning_content; renderBody(T.reasonEl, T.fullReason);
    // d.toks/d.ts0 présents quand l'événement est coalescé (replay) : plusieurs
    // tokens d'un coup. Sinon (direct), 1 token, ts0=ts.
    if(!T.reasonFirstTs) T.reasonFirstTs=d.ts0||d.ts||0; T.reasonLastTs=d.ts||T.reasonLastTs; T.reasonTok+=(d.toks||1);
    labelTokens(T.reasonEl, 'reasoning', T.reasonTok, T.reasonFirstTs, T.reasonLastTs);
    return; }
  if(d.content){
    removeTyping();
    if(!T.contentEl){ collapseAll(T.turnCollapsibles); T.contentEl=addMsg('assistant',''); T.fullContent=''; }
    T.fullContent+=d.content; renderBody(T.contentEl, T.fullContent);
    if(!T.contentFirstTs) T.contentFirstTs=d.ts0||d.ts||0; T.contentLastTs=d.ts||T.contentLastTs; T.contentTok+=(d.toks||1);
    labelTokens(T.contentEl, 'assistant', T.contentTok, T.contentFirstTs, T.contentLastTs);
    return; }
}
// Flux d'abonnement permanent + reconnexion auto (from=lastSeq → pas de
// re-téléchargement complet après une coupure / bascule d'appareil).
async function connectStream(){
  while(true){
    streamAbort=new AbortController();
    try{
      const r=await jfetch('/api/chat',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({from:lastSeq}),signal:streamAbort.signal});
      const reader=r.body.getReader(); const dec=new TextDecoder(); let buf='';
      while(true){
        const {done,value}=await reader.read(); if(done) break;
        buf+=dec.decode(value,{stream:true}); let i;
        while((i=buf.indexOf('\n\n'))>=0){
          const chunk=buf.slice(0,i); buf=buf.slice(i+2);
          for(const line of chunk.split('\n')){
            if(!line.startsWith('data:')) continue;
            const data=line.slice(5).trim(); if(data===''||data==='[DONE]') continue;
            try{ const o=JSON.parse(data); const d=(o.choices&&o.choices[0]&&o.choices[0].delta)||{}; handleDelta(d); }catch(e){}
          }
        }
      }
    }catch(e){ /* coupure : on reconnecte silencieusement */ }
    await new Promise(res=>setTimeout(res, 600));
  }
}
// Interrompt la génération en cours côté serveur (la goroutine détachée est
// annulée). Le serveur émet alors turn_done → le bouton repasse en « send ».
function stopGen(){ jfetch('/api/chat/stop',{method:'POST'}).catch(()=>{}); toast('stop'); }
// Envoi RÉSILIENT : sur le tunnel E2E, un aller-retour peut échouer transitoirement
// alors qu'il a en fait abouti (la génération démarre). On réessaie, et un 409
// (« déjà en cours ») = succès (c'est notre envoi qui est passé). On ne montre une
// erreur qu'après plusieurs échecs ET vérification que rien ne tourne — plus de
// « network error » alarmiste alors que l'IA répond quand même.
async function send(){
  if(busy) return;
  const ta=document.getElementById('input'); const text=ta.value.trim();
  if(!text) return;
  ta.value='';
  for(let attempt=0; attempt<3; attempt++){
    try{
      const r=await jfetch('/api/chat/send',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({message:text,ctx_used:CTX_USED})});
      if(r.status===409) return;               // déjà en cours (notre envoi a abouti) → OK
      if(r.ok) return;                          // la bulle + les tokens arrivent par le flux
      if(r.status<500){ let m='erreur'; try{ m=(await r.json()).error||m; }catch(_){} toast(m); ta.value=text; return; }
    }catch(e){ /* réseau : on retente */ }
    await new Promise(res=>setTimeout(res, 600));
  }
  // Après plusieurs échecs : le serveur a peut-être quand même reçu le message.
  try{ const s=await (await jfetch('/api/chat/state')).json(); if(s.generating) return; }catch(_){}
  toast('échec de l\'envoi — réessaie'); ta.value=text;
}
loadAll();
setInterval(loadStatus, 5000);
setInterval(loadVram, 3000);
setInterval(loadRam, 3000);
