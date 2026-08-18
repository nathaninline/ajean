// ===== Conversation SERVEUR (source de vérité, partagée entre appareils) =====
// L'historique et la génération vivent sur le serveur ajean. Le client ouvre un
// flux d'ABONNEMENT permanent (SSE) qui rejoue le journal depuis lastSeq puis
// suit le direct. Fermer l'onglet n'arrête plus la génération (détachée côté
// serveur) ; se reconnecter rejoue tout le fil, détails compris.
let lastSeq=0, streamAbort=null;
// Bulle « en attente » : affichée EN GRIS dès l'appui sur envoyer, avant tout
// aller-retour réseau. Le message ne disparaît donc plus de l'écran entre la
// frappe et la réponse du serveur. Elle s'éclaircit (classe retirée) quand
// l'événement `user` revient par le flux — preuve que le serveur l'a bien
// enregistré. En cas d'échec d'envoi, elle est retirée et le texte est rendu.
let PENDING=null;
// Retire aussi la rangée de pièces jointes, qui vit JUSTE AVANT la bulle : sans
// ça, un envoi échoué laissait les fichiers seuls dans le fil, sans message.
function clearPending(){
  if(!PENDING) return;
  const f=PENDING.previousElementSibling;
  if(f&&f.classList.contains('msg-files')) f.remove();
  PENDING.remove(); PENDING=null;
}
function addPending(text){
  clearPending();
  PENDING=addMsg('user', text);
  PENDING.classList.add('pending');
  const l=PENDING.querySelector('.label'); if(l) l.textContent='envoi…';
  jumpBottom();
  return PENDING;
}
// Le serveur confirme le message : on réutilise la bulle grise au lieu d'en
// ajouter une seconde (sinon le message clignoterait en double).
function confirmPending(text){
  if(!PENDING) return false;
  const b=PENDING.querySelector('.body');
  if(!b || b.textContent!==text) return false;
  PENDING.classList.remove('pending');
  const l=PENDING.querySelector('.label'); if(l) l.textContent='user';
  PENDING=null;
  return true;
}
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
// Compactage : on ÉTIQUETTE l'indicateur de frappe déjà à l'écran au lieu
// d'ouvrir une bannière à part (on avait les deux en même temps pour un seul
// état d'attente). En fin de tour l'indicateur a déjà été retiré : on le
// recrée, en le marquant pour le reprendre quand le compactage est fini.
function setCompacting(on){
  if(on){
    if(!T.typingEl){ T.typingEl=addTyping(); T.typingEl.dataset.forCompact='1'; }
    T.typingEl.classList.add('compacting');
    if(!T.typingEl.querySelector('.tlabel')){
      const s=document.createElement('span');
      s.className='tlabel';
      s.textContent='compactage du contexte…';
      T.typingEl.appendChild(s);
    }
    scrollMaybe();
    return;
  }
  if(!T.typingEl) return;
  if(T.typingEl.dataset.forCompact){ removeTyping(); return; }
  T.typingEl.classList.remove('compacting');
  const s=T.typingEl.querySelector('.tlabel'); if(s) s.remove();
}
// Séparateur laissé dans le fil à l'endroit exact de la coupure.
function addCompactMark(){
  const el=document.createElement('div');
  el.className='compact-mark';
  el.textContent='contexte compacté — anciens tours résumés';
  // Devant l'indicateur de frappe s'il est encore là (compactage en cours de
  // tour) : la suite de la réponse doit rester APRÈS la marque de coupure.
  if(T.typingEl && T.typingEl.parentNode===chatEl()) chatEl().insertBefore(el, T.typingEl);
  else chatEl().appendChild(el);
  scrollMaybe();
}
// L'indicateur « … » est retiré dès que quelque chose de VISIBLE le remplace.
// Il doit donc survivre quand la bulle qui arrive ne sera pas affichée : mode
// simplifié, mais aussi raisonnement/outils masqués par les préférences — sinon
// le fil reste totalement vide pendant que le modèle travaille (rien à voir, et
// aucun signe que ça tourne).
const typingKept=(kind)=>simpleMode()
  || (kind==='reasoning' && viewOn('hide-reasoning'))
  || (kind==='tool' && viewOn('hide-tools'));
