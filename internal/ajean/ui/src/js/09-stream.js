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
// Ligne d'état de génération (issue #34, façon Claude Code). Un raisonnement ou
// une exécution d'outil peut durer plusieurs minutes ; sans repère on croit à un
// plantage silencieux. On affiche donc EN PERMANENCE pendant la réponse, en bas
// (#genstatus) le chrono + les tokens produits (la vitesse exacte est figée à la
// fin). Purement client, EN DIRECT seulement : au replay on n'a pas l'instant de
// départ, et un chrono qui « recommencerait » à chaque rechargement tromperait.
let ELAPSED=null; // {start, timer}
// fmtElapsed : durée EN SECONDES → « 42s », « 15 mn 42s », « 1 h 05 mn ». Nom
// distinct de fmtDur() (16-tasks.js, en millisecondes) : les fichiers JS sont
// concaténés dans un même scope, une collision de nom écrasait celui-ci.
function fmtElapsed(secs){
  secs=Math.max(0,Math.round(secs));
  const h=Math.floor(secs/3600), m=Math.floor((secs%3600)/60), s=secs%60;
  if(h) return h+' h '+String(m).padStart(2,'0')+' mn';
  if(m) return m+' mn '+String(s).padStart(2,'0')+'s';
  return s+'s';
}
// Tokens produits ce tour (raisonnement + réponse) : ce qui défile côté modèle.
function genTokCount(){ return (T.reasonTok||0)+(T.contentTok||0); }
// La ligne d'état vit DANS le fil (dernier enfant de #chat) : elle se pose donc
// juste sous le texte de l'IA et suit le défilement, au lieu de flotter en bas
// de l'écran. Créée à la volée, retirée en fin de tour.
let GENEL=null;
function ensureGenEl(){
  const chat=chatEl();
  if(!GENEL){ GENEL=document.createElement('div'); GENEL.className='genstatus';
    // Le J du favicon AJEAN : deux carrés empilés (la barre) + un carré décalé à
    // gauche en bas (le pied du J). Statique, en accent. Classe (pas id) : plusieurs
    // lignes figées coexistent dans le fil, une par tour terminé.
    GENEL.innerHTML='<svg class="jlogo" viewBox="0 0 12 12" aria-hidden="true"><rect x="6" y="3" width="2" height="2"/><rect x="6" y="5" width="2" height="2"/><rect x="4" y="7" width="2" height="2"/></svg><span class="gtxt"></span>'; }
  if(GENEL.parentNode!==chat || chat.lastElementChild!==GENEL) chat.appendChild(GENEL); // toujours en dernier
  return GENEL;
}
function removeGenEl(){ if(GENEL&&GENEL.parentNode) GENEL.parentNode.removeChild(GENEL); GENEL=null; }
// Vitesse decode STABLE en direct, basée sur les HORODATAGES SERVEUR des tokens
// (d.ts), pas l'heure d'arrivée côté client : les ts serveur reflètent le vrai
// rythme de génération et ignorent la bufferisation réseau/MTP (qui rendait la
// mesure erratique). On additionne les écarts serveur entre tokens en EXCLUANT
// les gros trous (attente d'outil / re-prefill) → tokens / temps de decode pur.
const DECODE_GAP_MAX=800; // ms d'écart serveur au-delà = attente, pas du decode
function noteDecode(d){
  if(!ELAPSED) return;
  const ts=d.ts||0; if(!ts) return;
  const ts0=d.ts0||ts; // paquet coalescé (replay) : [ts0..ts] ; direct : ts0==ts
  if(ELAPSED.lastTs){ const gap=ts0-ELAPSED.lastTs; if(gap>0 && gap<DECODE_GAP_MAX) ELAPSED.decodeMs+=gap; }
  if(ts>ts0) ELAPSED.decodeMs+=(ts-ts0); // durée interne du paquet coalescé
  ELAPSED.lastTs=ts;
}
function genRate(){
  if(!ELAPSED || ELAPSED.decodeMs<600) return null; // pas avant ~0,6s de decode (bruit initial)
  // decodeMs ne compte que le temps de decode ACCUMULÉ depuis le (re)démarrage de la
  // ligne. Après un refresh en plein tour, les tokens déjà rejoués (tokBase) sont
  // là mais leur temps de decode, lui, n'a pas été mesuré → il faut diviser par les
  // seuls tokens produits DEPUIS la reprise, sinon la vitesse explose puis se
  // « normalise » à mesure que decodeMs rattrape. tokBase=0 en tour normal.
  const tok=genTokCount()-(ELAPSED.tokBase||0);
  if(tok<=0) return null;
  return tok/(ELAPSED.decodeMs/1000);
}
// EN DIRECT : chrono (temps total du tour) + tokens qui montent + vitesse decode
// stable. La vitesse EXACTE (timings serveur) est figée à la fin par finalizeTurn.
function paintGenStatus(){
  if(!ELAPSED) return;
  if(COMPACTING) return; // pendant un compactage la barre affiche « compactage… »
  const g=ensureGenEl(); const txt=g.querySelector('.gtxt');
  const secs=(Date.now()-ELAPSED.start)/1000;
  const tok=genTokCount();
  const parts=[fmtElapsed(secs)];
  if(tok>0){
    parts.push(tok+' tok');
    const rate=genRate();
    if(rate!=null) parts.push(rate.toFixed(1)+' tok/s');
  }
  txt.textContent=parts.join('  ·  ');
  scrollMaybe();
}
function genStatusOn(on){ chatEl().classList.toggle('genon', !!on); }
function elapsedStart(){
  if(REPLAYING) return;
  elapsedStop();
  ELAPSED={start:Date.now(), timer:setInterval(paintGenStatus,500), decodeMs:0, lastTs:0, tokBase:0};
  genStatusOn(true); paintGenStatus();
}
// Arrêt SANS conserver la ligne (erreur/reset) : le tour n'a pas de fin propre.
function elapsedStop(){ if(ELAPSED){ clearInterval(ELAPSED.timer); ELAPSED=null; } genStatusOn(false); removeGenEl(); }
// FIN DE TOUR (direct ET replay) : pose la ligne définitive sous le message, à
// partir de la durée SERVEUR (elapsed_ms, rejouée donc identique après reload) et
// des mesures serveur (tokens + vitesse decode réelle). On détache GENEL pour que
// le tour suivant en crée une neuve, laissant celle-ci comme trace du tour fini.
function finalizeTurn(elapsedMs){
  const st=T.serverStats||{};
  // Tokens = TOTAL du tour (cumul client sur tous les appels d'outils) — pas
  // st.gen_tokens, qui ne compte que la DERNIÈRE complétion (d'où le chiffre qui
  // baissait à la fin).
  const tok=genTokCount();
  // Vitesse = decode PUR du serveur (timings llama.cpp, exact, hors prefill/outils).
  const rate=st.gen_per_second||null;
  if(ELAPSED){ clearInterval(ELAPSED.timer); ELAPSED=null; }
  genStatusOn(false);
  const parts=[];
  if(elapsedMs>0) parts.push(fmtElapsed(elapsedMs/1000));
  if(tok>0){ parts.push(tok+' tok'); if(rate!=null) parts.push(rate.toFixed(1)+' tok/s'); }
  if(!parts.length){ removeGenEl(); scrollMaybe(); return; }
  const g=ensureGenEl(); g.querySelector('.gtxt').textContent=parts.join('  ·  ');
  GENEL=null;
  scrollMaybe(); // révèle la fin (et cette ligne) même sur un fil à peine défilable
}
function removeTyping(){ if(T.typingEl){ T.typingEl.remove(); T.typingEl=null; } }
// Compactage : on réutilise LA MÊME barre d'état de génération (logo J + texte),
// pas une bannière à part, en y écrivant « compactage… » et en faisant pulser le
// logo. Que le compactage soit proactif (début de tour, sans chrono en cours) ou
// en cours de tour (chrono qui tourne), la barre est la même ; le drapeau
// COMPACTING gèle paintGenStatus pour qu'il ne réécrive pas le chrono par-dessus.
let COMPACTING=false;
function setCompacting(on){
  COMPACTING=on;
  if(on){
    const g=ensureGenEl();
    genStatusOn(true);
    g.classList.add('compacting');
    g.querySelector('.gtxt').textContent='compactage…';
    scrollMaybe();
    return;
  }
  if(GENEL) GENEL.classList.remove('compacting');
  // Hors d'un tour en cours (compactage proactif) : la barre n'a plus lieu d'être.
  // Sinon on la laisse : paintGenStatus reprend le chrono au prochain tick.
  if(!ELAPSED){ genStatusOn(false); removeGenEl(); }
}
// Séparateur laissé dans le fil à l'endroit exact de la coupure.
function addCompactMark(){
  const el=document.createElement('div');
  el.className='compact-mark';
  el.textContent='contexte compacté : anciens tours résumés';
  // Devant la barre d'état encore à l'écran (compactage en cours de tour) : la
  // suite de la réponse doit rester APRÈS la marque de coupure. La barre de
  // génération (GENEL) est le repère ; à défaut l'ancien indicateur de frappe.
  const anchor = (GENEL && GENEL.parentNode===chatEl()) ? GENEL
    : (T.typingEl && T.typingEl.parentNode===chatEl()) ? T.typingEl : null;
  if(anchor) chatEl().insertBefore(el, anchor);
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
// Label d'une bulle de raisonnement : rôle + nombre de tokens. PAS de vitesse
// (tok/s) : elle est déjà affichée en bas dans la ligne d'état de génération, la
// répéter sur chaque bulle de raisonnement n'apporte rien (firstTs/lastTs restent
// dans la signature pour les appelants, désormais inutilisés).
function labelTokens(el, role, n, firstTs, lastTs){
  if(!el) return;
  setLabel(el, role+'  ·  '+n+' tok');
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
  if(d.reset!==undefined){
    // reset de RESTAURATION (d.replay) : un fil complet suit, on repasse en mode
    // REPLAY pour que les bulles de raisonnement/outil arrivent DÉJÀ repliées
    // (comme au chargement de page), sans l'animation « ouvre puis se ferme ». Le
    // caught_up qui clôt le rejeu remettra REPLAYING à false.
    if(d.replay) REPLAYING=true;
    elapsedStop(); smoothReset(); if(renderTimer){ clearTimeout(renderTimer); renderTimer=null; } renderPending=null; PENDING=null; document.getElementById('chat').innerHTML=''; newTurn(); setCtxUsed(0); lastSeq=0; setBusy(false); return; }
  if(d.user!==undefined){
    newTurn();
    let el=PENDING;
    if(!confirmPending(d.user)) el=addMsg('user', d.user);
    // Pièces jointes du tour : rendues DANS la bulle. La bulle en attente en
    // porte déjà (posées à l'envoi), on ne les ajoute donc qu'au replay/à une
    // bulle neuve — sinon elles apparaîtraient en double.
    if(d.files && !hasMsgFiles(el)) addMsgFiles(el, d.files);
    setBusy(true); T.typingEl=addTyping(); elapsedStart(); return; }
  if(d.turn_done){ smoothSnap(); flushRender();
    // MÊME ligne en direct et au replay : durée serveur (elapsed_ms, rejouée) +
    // mesures serveur. removeTyping AVANT finalize pour que la ligne soit bien le
    // dernier enfant du fil (donc sous le message).
    removeTyping(); finalizeTurn(d.elapsed_ms||0); collapseAll(T.turnCollapsibles); setBusy(false); return; }
  if(d.error){ smoothSnap(); flushRender(); elapsedStop(); removeTyping(); T.contentEl=null; T.reasonEl=null; const eb=addMsg('assistant',''); eb.classList.add('errmsg'); renderBody(eb, d.error); return; }
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
    // Les mesures définitives sont posées par finalizeTurn (à turn_done), même
    // ligne en direct et au replay — on se contente ici de mémoriser les stats.
    return; }
  if(d.tool_used){
    smoothSnap(); flushRender(); // le bloc texte précédent (raisonnement/contenu) est terminé
    killTyping('tool'); T.contentEl=null; T.reasonEl=null; const tu=d.tool_used;
    if(!T.pendingToolEl){ collapseAll(T.turnCollapsibles); T.pendingToolEl=addMsg('tool',''); if(REPLAYING||viewOn('fold-tools')) collapseInstant(T.pendingToolEl); T.turnCollapsibles.push(T.pendingToolEl); }
    renderToolMsg(T.pendingToolEl, tu);
    // Outils masqués : on garde l'indicateur même quand l'appel est terminé (le
    // tour continue, et rien d'autre n'est visible). Sinon, comportement inchangé.
    if(!tu.done || viewOn('hide-tools')) showTyping('tool');
    if(tu.done){
      T.pendingToolEl=null;
      if(tu.name==='mem_add'||tu.name==='mem_edit'||tu.name==='mem_delete') loadMem();
      // L'IA vient de créer/modifier/supprimer/activer une tâche : la liste, la
      // pastille « N actives » et l'état suspendu doivent se remettre à jour tout
      // de suite, sans attendre un refresh manuel de la page.
      if(tu.name==='task_create'||tu.name==='task_update'||tu.name==='task_delete') loadTasks();
    }
    if(ELAPSED) ensureGenEl(); // re-ancre la ligne EN DERNIER dans le même cycle : la bulle d'outil ne passe pas au-dessus (pas de saut)
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
    noteDecode(d); paintGenStatus();
    return; }
  if(d.content){
    removeTyping();
    if(!T.contentEl){ collapseAll(T.turnCollapsibles); T.contentEl=addMsg('assistant',''); T.fullContent=''; }
    if(d.replace){ smoothSnap(); T.fullContent=''; T.contentTok=0; T.contentFirstTs=0; }
    T.fullContent+=d.content; feedBlock(T.contentEl, T.fullContent);
    if(!T.contentFirstTs) T.contentFirstTs=d.ts0||d.ts||0; T.contentLastTs=d.ts||T.contentLastTs; T.contentTok+=(d.toks||1);
    labelTokens(T.contentEl, 'assistant', T.contentTok, T.contentFirstTs, T.contentLastTs);
    noteDecode(d); paintGenStatus();
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
    // Reprise d'une génération DÉJÀ en cours (page actualisée en plein tour) : au
    // replay, l'événement `user` qui démarre la ligne d'état a été rejoué alors que
    // REPLAYING gelait elapsedStart — la ligne (chrono · tokens · tok/s) restait donc
    // absente jusqu'à la fin du tour. On la (re)pose ici, une seule fois (garde
    // !ELAPSED, sinon le tick des 3 s la remettrait à zéro). Le serveur nous donne la
    // durée DÉJÀ écoulée du tour (gen_elapsed_ms) : on cale le départ dessus pour que
    // le chrono reprenne à la BONNE valeur, pas à zéro. Pas pour une tâche de fond
    // (RUNNING_TASK) : elle n'a pas de bulle dans le fil sous laquelle s'afficher.
    if(s.generating && !REPLAYING && !RUNNING_TASK && !ELAPSED){
      elapsedStart();
      if(ELAPSED){
        const e=+s.gen_elapsed_ms||0;
        if(e>0) ELAPSED.start=Date.now()-e; // chrono à la vraie valeur, pas zéro
        // Les tokens déjà rejoués ne comptent pas dans la vitesse LIVE (leur temps
        // de decode n'a pas été mesuré) : on mesure à partir d'ici.
        ELAPSED.tokBase=genTokCount();
      }
    }
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
// Une seule fois au démarrage (interroge GitHub côté serveur) : prévient en accès
// distant si le serveur AJEAN de la machine est plus ancien que le front hébergé.
checkServerFreshness();
setInterval(loadStatus, 5000);
setInterval(loadVram, 3000);
setInterval(loadRam, 3000);