function killTyping(kind){ if(!T.typingEl) return; if(typingKept(kind)) return; removeTyping(); }
function showTyping(kind){ if(!typingKept(kind)) return; const c=document.getElementById('chat');
  if(!T.typingEl){ T.typingEl=addTyping(); } else if(c.lastElementChild!==T.typingEl){ c.appendChild(T.typingEl); } }
// Label de vitesse rendu depuis les valeurs (fonctionne aussi bien en direct
// qu'au replay — pas de timer performance.now, qui n'a pas de sens hors-ligne).
function renderStats(el, s){
  if(!el||!s) return;
  const parts=[];
  const pt=s.prompt_tokens||s.prompt_tokens_total;
  if(pt) parts.push('prefill '+pt+' tok · '+(s.prompt_per_second||0).toFixed(0)+' tok/s');
  if(s.gen_tokens) parts.push('decode '+s.gen_tokens+' tok · '+(s.gen_per_second||0).toFixed(1)+' tok/s');
  if(!parts.length) return;
  // Réponse de l'assistant : ligne de mesures dédiée sous le texte (son étiquette
  // est masquée dans cette mise en page). Bulle repliable : l'étiquette EST le
  // bouton de repli, on y écrit comme avant.
  if(el.classList.contains('collapsible')) setLabel(el, ['reasoning'].concat(parts).join('  ·  '));
  else setStats(el, parts.join('  ·  '));
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
// RUNNING_TASK = nom de la tâche de fond qui occupe le moteur (vide = un tour
// utilisateur normal). Sert à distinguer les deux dans le bouton stop : sinon une
// tâche qui tourne fait croire à l'utilisateur qu'il génère lui-même.
let RUNNING_TASK='';
function syncSendBtn(){
  const sb=document.getElementById('send');
  sb.style.display=busy?'none':'inline-block';
  const stop=document.getElementById('stop');
  stop.style.display=busy?'inline-block':'none';
  // Étiquette explicite quand c'est une TÂCHE qui occupe le moteur.
  if(busy && RUNNING_TASK){
    stop.textContent='arrêter la tâche';
    stop.title='Tâche « '+RUNNING_TASK+' » en cours';
  } else {
    stop.textContent='stop';
    stop.title='';
  }
  // Tant que le moteur n'a pas fini de charger le modèle, envoyer ne mène à rien :
  // on bloque le bouton et l'Entrée, et on le DIT sous le champ. `STATUS_SEEN`
  // évite de verrouiller le chat quand /api/status n'a pas encore répondu (ou ne
  // répond pas du tout) — dans le doute on laisse la main.
  const ready = !STATUS_SEEN || MODEL_READY;
  sb.disabled = !ready;
  sb.title = ready ? '' : 'le modèle n\'est pas encore chargé';
  const hint=document.getElementById('sendhint');
  if(hint){
    hint.textContent = ready ? 'Entrée pour envoyer · Maj+Entrée = nouvelle ligne'
                             : 'Le modèle charge — envoi possible dès qu\'il est prêt.';
    hint.classList.toggle('waiting', !ready);
  }
}
// Rendu THROTTLÉ du bloc en cours de streaming (issue #24). Re-parser le Markdown
// du bloc ENTIER à chaque token est en O(n²) : sur un long raisonnement (des
// dizaines de milliers de tokens) l'app se met à ramer, les tokens arrivent au
// ralenti, et un refresh « répare » tout simplement parce qu'il rend le bloc une
// seule fois. On coalesce donc : le texte s'accumule (concat, quasi gratuit) et on
// ne re-rend qu'à intervalle borné. Le rendu final exact est garanti par
// flushRender(), appelé à chaque frontière de bloc (nouvel élément, outil, fin de
// tour, caught_up).
let renderTimer=null, renderPending=null, lastRenderMs=0; // {el, text}
function scheduleRender(el, text){
  // Changement de bloc en cours de route : on rend d'abord l'ancien à sa dernière
  // valeur, sinon son ultime bout de texte serait perdu.
  if(renderPending && renderPending.el!==el) flushRender();
  renderPending={el, text};
  if(renderTimer) return;
  // Cadence ADAPTATIVE : on vise à ne pas passer plus d'~1/6 du temps à re-parser
  // le Markdown. Tant que le bloc est petit, un rendu coûte 1-2 ms → plancher 16 ms
  // ≈ 60 img/s, l'apparition reste fluide token par token. Quand le raisonnement
  // devient énorme, le rendu coûte cher et on espace tout seul jusqu'à 500 ms, ce
  // qui casse le O(n²) sans jamais figer l'interface (issue #24).
  const delay=Math.min(500, Math.max(16, lastRenderMs*6));
  renderTimer=setTimeout(flushRender, delay);
}
function flushRender(){
  if(renderTimer){ clearTimeout(renderTimer); renderTimer=null; }
  const p=renderPending; renderPending=null;
  if(!p) return;
  const t0=performance.now();
  renderBody(p.el, p.text);
  lastRenderMs=performance.now()-t0;
}
// Lissage d'apparition (« machine à écrire »). Le MTP et le dual-GPU débitent les
// tokens PAR RAFALES : plusieurs tokens acceptés d'un coup, puis une pause → le
// texte grandit par paquets et saute à l'écran. On découple donc l'ARRIVÉE (les
// rafales réseau) de l'AFFICHAGE : `target` = tout ce qui est reçu, `shown` avance
// à cadence régulière (requestAnimationFrame) et on n'affiche que le préfixe
// révélé. Le rendu passe TOUJOURS par scheduleRender pour conserver le throttle
// anti-O(n²) (issue #24) : sur un bloc énorme le lissage devient grossier, mais
// on ne perçoit plus les à-coups token par token de toute façon.
// UNIQUEMENT EN DIRECT : au replay, tout est posé d'un bloc (voir handleDelta),
// sinon relire le fil deviendrait une lente réécriture caractère par caractère.
let smooth=null, smoothLast=0; // {el, target, shown, raf}
function smoothReset(){ if(smooth&&smooth.raf) cancelAnimationFrame(smooth.raf); smooth=null; smoothLast=0; }
// Solde le bloc courant : on affiche TOUT immédiatement. Appelé à chaque frontière
// (fin de tour, outil, changement de bloc) — sinon l'ultime bout de texte resterait
// « en retard » derrière le curseur de révélation.
function smoothSnap(){ if(!smooth) return; const el=smooth.el, target=smooth.target; smoothReset(); scheduleRender(el, target); }
// Débit BASÉ SUR LE TEMPS (indépendant du frame-rate). La vitesse de révélation
// est proportionnelle au RETARD accumulé, avec une constante de temps TAU : le
// curseur traîne volontairement ~TAU derrière l'arrivée, ce qui donne un
// écoulement CONTINU malgré les rafales du MTP, et se vide en douceur (drain
// exponentiel) quand la génération s'arrête. Un plancher garantit un progrès
// même quand le retard est minuscule, sans jamais figer.
const SMOOTH_TAU=260; // ms — plus grand = plus lisse mais traîne davantage
function smoothStep(ts){
  if(!smooth){ return; }
  if(!smoothLast) smoothLast=ts;
  const dt=Math.min(120, ts-smoothLast); smoothLast=ts;
  const remaining=smooth.target.length - smooth.shown;
  if(remaining<=0){ smooth.raf=null; smoothLast=0; return; }
  let adv=remaining*dt/SMOOTH_TAU;      // vitesse ∝ retard
  if(adv<0.4) adv=0.4;                  // progrès minimal
  smooth.shown=Math.min(smooth.target.length, smooth.shown+Math.ceil(adv));
  scheduleRender(smooth.el, smooth.target.slice(0, smooth.shown));
  smooth.raf=requestAnimationFrame(smoothStep);
}
function smoothFeed(el, target){
  if(smooth && smooth.el!==el) smoothSnap();   // changement de bloc : solder l'ancien
  if(!smooth) smooth={el, target, shown:0, raf:null};
  smooth.target=target;
  if(!smooth.raf) smooth.raf=requestAnimationFrame(smoothStep);
}
// Rend un bloc en streaming : lissé en direct, instantané au replay.
function feedBlock(el, full){ if(REPLAYING) scheduleRender(el, full); else smoothFeed(el, full); }
// Traite UN événement du flux — même sémantique que l'ancien switch inline, mais
// piloté par le serveur et rejouable à l'identique.
function handleDelta(d){
  if(typeof d.seq==='number' && d.seq>lastSeq) lastSeq=d.seq;
  if(d.caught_up){
    smoothSnap(); flushRender(); // rendre le dernier bloc rejoué à sa valeur exacte
    // Fin du replay initial : on saute en bas puis on révèle (une seule fois — pas
    // sur les reconnexions, pour ne pas te ramener en bas si tu lisais plus haut).
    setChatLoading(null);
    if(REPLAYING){ REPLAYING=false; jumpBottom(); syncSendBtn(); const c=chatEl(); c.style.transition='opacity .15s'; c.style.opacity='1'; }
    // Le bouton envoyer/stop doit refléter l'état RÉEL du serveur, pas seulement les
    // événements rejoués (issue #24) : un reload en pleine génération pouvait laisser
    // « envoyer » alors que l'IA répondait encore, car l'événement `user` qui met
    // busy=true n'est pas toujours redéroulé sur une reconnexion depuis lastSeq. On
    // se recale donc sur /api/chat/state, source de vérité de « generating ».
    reconcileBusy();
    // Fil vide : aucune bulle n'a été rejouée, donc aucune mutation ne viendra
    // déclencher la synchro — c'est ici qu'on décide d'afficher l'accueil.
    syncChatEmpty();
    return; }
  if(d.reset!==undefined){ smoothReset(); if(renderTimer){ clearTimeout(renderTimer); renderTimer=null; } renderPending=null; PENDING=null; document.getElementById('chat').innerHTML=''; newTurn(); setCtxUsed(0); lastSeq=0; setBusy(false); return; }
  if(d.user!==undefined){
    newTurn();
    let el=PENDING;
    if(!confirmPending(d.user)) el=addMsg('user', d.user);
    // Pièces jointes du tour : rendues DANS la bulle. La bulle en attente en
    // porte déjà (posées à l'envoi), on ne les ajoute donc qu'au replay/à une
    // bulle neuve — sinon elles apparaîtraient en double.
    if(d.files && !hasMsgFiles(el)) addMsgFiles(el, d.files);
    setBusy(true); T.typingEl=addTyping(); return; }
  if(d.turn_done){ smoothSnap(); flushRender(); removeTyping(); collapseAll(T.turnCollapsibles); if(T.serverStats) renderStats(T.contentEl||T.reasonEl, T.serverStats); setBusy(false); return; }
  if(d.error){ smoothSnap(); flushRender(); removeTyping(); T.contentEl=null; T.reasonEl=null; const eb=addMsg('assistant',''); eb.classList.add('errmsg'); renderBody(eb, d.error); return; }
  if(d.compacting!==undefined){ setCompacting(d.compacting); return; }
  if(d.compacted){ setCompacting(false); addCompactMark(); return; }
  // Pas de toast au REPLAY : le journal est rejoué à chaque chargement de page,
  // donc une notification persistée se re-déclenchait à chaque rafraîchissement
  // (« rien à compacter » qui revient sans raison). C'est un événement ponctuel,
  // il n'a de sens qu'en direct — contrairement à la marque `compacted`, qui est
  // une trace du fil et DOIT être rejouée.
  if(d.compact_noop){ setCompacting(false); if(!REPLAYING) toast('rien à compacter (contexte déjà minimal)'); return; }
  if(d.ctx_used!==undefined){ setCtxUsed(d.ctx_used); return; }
  if(d.stats){ T.serverStats=d.stats;
    if(d.stats.prompt_tokens_total){ setCtxUsed((d.stats.prompt_tokens_total||0)+(d.stats.gen_tokens||0)); }
    if(T.contentEl||T.reasonEl) renderStats(T.contentEl||T.reasonEl, d.stats); return; }
  if(d.tool_used){
    smoothSnap(); flushRender(); // le bloc texte précédent (raisonnement/contenu) est terminé
    killTyping('tool'); T.contentEl=null; T.reasonEl=null; const tu=d.tool_used;
    if(!T.pendingToolEl){ collapseAll(T.turnCollapsibles); T.pendingToolEl=addMsg('tool',''); if(REPLAYING||viewOn('fold-tools')) collapseInstant(T.pendingToolEl); T.turnCollapsibles.push(T.pendingToolEl); }
    renderToolMsg(T.pendingToolEl, tu);
    // Outils masqués : on garde l'indicateur même quand l'appel est terminé (le
    // tour continue, et rien d'autre n'est visible). Sinon, comportement inchangé.
    if(!tu.done || viewOn('hide-tools')) showTyping('tool');
    if(tu.done){ T.pendingToolEl=null; if(tu.name==='mem_add'||tu.name==='mem_edit') loadMem(); }
    return; }
  if(d.drop_reasoning){
    smoothReset(); if(renderTimer){ clearTimeout(renderTimer); renderTimer=null; } renderPending=null; // le bloc raisonnement disparaît
    if(T.reasonEl){ const i=T.turnCollapsibles.indexOf(T.reasonEl); if(i>=0) T.turnCollapsibles.splice(i,1); T.reasonEl.remove(); T.reasonEl=null; T.fullReason=''; }
    return; }
  if(d.reasoning_content){
    killTyping('reasoning');
    if(!T.reasonEl){ collapseAll(T.turnCollapsibles); T.reasonEl=addMsg('reasoning',''); if(REPLAYING||viewOn('fold-tools')) collapseInstant(T.reasonEl); T.fullReason=''; T.turnCollapsibles.push(T.reasonEl); }
    // d.replace : le serveur renvoie le bloc ENTIER alors qu'on en affichait déjà
    // le début (voir decorateEvent/coalesceReplay côté serveur) → on repart de zéro
    // au lieu de concaténer, sinon le texte apparaît en double.
    if(d.replace){ smoothSnap(); T.fullReason=''; T.reasonTok=0; T.reasonFirstTs=0; }
    showTyping('reasoning'); T.fullReason+=d.reasoning_content; feedBlock(T.reasonEl, T.fullReason);
    // d.toks/d.ts0 présents quand l'événement est coalescé (replay) : plusieurs
    // tokens d'un coup. Sinon (direct), 1 token, ts0=ts.
    if(!T.reasonFirstTs) T.reasonFirstTs=d.ts0||d.ts||0; T.reasonLastTs=d.ts||T.reasonLastTs; T.reasonTok+=(d.toks||1);
    labelTokens(T.reasonEl, 'reasoning', T.reasonTok, T.reasonFirstTs, T.reasonLastTs);
    return; }
  if(d.content){
    removeTyping();
    if(!T.contentEl){ collapseAll(T.turnCollapsibles); T.contentEl=addMsg('assistant',''); T.fullContent=''; }
    if(d.replace){ smoothSnap(); T.fullContent=''; T.contentTok=0; T.contentFirstTs=0; }
    T.fullContent+=d.content; feedBlock(T.contentEl, T.fullContent);
    if(!T.contentFirstTs) T.contentFirstTs=d.ts0||d.ts||0; T.contentLastTs=d.ts||T.contentLastTs; T.contentTok+=(d.toks||1);
    labelTokens(T.contentEl, 'assistant', T.contentTok, T.contentFirstTs, T.contentLastTs);
    return; }
}
// Flux d'abonnement permanent + reconnexion auto (from=lastSeq → pas de
// re-téléchargement complet après une coupure / bascule d'appareil).
// Un onglet caché RELÂCHE son flux SSE. Sans ça, chaque onglet AJEAN laissé
// ouvert monopolise une des ~6 connexions simultanées autorisées par domaine :
// au-delà, toute requête (journal, installation du moteur…) reste en file
// d'attente sans jamais partir ni échouer — un blocage silencieux très
// déroutant. Au retour de l'onglet on se reconnecte, et le replay depuis
// lastSeq rattrape tout ce qui s'est passé entre-temps.
let streamPaused=false;
document.addEventListener('visibilitychange', ()=>{
  if(document.hidden){
    streamPaused=true;
    if(streamAbort) try{ streamAbort.abort(); }catch(e){}
  } else if(streamPaused){
    streamPaused=false;
  }
});
async function connectStream(){
  while(true){
    // Onglet en arrière-plan : on n'ouvre aucune connexion, on attend le retour.
    while(document.hidden){ await new Promise(res=>setTimeout(res, 500)); }
    streamAbort=new AbortController();
    try{
      const r=await jfetch('/api/chat',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({from:lastSeq}),signal:streamAbort.signal});
      if(REPLAYING) setChatLoading('chargement de la conversation…');
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
    // Le flux s'est arrêté (coupure ou fin prématurée). Si le fil n'a JAMAIS fini
    // de charger, le silence est trompeur — un chat vide sans explication. On le
    // dit dans le voile ; il disparaîtra au {caught_up} de la reconnexion.
    if(REPLAYING) setChatLoading('connexion au serveur…');
    await new Promise(res=>setTimeout(res, 600));
  }
}
// Interrompt la génération en cours côté serveur (la goroutine détachée est
// annulée). Le serveur émet alors turn_done → le bouton repasse en « send ».
function stopGen(){ jfetch('/api/chat/stop',{method:'POST'}).catch(()=>{}); toast('stop'); }
// Recale l'état du bouton sur la vérité serveur. Ne touche à rien pendant le replay
// initial (l'état final y est posé au caught_up) ni si l'appel échoue : dans le
// doute on garde ce que les événements ont déjà établi.
async function reconcileBusy(){
  try{
    const s=await (await jfetch('/api/chat/state')).json();
    const rt = s.running_task || '';
    if(rt !== RUNNING_TASK){ RUNNING_TASK = rt; if(!REPLAYING) syncSendBtn(); }
    if(typeof s.generating==='boolean' && s.generating!==busy) setBusy(s.generating);
  }catch(_){}
}
// Recalage périodique : une tâche de fond prend le moteur SANS émettre d'événement
// dans le fil (elle est isolée), donc seul un sondage de l'état permet au bouton de
// refléter « moteur occupé par une tâche » en direct.
setInterval(reconcileBusy, 3000);
// Envoi RÉSILIENT : sur le tunnel E2E, un aller-retour peut échouer transitoirement
// alors qu'il a en fait abouti (la génération démarre). On réessaie, et un 409
// (« déjà en cours ») = succès (c'est notre envoi qui est passé). On ne montre une
// erreur qu'après plusieurs échecs ET vérification que rien ne tourne — plus de
// « network error » alarmiste alors que l'IA répond quand même.
async function send(){
  if(busy) return;
  // Garde-fou : le bouton est déjà désactivé, mais l'Entrée passe aussi par ici.
  if(STATUS_SEEN && !MODEL_READY){ toast('le modèle n\'est pas encore prêt'); return; }
  const ta=document.getElementById('input'); const text=ta.value.trim();
  // Un envoi sans texte est légitime s'il porte une pièce jointe (« tiens, regarde »).
  if(!text && !ATTACH.length) return;
  ta.value=''; autoGrow(ta);
  // Le message s'affiche TOUT DE SUITE, en gris : il ne disparaît plus le temps
  // de l'aller-retour. Il s'éclaircit quand le flux le confirme (confirmPending).
  addPending(text);
  const fail=(m)=>{ clearPending(); toast(m); ta.value=text; autoGrow(ta); };
  // C'est ici que les fichiers partent vers le serveur — pas avant. Les pastilles
  // ne sont retirées qu'une fois le message accepté : tant qu'il n'est pas parti,
  // on doit pouvoir en enlever une, et un échec doit rester visible.
  const files=await attachPaths();
  if(!text && !files.length){ fail('aucun fichier n\'a pu être déposé'); return; }
  // Les pastilles passent dans la bulle en attente : le message porte ses
  // fichiers dès l'envoi, sans attendre l'aller-retour.
  if(PENDING) addMsgFiles(PENDING, attachSent());
  for(let attempt=0; attempt<3; attempt++){
    try{
      const r=await jfetch('/api/chat/send',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({message:text,files:files,ctx_used:CTX_USED})});
      if(r.status===409 || r.ok) clearAttach();
      if(r.status===409) return;               // déjà en cours (notre envoi a abouti) → OK
      if(r.ok) return;                          // la bulle + les tokens arrivent par le flux
      if(r.status<500){ let m='erreur'; try{ m=(await r.json()).error||m; }catch(_){} fail(m); return; }
    }catch(e){ /* réseau : on retente */ }
    await new Promise(res=>setTimeout(res, 600));
  }
  // Après plusieurs échecs : le serveur a peut-être quand même reçu le message.
  try{ const s=await (await jfetch('/api/chat/state')).json(); if(s.generating) return; }catch(_){}
  fail('échec de l\'envoi — réessaie');
}
loadAll();
setInterval(loadStatus, 5000);
setInterval(loadVram, 3000);
setInterval(loadRam, 3000);
