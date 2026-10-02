let stickyBottom = true;
// CHAT_TARGET : conteneur hors page où rendre un lot d'échanges anciens (voir
// loadOlder) ; null = le fil lui-même.
let CHAT_TARGET=null;
const chatEl = () => CHAT_TARGET || document.getElementById('chat');
function isNearBottom(){
  const c = chatEl();
  return c.scrollHeight - c.scrollTop - c.clientHeight < 60;
}
// La zone de chat réserve une gouttière de barre de défilement de chaque côté
// (scrollbar-gutter: stable both-edges) ; le composer, lui, n'est pas défilant.
// On mesure la gouttière réelle et on la reporte sur le composer, sinon les
// messages sont en retrait par rapport à la zone de saisie — décalage visible
// surtout quand la barre latérale est escamotée.
function syncGutter(){
  const chat=document.getElementById('chat'); if(!chat) return;
  const g=Math.max(0,(chat.offsetWidth-chat.clientWidth)/2);
  document.documentElement.style.setProperty('--sbw', g+'px');
}
addEventListener('resize', syncGutter);
addEventListener('DOMContentLoaded', syncGutter);
// Le composeur flotte au-dessus du fil et sa hauteur VARIE (saisie multi-lignes,
// pièces jointes en attente). On la mesure et on la reporte en variable CSS pour
// que le padding bas du fil dégage toujours exactement la carte — sinon, sur une
// conversation courte pas encore défilable, le texte se glissait sous le composeur
// sans qu'on puisse le faire remonter. On re-scrolle après coup si on était collé
// en bas (le padding qui change déplace le bas).
// Mesure la hauteur RÉELLE du composeur → --composer-h. Le composeur est en
// superposition (absolu) : #chat réserve cette hauteur (+ marge) en padding-bas
// pour que le fil défile derrière la carte sans que la fin se cache dessous.
function syncComposerPad(){
  const comp=document.getElementById('composer'); if(!comp) return;
  document.documentElement.style.setProperty('--composer-h', comp.offsetHeight+'px');
  scrollMaybe();
}
// Initialise la mesure de la hauteur du composeur de façon ROBUSTE sur iOS Safari.
// Piège : au retour via le cache page (bfcache) ou selon le timing, `DOMContentLoaded`
// peut ne PAS se redéclencher → sans ça `--composer-h` restait à sa valeur par
// défaut (150px), souvent plus petite que le composeur réel (safe-area, nom du
// preset sur 2 lignes…), donc la fin de la réponse se cachait sous la carte de
// saisie. On (re)mesure sur tous les points d'entrée + quelques filets différés
// (polices/statut chargés tard), et l'observateur est posé une seule fois.
function initComposerPad(){
  syncComposerPad();
  const comp=document.getElementById('composer');
  if(comp && window.ResizeObserver && !comp._roPad){ comp._roPad=new ResizeObserver(syncComposerPad); comp._roPad.observe(comp); }
  setTimeout(syncComposerPad, 300);
  setTimeout(syncComposerPad, 1200);
}
addEventListener('resize', syncComposerPad);
addEventListener('orientationchange', syncComposerPad);
addEventListener('DOMContentLoaded', initComposerPad);
addEventListener('load', initComposerPad);
addEventListener('pageshow', initComposerPad); // iOS : rechargement depuis le bfcache

// scrollMaybe est appelé À CHAQUE token (paintGenStatus) ET à chaque rendu Markdown
// (renderBody) : en streaming, des dizaines de fois par seconde. Écrire scrollTop
// aussi souvent avait deux effets pénibles sur iPhone (PWA) : un layout synchrone
// forcé (lecture de scrollHeight) qui jankait le thread principal, et surtout un
// défilement programmatique quasi permanent du #chat — pendant lequel iOS AVALE les
// taps (il croit qu'un geste de défilement est en cours), d'où « le menu et stop ne
// répondent pas toujours ». On COALESCE donc : au plus une écriture de scroll par
// frame (rAF), et on n'écrit RIEN quand on est déjà en bas (write redondant =
// défilement inutile qui vole quand même les taps).
let _scrollRAF = 0, _lastScrollWrite = 0;
// Écart MINIMAL entre deux écritures d'auto-scroll pendant le streaming. Le fil
// grandit token par token : sans ce frein, scrollTop était réécrit à CHAQUE frame
// (~60/s), un défilement programmatique continu pendant lequel iOS (PWA) AVALE les
// taps — « je ne peux plus rien cliquer tant que l'IA répond ». En espaçant les
// écritures, on laisse des fenêtres d'inactivité (~150 ms) où le tap est enregistré,
// tout en suivant le bas d'assez près pour que ça reste fluide. `force` (jumpBottom,
// fin de tour, caught_up) court-circuite le frein pour un recalage immédiat.
const SCROLL_MIN_GAP = 150;
// Doigt posé (ou tout juste levé) : AUCUNE écriture d'auto-scroll. Sur iOS, un tap
// dure 100-200 ms ; si la position de défilement change pendant ce temps, Safari
// l'annule. Avec une écriture toutes les 150 ms pendant la génération, presque
// chaque tap tombait dessus : plus rien ne répondait (pas même stop) jusqu'à la
// fin de la réponse. On suspend donc le suivi le temps du contact, et on rattrape
// juste après (TOUCH_GRACE).
let TOUCHING = false, _touchEnd = 0;
const TOUCH_GRACE = 350;
(function(){
  const on = ()=>{ TOUCHING = true; };
  const off = ()=>{ TOUCHING = false; _touchEnd = performance.now(); if(stickyBottom) setTimeout(()=>scrollMaybe(), TOUCH_GRACE + 20); };
  document.addEventListener('touchstart', on, {passive:true, capture:true});
  document.addEventListener('touchend', off, {passive:true, capture:true});
  document.addEventListener('touchcancel', off, {passive:true, capture:true});
})();
function scrollMaybe(force){
  // Pendant le replay initial on NE force AUCUN reflow : lire scrollHeight à chaque
  // événement rejoué = un layout synchrone forcé sur un DOM qui grossit → coût
  // quadratique (20-30 s de rendu au refresh sur un long fil). Le scroll est fait
  // une seule fois à la fin du replay, via jumpBottom() au signal {caught_up}.
  if(REPLAYING && !force) return;
  if(_scrollRAF) return;                 // déjà planifié pour cette frame
  _scrollRAF = requestAnimationFrame(()=>{
    _scrollRAF = 0;
    const c = chatEl(); if(!c) return;
    if(stickyBottom){
      const now = (window.performance&&performance.now)?performance.now():Date.now();
      // Frein temporel : hors recalage forcé, au plus une écriture toutes ~150 ms.
      // C'est ce qui rend les taps de nouveau captés pendant la génération (voir plus haut).
      // Contact en cours : on n'écrit pas (voir TOUCHING) ; le touchend relance.
      const touchBusy = TOUCHING || now - _touchEnd < TOUCH_GRACE;
      if(!touchBusy && (force || now - _lastScrollWrite >= SCROLL_MIN_GAP)){
        const target = c.scrollHeight - c.clientHeight;
        // Seuil : n'écris que si on n'y est pas déjà (à 1px près). Sinon on relance
        // la machinerie de scroll d'iOS pour rien, et les taps continuent d'être volés.
        if(Math.abs(c.scrollTop - target) > 1){ c.scrollTop = target; _lastScrollWrite = now; }
      }
    }
    const sb = document.getElementById('scrollbtn');
    if(sb) sb.classList.toggle('show', !stickyBottom);
  });
}
function jumpBottom(){ stickyBottom = true; scrollMaybe(true); }
document.addEventListener('DOMContentLoaded', ()=>{
  const c = chatEl();
  c.addEventListener('scroll', ()=>{
    stickyBottom = isNearBottom();
    maybeLoadOlder(); // remontée du fil : lot d'échanges précédent près du haut
    document.getElementById('scrollbtn').classList.toggle('show', !stickyBottom);
  });
});

// Jeu d'icônes SVG au trait (style Lucide, 24×24, stroke currentColor). Pas d'emoji :
// rendu net, monochrome, qui suit la couleur du thème (clair/sombre) et le dim des
// étiquettes. Le contenu est développeur (statique) → innerHTML sûr.
const ICONS = {
  brain:'<path d="M12 5a3 3 0 1 0-5.997.142 4 4 0 0 0-2.526 5.77 4 4 0 0 0 .556 6.588A4 4 0 1 0 12 18Z"/><path d="M12 5a3 3 0 1 1 5.997.142 4 4 0 0 1 2.526 5.77 4 4 0 0 1-.556 6.588A4 4 0 1 1 12 18Z"/><path d="M15 13a4.5 4.5 0 0 1-3-4 4.5 4.5 0 0 1-3 4"/>',
  terminal:'<polyline points="4 17 10 11 4 5"/><line x1="12" y1="19" x2="20" y2="19"/>',
  file:'<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/>',
  edit:'<path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"/>',
  search:'<circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>',
  globe:'<circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>',
  db:'<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>',
  clock:'<circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/>',
  calendar:'<rect x="3" y="4" width="18" height="18" rx="2"/><line x1="3" y1="9" x2="21" y2="9"/><line x1="8" y1="2" x2="8" y2="6"/><line x1="16" y1="2" x2="16" y2="6"/>',
  trend:'<path d="M4 19V5"/><path d="M4 19h16"/><polyline points="7 15 11 11 14 13 19 7"/>',
  image:'<rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/>',
  monitor:'<rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/>',
  plug:'<path d="M9 2v6"/><path d="M15 2v6"/><path d="M6 8h12v3a6 6 0 0 1-6 6 6 6 0 0 1-6-6z"/><path d="M12 17v5"/>',
  wrench:'<path d="M14.7 6.3a4 4 0 0 0-5.4 5.4L3 18v3h3l6.3-6.3a4 4 0 0 0 5.4-5.4l-2.8 2.8-2-2 2.8-2.8z"/>',
};
function iconSvg(key){
  const p = ICONS[key] || ICONS.wrench;
  return '<svg class="ic" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">'+p+'</svg>';
}
// Icône du rôle d'une bulle à sa création, avant que le flux ne pose le vrai libellé.
function roleIcon(role){ return role==='reasoning' ? 'brain' : role==='tool' ? 'wrench' : ''; }
function roleLabel(role){
  if(role==='reasoning') return t('chat.reasoning_in_progress');
  if(role==='tool') return t('chat.tool_ellipsis');
  return role;
}
// Pose (ou retire) l'icône d'une étiquette sans toucher au texte.
function setIcon(el, key){ const s=el&&el.querySelector('.label .ic-slot'); if(s) s.innerHTML = key ? iconSvg(key) : ''; }
function addMsg(role, text){
  const el=document.createElement('div');
  el.className='msg '+role;
  const collapsible = (role==='reasoning' || role==='tool');
  const labelHTML='<span class="label"><span class="ic-slot"></span><span class="txt"></span></span>';
  // .body must be a real block so <p>/<pre>/<ul> margins behave properly.
  if(collapsible){
    // 'working' = bulle active : son étiquette (la ligne-résumé, qui survit au repli)
    // reçoit le shimmer « voile blanc » façon Claude/DeepSeek tant que l'IA y travaille.
    // Piloté par le flux (retiré à tu.done pour un outil, au passage reasoning→suite),
    // PAS par le repli. Pas de shimmer pour les bulles historiques rejouées.
    el.classList.add('collapsible');
    if(!(typeof REPLAYING!=='undefined' && REPLAYING)) el.classList.add('working');
    el.innerHTML=labelHTML+'<div class="bodywrap"><div class="body"></div></div>';
    el.querySelector('.label').onclick=()=>toggleCollapse(el);
  } else {
    el.innerHTML=labelHTML+'<div class="body"></div>';
  }
  setIcon(el, roleIcon(role));
  setLabel(el, roleLabel(role));
  el.querySelector('.body').textContent=text;
  chatEl().appendChild(el);
  scrollMaybe();
  return el;
}
// Bulle « … » animée affichée dès l'envoi, retirée au 1er token/outil/erreur.
function addTyping(){
  const el=document.createElement('div');
  el.className='msg assistant typing';
  el.innerHTML='<span class="dot"></span>';
  chatEl().appendChild(el); scrollMaybe();
  return el;
}
// Replie/déplie en douceur les bulles reasoning/tool. Hauteur animée en JS :
// on fige scrollHeight puis on va à 0 (fermeture) ou de 0 vers scrollHeight
// (ouverture), sans jamais dépasser. overflow:hidden clippe pendant l'animation.
function collapseBody(el){
  const bw=el.querySelector('.bodywrap'); if(!bw || el.classList.contains('collapsed')) return;
  // NB : on ne touche PAS à 'working' ici. Le repli est indépendant de l'activité —
  // une bulle repliée peut être encore en cours (fold-tools la replie dès sa
  // création). Le shimmer est piloté par le flux (création → done), pas par le repli.
  bw.style.height = bw.scrollHeight+'px';   // fige les dimensions courantes
  bw.style.width  = bw.scrollWidth+'px';
  void bw.offsetHeight;                      // reflow pour que la transition parte de là
  el.classList.add('collapsed');
  bw.style.height = '0px';                    // → anime height ET width vers 0
  bw.style.width  = '0px';
}
function expandBody(el){
  const bw=el.querySelector('.bodywrap'); if(!bw) return;
  el.classList.remove('collapsed');
  bw.style.height=''; bw.style.width='';      // mesure les dimensions naturelles…
  const h=bw.scrollHeight, w=bw.scrollWidth;
  bw.style.height='0px'; bw.style.width='0px';// …repart de 0 (pas de flash, même frame)
  void bw.offsetHeight;
  bw.style.height=h+'px'; bw.style.width=w+'px';
  const done=e=>{ if(e.propertyName!=='height') return; bw.style.height=''; bw.style.width=''; bw.removeEventListener('transitionend',done); };
  bw.addEventListener('transitionend',done);
}
function toggleCollapse(el){ el.classList.contains('collapsed') ? expandBody(el) : collapseBody(el); }
// Replie toutes les bulles d'un tour une fois la réponse finale entamée.
function collapseAll(list){ for(const el of list){ if(el) collapseBody(el); } list.length=0; }
// Replie une bulle INSTANTANÉMENT (sans animation) — utilisé pendant le replay au
// chargement pour que les vieilles bulles apparaissent déjà fermées. La classe
// 'collapsed' seule ne gère que l'opacité ; la hauteur est en style inline, donc
// on la met à 0 transition désactivée.
function collapseInstant(el){
  const bw=el.querySelector('.bodywrap'); if(!bw) return;
  el.classList.add('collapsed');
  // Pas de `void bw.offsetHeight` ici : la bulle vient d'être créée et n'a jamais
  // été peinte dépliée, donc poser height:0 n'anime pas — inutile de forcer un
  // reflow par bulle (ce qui, multiplié par le replay, coûtait très cher).
  bw.style.transition='none';
  bw.style.height='0px'; bw.style.width='0px';
  requestAnimationFrame(()=>{ bw.style.transition=''; });
}
// Écrit le texte de l'étiquette dans son slot .txt (préserve l'icône .ic-slot).
// Repli sur l'ancien comportement (textContent entier) pour une étiquette non
// structurée, au cas où.
function setLabel(el, text){
  const lab=el.querySelector('.label'); if(!lab) return;
  const txt=lab.querySelector('.txt');
  if(txt) txt.textContent=text; else lab.textContent=text;
}
// Ajoute « +N -N » colorés à l'étiquette d'une bulle. L'étiquette reste visible
// une fois la bulle repliée : c'est le seul endroit où le volume d'une écriture
// survit au repli, donc on le met là plutôt que dans le corps seul.
function setLabelCounts(el, add, del){
  const lab=el.querySelector('.label');
  // Idempotent : renderToolMsg est rappelé à CHAQUE événement de flux pour la même
  // bulle. Sans purge, chaque passage empilait un badge (« +1 +2 +1 » observé) au
  // lieu de refléter l'état courant. On retire donc l'ancien compteur d'abord.
  lab.querySelectorAll('.diff-count').forEach(n=>n.remove());
  const cnt=document.createElement('span'); cnt.className='diff-count';
  if(add) cnt.appendChild(Object.assign(document.createElement('span'),{className:'a',textContent:'+'+add}));
  if(add && del) cnt.appendChild(document.createTextNode(' '));
  if(del) cnt.appendChild(Object.assign(document.createElement('span'),{className:'d',textContent:'-'+del}));
  lab.appendChild(cnt);
}
// Ligne de mesures sous une réponse (prefill / decode). Les étiquettes VOUS/AJEAN
// sont masquées dans cette mise en page, donc les chiffres qu'on y écrivait
// avaient disparu : ils ont leur propre ligne, discrète, sous le texte. Toujours
// affichée (plus de réglage pour la cacher).
function setStats(el, text){
  if(!el) return;
  let s = el.querySelector(':scope > .statline');
  if(!s){
    s=document.createElement('div'); s.className='statline';
    // Apparition en fondu, à la PREMIÈRE pose seulement : la ligne arrive une fois
    // la réponse finie, un surgissement sec accrochait l'œil. Les mises à jour
    // suivantes ne rejouent pas l'animation (elle clignoterait), et le rejeu du
    // journal au chargement n'anime rien du tout.
    if(!(typeof REPLAYING!=='undefined' && REPLAYING)) s.classList.add('statline-in');
    el.appendChild(s);
  }
  s.textContent = text;
}
function bodyOf(el){ return el.querySelector('.body'); }
// Render markdown into a message body in place; safe because md() escapes HTML.
function renderBody(el, text, tail){ const b=bodyOf(el); b.innerHTML = md(encodeMdLinkSpaces(text)); markNotices(b); addCopyButtons(b); markFileLinks(b); markWorkspaceImages(b); if(tail) fadeTail(b, FADE_TAIL); scrollMaybe(); }
// Apparition en fondu du texte de l'IA pendant qu'il s'écrit : les derniers
// caractères révélés reçoivent une opacité croissante (0,2 au bout → 1). Comme ce
// dégradé avance avec le texte, chaque mot semble apparaître en fondu, sans
// animation CSS (le bloc est redessiné à chaque image : une animation se
// relancerait et clignoterait). Le rendu final, lui, est posé sans dégradé.
const FADE_TAIL = 16;
function fadeTail(root, n){
  const w = document.createTreeWalker(root, NodeFilter.SHOW_TEXT, {acceptNode:(t)=>t.parentElement.closest('button') ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT});
  const nodes = []; while(w.nextNode()) nodes.push(w.currentNode);
  let k = 0; // caractères déjà traités depuis la fin
  for(let i = nodes.length - 1; i >= 0 && k < n; i--){
    const tn = nodes[i], chars = [...tn.data];
    if(!k && !tn.data.trim()) continue;
    const take = Math.min(n - k, chars.length);
    const frag = document.createDocumentFragment();
    if(chars.length > take) frag.appendChild(document.createTextNode(chars.slice(0, chars.length - take).join('')));
    chars.slice(chars.length - take).forEach((ch, j)=>{
      const fromEnd = k + (take - 1 - j);
      const sp = document.createElement('span');
      sp.style.opacity = (0.2 + 0.8 * fromEnd / n).toFixed(2);
      sp.textContent = ch; frag.appendChild(sp);
    });
    tn.replaceWith(frag); k += take;
  }
}
// Lignes d'un contenu en cours d'écriture, comptées comme côté serveur : un saut
// de ligne final termine la dernière ligne, il n'en ouvre pas une vide.
function bodyLineCount(s){ s=String(s).replace(/\r\n/g,'\n').replace(/\n$/,''); return s ? s.split('\n').length : 0; }
// Render a tool call as its own conversation message: the command the model
// wrote, then the response it got back. textContent keeps it injection-safe.
function renderToolMsg(el, tu){
  // Métadonnées d'affichage par outil : nom court + en-tête. Les outils web
  // (web_search/open/read/grep) ont leur propre libellé, pas le fallback mémoire.
  // `ico` = clé d'icône SVG (voir ICONS) affichée devant l'étiquette pour repérer
  // d'un coup d'œil le type d'action.
  const META = {
    bash:       {ico:'terminal', lbl:t('chat.tool.bash_lbl'),            head:t('chat.tool.bash_head')},
    write:      {ico:'file',     lbl:t('chat.tool.write_lbl'),           head:t('chat.tool.write_head')},
    edit:       {ico:'edit',     lbl:t('chat.tool.edit_lbl'),            head:t('chat.tool.edit_head')},
    web_search: {ico:'search',   lbl:t('chat.tool.web_search_lbl'),      head:t('chat.tool.web_search_head')},
    web_open:   {ico:'globe',    lbl:t('chat.tool.web_open_lbl'),        head:t('chat.tool.web_open_head')},
    web_read:   {ico:'globe',    lbl:t('chat.tool.web_read_lbl'),        head:t('chat.tool.web_read_head')},
    web_grep:   {ico:'globe',    lbl:t('chat.tool.web_grep_lbl'),        head:t('chat.tool.web_grep_head')},
    mem_search: {ico:'db',       lbl:t('chat.tool.mem_search_lbl'),      head:t('chat.tool.mem_search_head')},
    mem_read:   {ico:'db',       lbl:t('chat.tool.mem_read_lbl'),        head:t('chat.tool.mem_read_head')},
    mem_add:    {ico:'db',       lbl:t('chat.tool.mem_add_lbl'),         head:t('chat.tool.mem_add_head')},
    mem_edit:   {ico:'db',       lbl:t('chat.tool.mem_edit_lbl'),        head:t('chat.tool.mem_edit_head')},
    mem_delete: {ico:'db',       lbl:t('chat.tool.mem_delete_lbl'),      head:t('chat.tool.mem_delete_head')},
    recall:       {ico:'db',     lbl:t('chat.tool.recall_lbl'),          head:t('chat.tool.recall_head')},
    recall_search:{ico:'db',     lbl:t('chat.tool.recall_search_lbl'),   head:t('chat.tool.recall_search_head')},
    task_list:  {ico:'clock',    lbl:t('chat.tool.task_list_lbl'),       head:t('chat.tool.task_list_head')},
    task_create:{ico:'clock',    lbl:t('chat.tool.task_create_lbl'),     head:t('chat.tool.task_create_head')},
    task_update:{ico:'clock',    lbl:t('chat.tool.task_update_lbl'),     head:t('chat.tool.task_update_head')},
    task_delete:{ico:'clock',    lbl:t('chat.tool.task_delete_lbl'),     head:t('chat.tool.task_delete_head')},
    see_image:  {ico:'image',    lbl:t('chat.tool.see_image_lbl'),       head:t('chat.tool.see_image_head')},
    browser_open:      {ico:'globe',  lbl:t('chat.tool.browser_open_lbl'),         head:t('chat.tool.cu_head')},
    browser_snapshot:  {ico:'globe',  lbl:t('chat.tool.browser_snapshot_lbl'),     head:t('chat.tool.cu_head')},
    browser_find:      {ico:'search', lbl:t('chat.tool.browser_find_lbl'),         head:t('chat.tool.cu_head')},
    browser_click:     {ico:'globe',  lbl:t('chat.tool.browser_click_lbl'),        head:t('chat.tool.cu_head')},
    browser_click_xy:  {ico:'globe',  lbl:t('chat.tool.browser_click_xy_lbl'),     head:t('chat.tool.cu_head')},
    browser_type:      {ico:'edit',   lbl:t('chat.tool.browser_type_lbl'),         head:t('chat.tool.cu_head')},
    browser_key:       {ico:'globe',  lbl:t('chat.tool.browser_key_lbl'),          head:t('chat.tool.cu_head')},
    browser_scroll:    {ico:'globe',  lbl:t('chat.tool.browser_scroll_lbl'),       head:t('chat.tool.cu_head')},
    browser_screenshot:{ico:'image',  lbl:t('chat.tool.browser_screenshot_lbl'),   head:t('chat.tool.cu_head')},
    machines_list:{ico:'monitor', lbl:t('chat.tool.machines_list_lbl'),  head:t('chat.tool.machines_list_head')},
    machines_use: {ico:'monitor', lbl:t('chat.tool.machines_use_lbl'),   head:t('chat.tool.machines_use_head')},
    tracker:    {ico:'trend',     lbl:t('chat.tool.tracker_lbl'),        head:t('chat.tool.tracker_head')},
  };
  // Outils MCP (nom mcp__<serveur>__<outil>) : en-tête = nom du serveur, libellé lisible,
  // pas le fallback générique. On extrait serveur et outil du nom namespacé.
  let meta = META[tu.name];
  if(!meta && tu.name && tu.name.indexOf('mcp__')===0){
    const parts = tu.name.slice(5).split('__');
    const server = parts.shift() || 'mcp';
    const tool = parts.join('__') || tu.name;
    meta = {ico:'plug', lbl: tool, head: server};
  }
  meta = meta || {ico:'wrench', lbl:t('chat.tool.fallback_lbl'), head:t('chat.tool.fallback_head')};
  setIcon(el, meta.ico);
  let lbl = meta.lbl;
  // Indication du volume de la réponse de l'outil (~tokens, estimation 1 tok ≈ 4 car).
  // On utilise la taille RÉELLE (result_chars) quand le flux n'a envoyé qu'un aperçu,
  // sinon on retombe sur la longueur du résultat présent.
  const realChars = tu.result_chars || (tu.result ? tu.result.length : 0);
  if(realChars){ lbl += '  ·  ' + Math.max(1, Math.round(realChars/4)) + ' ' + t('chat.tok_unit'); }
  setLabel(el, lbl);
  // Volume de l'écriture (final si le diff est là, provisoire pendant la frappe)
  // reporté sur l'étiquette, pour rester lisible bulle repliée.
  // Les vrais totaux viennent du serveur (added/removed) : le diff lui-même est
  // tronqué pour l'affichage, le recompter donnait « +120 » pour 500 lignes.
  // Repli sur le décompte du diff pour les conversations d'avant ces champs.
  let add=0, del=0;
  if(tu.added!=null || tu.removed!=null){ add=tu.added||0; del=tu.removed||0; }
  else if(tu.diff && tu.diff.length){ tu.diff.forEach(l=>{ if(l.op==='+') add++; else if(l.op==='-') del++; }); }
  else if(tu.body){ add=bodyLineCount(tu.body); }
  if(add||del) setLabelCounts(el, add, del);
  const body=bodyOf(el); body.innerHTML='';
  // Plus d'en-tête « commande / recherche web » ici : il répétait le label (icône +
  // nom) juste au-dessus. La carte va droit à la commande puis au résultat.
  if(tu.label){
    const pre=document.createElement('pre'); pre.className='tool-cmd';
    const code=document.createElement('code'); code.textContent=tu.label;
    if(tu.typing){ const car=document.createElement('span'); car.className='tool-caret'; car.textContent='▋'; code.appendChild(car); }
    pre.appendChild(code); body.appendChild(pre);
  }
  // Écriture EN COURS : le modèle tape encore le contenu. On l'affiche ligne à
  // ligne, dans la même forme que le diff final, pour que la bulle se remplisse
  // sous les yeux au lieu de rester vide puis de s'ouvrir d'un coup. Seule la
  // dernière ligne est « fraîche » (fondu) : réanimer tout à chaque événement
  // ferait clignoter le bloc entier.
  if(tu.body && !(tu.diff && tu.diff.length)){
    const lines=tu.body.split('\n');
    const sub=document.createElement('div'); sub.className='tool-sub';
    sub.textContent=t('chat.writing_in_progress'); // le +N vit sur l'étiquette (visible repliée)
    body.appendChild(sub);
    const pre=document.createElement('pre'); pre.className='diff live';
    lines.forEach((t,i)=>{
      const ln=document.createElement('span');
      ln.className='dl add'+(i===lines.length-1?' fresh':'');
      // Marqueur +/- dans une gouttière séparée du texte : sinon un contenu qui
      // commence lui-même par « - » (puce Markdown) donnait un « + - » collé et
      // trompeur. Ici le « + » vit dans sa colonne, le texte reste intact à côté.
      ln.appendChild(Object.assign(document.createElement('span'),{className:'op',textContent:'+'}));
      ln.appendChild(Object.assign(document.createElement('span'),{className:'tx',textContent:t}));
      if(i===lines.length-1 && tu.typing){
        const car=document.createElement('span'); car.className='tool-caret'; car.textContent='▋';
        ln.appendChild(car);
      }
      pre.appendChild(ln);
    });
    body.appendChild(pre);
    // Le bloc est re-créé à chaque événement : on le recale en bas pour suivre
    // la ligne en cours (max-height côté CSS l'empêche de pousser le fil).
    pre.scrollTop = pre.scrollHeight;
  }
  // Diff d'une écriture (fichier ou page de mémoire) : lignes ajoutées en vert,
  // retirées en rouge, contexte en gris — comme un diff de terminal.
  if(tu.diff && tu.diff.length){
    const sub=document.createElement('div'); sub.className='tool-sub';
    sub.textContent=t('chat.modifications'); // le +N -N vit sur l'étiquette (visible repliée)
    body.appendChild(sub);
    const pre=document.createElement('pre'); pre.className='diff';
    tu.diff.forEach(l=>{
      const ln=document.createElement('span');
      ln.className='dl'+(l.op==='+'?' add':l.op==='-'?' del':'');
      // Marqueur dans sa propre gouttière (voir bloc « écriture en cours ») : évite
      // le « + - » collé quand la ligne ajoutée est elle-même une puce Markdown.
      ln.appendChild(Object.assign(document.createElement('span'),{className:'op',textContent:(l.op==='+'?'+':l.op==='-'?'−':'')}));
      ln.appendChild(Object.assign(document.createElement('span'),{className:'tx',textContent:l.text}));
      pre.appendChild(ln);
    });
    body.appendChild(pre);
  }
  const hasResult = tu.result!==undefined && tu.result!=='';
  if(hasResult){
    const sub=document.createElement('div'); sub.className='tool-sub'; sub.textContent=t('chat.response');
    body.appendChild(sub);
    const pre=document.createElement('pre');
    const code=document.createElement('code');
    const preview=tu.result, rid=tu.result_id, PREVIEW=1600;
    // Deux cas :
    //  - result_id présent : le flux n'a envoyé qu'un aperçu (résultat coupé),
    //    « voir plus » CHARGE le reste à la demande via l'API.
    //  - sinon : le résultat est déjà complet ; s'il est long, « voir plus » le
    //    déplie localement (contenu déjà là).
    if(rid){
      code.textContent=preview+'…';
      pre.appendChild(code);
      const more=document.createElement('button');
      more.className='tool-more'; more.type='button'; more.textContent=t('chat.show_more');
      let full=null, open=false;
      more.onclick=async(e)=>{ e.stopPropagation();
        if(full===null){
          // Un échec n'est PAS mémorisé : il laissait « voir plus » inerte pour
          // toujours (le repli sur l'aperçu ne dépliait rien). On prévient et on
          // laisse réessayer.
          more.disabled=true; const prevTxt=more.textContent; more.textContent='…';
          let got=null;
          try{ const sid=(typeof READING!=='undefined' && READING) ? READING_ID : (typeof CONV_ID!=='undefined' ? CONV_ID : '');
          const r=await jfetch('/api/chat/tool-result?id='+encodeURIComponent(rid)+(sid?'&sid='+encodeURIComponent(sid):'')); const j=await r.json(); if(r.ok && j && typeof j.result==='string') got=j.result; }
          catch(_){}
          more.disabled=false; more.textContent=prevTxt;
          if(got===null){ toast(t('chat.result_unavailable')); return; }
          full=got;
        }
        open=!open;
        code.textContent = open ? full : preview+'…';
        pre.classList.toggle('expanded', open);
        more.textContent = t(open?'chat.show_less':'chat.show_more');
      };
      body.appendChild(toolBlock(pre, more));
    } else if(preview.length>PREVIEW){
      code.textContent=preview.slice(0,PREVIEW)+'…';
      pre.appendChild(code);
      const more=document.createElement('button');
      more.className='tool-more'; more.type='button'; more.textContent=t('chat.show_more');
      let open=false;
      more.onclick=(e)=>{ e.stopPropagation(); open=!open;
        code.textContent = open ? preview : preview.slice(0,PREVIEW)+'…';
        pre.classList.toggle('expanded', open);
        more.textContent = t(open?'chat.show_less':'chat.show_more');
      };
      body.appendChild(toolBlock(pre, more));
    } else {
      code.textContent=preview; pre.appendChild(code); body.appendChild(pre);
    }
  } else if(!tu.done && !tu.typing){
    const wait=document.createElement('div'); wait.className='tool-wait'; wait.textContent=t('chat.execution_in_progress');
    body.appendChild(wait);
  }
  addCopyButtons(body); scrollMaybe();
}
// Bloc d'un résultat d'outil : le pre scrolle (max-height), donc la barre d'actions
// vit sur un bloc-parent NON scrollant, ancrée en bas à droite — elle reste au coin
// même quand on scrolle dans le résultat. La barre réunit « voir plus » (passé ici)
// et, ajouté ensuite par addCopyButtons, « copier ».
function toolBlock(pre, more){
  const block=document.createElement('div'); block.className='tool-block';
  block.appendChild(pre);
  const bar=document.createElement('div'); bar.className='tool-actions';
  bar.appendChild(more);
  block.appendChild(bar);
  return block;
}
// Inject a "copier" button into every <pre> code block (idempotent).
function addCopyButtons(root){
  root.querySelectorAll('pre').forEach(pre=>{
    // Résultat d'outil : le pre est enrobé dans .tool-block et la barre d'actions
    // est SŒUR du pre (pas dedans) ; on cherche donc le copier dans ce périmètre.
    const scope = pre.closest('.tool-block') || pre;
    if(scope.querySelector('.copybtn')) return;
    // Pas de bouton copier sur un diff : on copierait les préfixes + / - .
    if(pre.classList.contains('diff')) return;
    const btn=document.createElement('button');
    btn.className='copybtn'; btn.type='button'; btn.textContent=t('chat.copy');
    btn.onclick=async(e)=>{
      e.stopPropagation();
      const code=pre.querySelector('code'), txt=(code||pre).innerText;
      try{ await navigator.clipboard.writeText(txt); }
      catch(_){ const ta=document.createElement('textarea'); ta.value=txt; document.body.appendChild(ta); ta.select(); document.execCommand('copy'); ta.remove(); }
      btn.textContent=t('chat.copied'); btn.classList.add('done');
      setTimeout(()=>{ btn.textContent=t('chat.copy'); btn.classList.remove('done'); },1500);
    };
    // S'il y a une barre d'actions (résultat d'outil avec « voir plus »), le
    // bouton copier s'y range à côté ; sinon il se colle en bas à droite du bloc.
    (scope.querySelector('.tool-actions')||pre).appendChild(btn);
  });
}
// Mode Jean : une seule conversation, sans fin. L'ouvrir la rouvre (ou la crée) ;
// « vider le contexte » allège le modèle sans toucher au fil affiché.
function openJean(){
  const chat=document.getElementById('chat');
  if(chat && chat.querySelector('.msg')){ chat.classList.remove('chat-in'); chat.classList.add('chat-out'); }
  jfetch('/api/chat/jean',{method:'POST'}).catch(()=>{ if(chat) chat.classList.remove('chat-out'); });
}
async function clearJeanContext(){
  const r=await jfetch('/api/chat/jean/clear',{method:'POST'}).then(x=>x.json()).catch(()=>null);
  if(!r || !r.ok) toast((r && r.error) || t('jean.clear_ctx_fail'));
}
// Nouvelle conversation POUR TOUS LES APPAREILS : le serveur vide le fil et
// diffuse un {reset} ; le flux d'abonnement nettoie alors l'affichage.
// Nouvelle conversation : le fil s'efface en fondu AVANT la demande au serveur,
// puis son reset vide le chat (chatClearAnimated) et le fil vierge apparaît.
function resetChat(){
  if(MODE==='jean'){ openJean(); return; } // Jean : une seule conversation, on y revient
  const chat=document.getElementById('chat');
  const go=()=>{ jfetch('/api/chat/reset',{method:'POST'}).catch(()=>{}); toast(t('chat.new_conversation')); };
  if(!chat || !chat.querySelector('.msg') || matchMedia('(prefers-reduced-motion: reduce)').matches){ go(); return; }
  chat.classList.remove('chat-in'); chat.classList.add('chat-out');
  // Filet : si le reset n'arrive jamais (réseau), le fil réapparaît.
  clearTimeout(resetChat.t); resetChat.t=setTimeout(()=>chat.classList.remove('chat-out'), 4000);
  setTimeout(go, 200);
}
// Vide le fil et le garde invisible jusqu'au caught_up du rejeu qui suit (qui le
// révèle en fondu, en bas). Filet : révélé de toute façon après 2 s.
function hideChatForReplay(){
  const chat=document.getElementById('chat'); if(!chat) return;
  clearTimeout(resetChat.t); chat.classList.remove('chat-out','chat-in');
  chat.innerHTML=''; chat.style.transition='none'; chat.style.opacity='0';
  clearTimeout(hideChatForReplay.t);
  hideChatForReplay.t=setTimeout(()=>{ if(chat.style.opacity==='0'){ chat.style.transition='opacity .15s'; chat.style.opacity='1'; jumpBottom(); } }, 2000);
}
// Vide le fil ; s'il sortait en fondu (resetChat), le fil vierge entre en fondu.
function chatClearAnimated(){
  const chat=document.getElementById('chat'); if(!chat) return;
  chat.innerHTML='';
  if(chat.classList.contains('chat-out')){
    clearTimeout(resetChat.t); chat.classList.remove('chat-out');
    chat.classList.remove('chat-in'); void chat.offsetWidth; chat.classList.add('chat-in');
  }
}
// ===== Sessions ============================================================
// Chaque conversation est une session persistante à id stable. Le modal les
// gère : ouvrir (garde tout dans la liste), renommer, favori, supprimer, et
// démarrer une nouvelle session.
// Bouton « nouvelle conversation » (tête du menu) : archive la courante (elle
// reste dans l'historique) et repart d'un fil vierge. Rien à faire si le fil est
// déjà vide.
function newChatFromTop(){
  const c=document.getElementById('chat');
  if(c && !c.querySelector('.msg')){ document.getElementById('input')?.focus(); return; }
  resetChat();
  if(document.body.classList.contains('drawer-open')) toggleSide();
  document.getElementById('input')?.focus();
}
// Menu latéral : bascule entre le menu normal et l'historique des conversations.
// La vue qui part glisse et s'efface, l'autre arrive en glissant ; l'icône
// horloge reste allumée tant que l'historique est affiché.
let HIST_VIEW=false;
const SIDE_ICON_HIST='<svg viewBox="0 0 24 24" width="19" height="19" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5"/><path d="M12 7v5l3 2"/></svg>';
const SIDE_ICON_MENU='<svg viewBox="0 0 24 24" width="19" height="19" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 6h16"/><path d="M4 12h16"/><path d="M4 18h10"/></svg>';
function showSideView(hist){
  const main=document.getElementById('side-main'), h=document.getElementById('side-hist');
  const side=document.getElementById('side'), btn=document.getElementById('hist-toggle');
  if(!main||!h) return;
  HIST_VIEW=hist;
  if(btn){
    // Pas d'état « enfoncé » : l'icône change, directement (pas d'animation).
    // Horloge = aller à l'historique, lignes de menu = revenir au menu.
    btn.classList.remove('on');
    btn.innerHTML = hist ? SIDE_ICON_MENU : SIDE_ICON_HIST;
    btn.title = t(hist ? 'chat.back_to_menu' : 'chat.history_btn');
    btn.setAttribute('aria-label', btn.title);
  }
  const show=hist?h:main, hide=hist?main:h;
  // Pas de barre de défilement flottante pendant la bascule (fondu rapide).
  SIDE_THUMB_QUIET_UNTIL=Date.now()+450;
  const th=document.getElementById('side-thumb'); if(th) th.classList.remove('on');
  const reduce=matchMedia('(prefers-reduced-motion: reduce)').matches;
  clearTimeout(showSideView._t);
  if(reduce){ hide.hidden=true; show.hidden=false; if(side) side.scrollTop=0; return; }
  // Les deux vues bougent EN MÊME TEMPS : celle qui part passe en surimpression
  // (absolue, hors flux) et s'efface pendant que l'autre entre. Aucun instant
  // sans contenu, donc rien ne saute.
  [main,h].forEach(v=>v.classList.remove('leave-l','leave-r','enter-l','enter-r','ghost'));
  hide.classList.add('ghost');
  show.classList.add(hist?'enter-r':'enter-l'); show.hidden=false;
  if(side) side.scrollTop=0;
  void show.offsetWidth;
  show.classList.remove('enter-r','enter-l');
  hide.classList.add(hist?'leave-l':'leave-r');
  showSideView._t=setTimeout(()=>{
    hide.hidden=true; hide.classList.remove('leave-l','leave-r','ghost');
  },270);
}
function toggleHistoryView(){
  showSideView(!HIST_VIEW);
  if(HIST_VIEW) loadHistory(); // après la bascule : l'animation n'attend pas le réseau
}
// Compat : anciens appels (ouvrir = afficher l'historique, fermer = revenir au
// menu ; sur mobile on referme aussi le tiroir pour montrer la conversation).
function openHistoryModal(){ if(!HIST_VIEW) toggleHistoryView(); }
function closeHistoryModal(){
  // La vue historique RESTE affichée après avoir ouvert une conversation : seul le
  // bouton de bascule menu / historique change de vue. Sur mobile, le tiroir se referme.
  if(document.body.classList.contains('drawer-open')) toggleSide();
}
function fmtHistDate(ms){
  const d = new Date(ms||0);
  try{ return d.toLocaleString([], {dateStyle:'medium', timeStyle:'short'}); }
  catch(_){ return d.toLocaleString(); }
}
// Icônes SVG en ligne (l'app n'a pas de police d'icônes) : traits nets, prennent
// la couleur courante. On renvoie une chaîne SVG posée en innerHTML.
// Export d'une conversation (menus ⋮ de l'historique et des sessions).
const EXPORT_IC = '<svg viewBox="0 0 24 24" width="17" height="17" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12M8 11l4 4 4-4M4 19h16"/></svg>';
const SESS_ICONS = {
  star: '<path d="M12 17.75l-6.172 3.245l1.179 -6.873l-5 -4.867l6.9 -1l3.086 -6.253l3.086 6.253l6.9 1l-5 4.867l1.179 6.873z"/>',
  pencil: '<path d="M4 20h4l10.5 -10.5a2.83 2.83 0 1 0 -4 -4l-10.5 10.5v4"/><path d="M13.5 6.5l4 4"/>',
  trash: '<path d="M4 7h16"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M5 7l1 12a2 2 0 0 0 2 2h8a2 2 0 0 0 2 -2l1 -12"/><path d="M9 7v-3a1 1 0 0 1 1 -1h4a1 1 0 0 1 1 1v3"/>',
  doc: '<path d="M14 3v4a1 1 0 0 0 1 1h4"/><path d="M17 21h-10a2 2 0 0 1 -2 -2v-14a2 2 0 0 1 2 -2h7l5 5v11a2 2 0 0 1 -2 2z"/><path d="M9 9h1"/><path d="M9 13h6"/><path d="M9 17h6"/>',
  move: '<path d="M2 9V5a2 2 0 0 1 2-2h3.6a1 1 0 0 1 .8.4l1.2 1.6a1 1 0 0 0 .8.4H20a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2"/><path d="M2 13h10"/><path d="M9 16l3-3-3-3"/>',
  mem: '<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/><path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>',
  chart: '<path d="M4 19V5M4 19h16M8 16l3-4 3 2 4-6"/>'
};
function sessIconSvg(name, filled){
  return '<svg viewBox="0 0 24 24" width="17" height="17" fill="'+(filled?'currentColor':'none')+'" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">'+SESS_ICONS[name]+'</svg>';
}
// Une ligne de session. Cliquer la ligne OUVRE la session (sauf l'active). L'étoile
// bascule le favori, le crayon renomme, la corbeille supprime.
function sessionRow(c, active){
  const row = document.createElement('div'); row.className = 'sess-row' + (active?' active':'');
  if(!active){ row.tabIndex = 0; row.title = t('chat.session.open_this');
    row.onclick = ()=>restoreHistory(c.id, c.mode);
    row.onkeydown = (e)=>{ if(e.key==='Enter'||e.key===' '){ e.preventDefault(); restoreHistory(c.id, c.mode); } };
  }
  const info = document.createElement('div'); info.className = 'sess-info';
  const name = document.createElement('div'); name.className = 'sess-name';
  if(c.fav){ const st=document.createElement('span'); st.className='sess-fav'; st.innerHTML=sessIconSvg('star', true); name.appendChild(st); }
  name.appendChild(document.createTextNode(c.title || t('chat.session.default_name')));
  const meta = document.createElement('div'); meta.className = 'sess-meta';
  const n = c.turns || 0;
  // Projet de la conversation : affiché seulement s'il diffère du projet actif
  // (résultats de recherche, qui couvrent tous les projets).
  const pn = (c.project && c.mode!=='fast' && c.mode!=='base' && c.project !== (ACTIVE_PROJECT||HIST_DEFAULT_PROJ)) ? (HIST_PROJ_NAMES[c.project] || c.project) : '';
  if(pn){ const pj=document.createElement('span'); pj.className='sess-proj'; pj.textContent=pn; meta.appendChild(pj); }
  meta.appendChild(document.createTextNode(fmtHistDate(c.saved_at) + ' · ' + n + ' ' + (n>1?t('chat.session.messages'):t('chat.session.message')) + (active?' · '+t('chat.session.ongoing'):'')));
  info.appendChild(name); info.appendChild(meta);
  // Favori / renommer / exporter / supprimer : dans un petit menu ⋮.
  const more = document.createElement('button'); more.className = 'sess-menu-btn'; more.innerHTML = projDotsSvg();
  more.title = t('chat.session.actions'); more.setAttribute('aria-label', more.title);
  more.onclick = (e)=>{ e.stopPropagation(); openHistRowMenu(more, c); };
  row.appendChild(info); row.appendChild(more);
  return row;
}
// Menu ⋮ d'une ligne d'historique.
function openHistRowMenu(anchor, c){
  popMenu(anchor, [
    {icon:'star', label:c.fav?t('chat.session.unfav'):t('chat.session.fav'), run:()=>favHistory(c.id, !c.fav)},
    {icon:'pencil', label:t('chat.session.rename'), run:()=>renameHistory(c.id, c.title)},
    PROJECTS.length > 1 && c.id !== HIST_ST.active && c.mode!=='fast' && c.mode!=='base' && {icon:'move', label:t('projects.move_to'), run:()=>moveSessionUI(c, anchor)}, // la conversation ouverte ne se déplace pas
    {icon:EXPORT_IC, label:t('projects.export'), run:()=>downloadExport('/api/chat/export?id='+encodeURIComponent(c.id))},
    {icon:'trash', label:t('chat.session.delete_permanently'), danger:true, run:()=>deleteHistory(c.id, c.title)},
  ], {side:'below'});
}
let HIST_PROJ_NAMES = {}, HIST_DEFAULT_PROJ = '';
// Historique par PAGES (60 lignes, la suite au défilement) : construire d'un coup
// les 500+ conversations figeait l'ouverture. HIST_SIG évite de reconstruire une
// liste identique (préchargement puis ouverture : rien ne bouge à l'écran).
const HIST_PAGE = 60;
let HIST_ST = {off:0, total:0, active:'', section:'', loading:false, sig:''};
let HIST_Q = '', HIST_QT = 0;
// Historique = conversations du PROJET ACTIF ; une recherche, elle, couvre tous les projets.
const histScope = ()=>(MODE==='fast'||MODE==='base') ? MODE : ''; // Rapide et Modèle de base : chacun son historique
const histUrl = (off)=>'/api/chat/history?'+(HIST_Q?'all=1&':(histScope()?'scope='+histScope()+'&':''))+'offset='+off+'&limit='+HIST_PAGE+(HIST_Q?'&q='+encodeURIComponent(HIST_Q):'');
// Recherche : on attend une courte pause dans la frappe avant d'interroger.
function onHistSearch(){
  clearTimeout(HIST_QT);
  HIST_QT = setTimeout(()=>{ HIST_Q = document.getElementById('hist-q').value.trim(); loadHistory(); }, 180);
}
function histAppend(box, list, animate){
  const section = (label)=>{ const h=document.createElement('div'); h.className='sess-head'; h.textContent=label; box.appendChild(h); };
  let i = 0;
  for(const c of list){
    const sec = c.fav ? 'fav' : 'recent';
    if(sec !== HIST_ST.section){
      if(sec === 'fav') section(t('chat.session.favorites'));
      else if(HIST_ST.section === 'fav') section(t('chat.session.recent'));
      HIST_ST.section = sec;
    }
    const row = sessionRow(c, c.id===HIST_ST.active);
    box.appendChild(row); i++;
  }
}
async function loadHistory(){
  const box = document.getElementById('history-list'); if(!box) return;
  // Mode Jean : une seule conversation, pas d'historique (on change de mode pour le voir).
  if(MODE==='jean'){ HIST_ST={off:0,total:0,active:'',section:'',loading:false,sig:'jean'}; box.innerHTML='<span class="muted" style="font-size:12px">'+t('jean.no_history')+'</span>'; return; }
  let r;
  try{ r = await jget(histUrl(0)); }
  catch(_){ if(!box.children.length) box.innerHTML = '<span class="muted" style="font-size:12px">'+t('chat.session.load_error')+'</span>'; return; }
  const list = (r && r.conversations) || [];
  HIST_PROJ_NAMES = (r && r.projects) || {}; HIST_DEFAULT_PROJ = (r && r.default_project) || '';
  const sig = JSON.stringify([HIST_Q, r.active, r.total, list.map(c=>[c.id,c.title,c.fav,c.turns,c.project])]);
  const cnt = document.getElementById('sess-count'); if(cnt) cnt.textContent = r.total || '';
  // « Tout supprimer » masqué pendant une recherche : il viderait tout l'historique,
  // pas seulement les résultats affichés.
  const clr = document.getElementById('history-clear-all'); if(clr) clr.hidden = !!HIST_Q;
  if(sig === HIST_ST.sig && box.children.length) return; // déjà à jour : on ne touche à rien
  const fresh = !box.querySelector('.sess-row'); // première apparition : fondu des lignes
  HIST_ST = {off:list.length, total:r.total||list.length, active:(r && r.active)||'', section:'', loading:false, sig};
  if(!list.length){ box.innerHTML = '<span class="muted" style="font-size:12px">'+t(HIST_Q?'chat.history_no_results':'chat.session.empty')+'</span>'; return; }
  const frag = document.createDocumentFragment();
  const tmp = {appendChild:(n)=>frag.appendChild(n)};
  histAppend(tmp, list, fresh && HIST_VIEW);
  box.replaceChildren(frag);
}
// Suite de la liste quand on approche du bas du menu (vue historique seulement).
async function loadMoreHistory(){
  const box = document.getElementById('history-list');
  if(!box || HIST_ST.loading || HIST_ST.off >= HIST_ST.total) return;
  HIST_ST.loading = true;
  try{
    const r = await jget(histUrl(HIST_ST.off));
    const list = (r && r.conversations) || [];
    const frag = document.createDocumentFragment();
    histAppend({appendChild:(n)=>frag.appendChild(n)}, list, false);
    box.appendChild(frag);
    HIST_ST.off += list.length;
  }catch(_){}
  HIST_ST.loading = false;
}
// Rafraîchit la liste peu après un événement qui la change (nouveau message, autre
// conversation, fin de tour) : une conversation neuve y apparaît tout de suite.
let HIST_RT=0;
function histRefreshSoon(){ if(typeof REPLAYING!=='undefined' && REPLAYING) return; clearTimeout(HIST_RT); HIST_RT=setTimeout(loadHistory, 700); }
function histInit(){
  const side = document.getElementById('side');
  if(side) side.addEventListener('scroll', ()=>{
    if(HIST_VIEW && side.scrollTop + side.clientHeight > side.scrollHeight - 300) loadMoreHistory();
  }, {passive:true});
  // Préchargement discret : la première ouverture de l'historique est instantanée.
  setTimeout(()=>{ loadHistory(); }, 2500);
}
if(document.readyState==='loading') document.addEventListener('DOMContentLoaded', histInit); else histInit();
// Bascule le favori d'une session (étoile).
async function favHistory(id, fav){
  let r; try{ r = await jpost('/api/chat/history/fav', {id, fav}); }catch(_){ toast(t('chat.session.network_error')); return; }
  if(!r.ok){ toast(r.error || t('chat.session.impossible')); return; }
  loadHistory();
}
// Renommer une session (le favori se gère à l'étoile).
async function renameHistory(id, current){
  const name = await askPrompt(t('chat.session.rename_prompt'), {title:t('chat.session.rename_title'), okText:t('chat.session.save'), default: current||'', placeholder:t('chat.session.rename_placeholder')});
  if(name===null) return; // annulé
  let r; try{ r = await jpost('/api/chat/history/rename', {id, title:name}); }catch(_){ toast(t('chat.session.network_error')); return; }
  if(!r.ok){ toast(r.error || t('chat.session.rename_error')); return; }
  loadHistory();
}
// Supprime toutes les sessions SAUF les favoris (et la session en cours).
async function clearAllHistory(){
  if(!await askConfirm(t('chat.session.clear_all_confirm'), {title:t('chat.session.clear_all_title'), okText:t('chat.session.clear_all_ok'), danger:true})) return;
  let r; try{ r = await jpost('/api/chat/history/clear', {scope:histScope()}); }catch(_){ toast(t('chat.session.network_error')); return; }
  if(!r.ok){ toast(r.error || t('chat.session.delete_error')); return; }
  toast((r.deleted||0) + ' ' + ((r.deleted>1)?t('chat.session.deleted_plural'):t('chat.session.deleted_singular')));
  loadHistory();
}
async function restoreHistory(id, mode){
  let r; try{ r = await jpost('/api/chat/history/restore', {id}); }catch(_){ toast(t('chat.session.network_error')); return; }
  if(!r.ok){ toast(r.error || t('chat.session.open_error')); return; }
  closeHistoryModal();
  if(mode) setModeFromConv(mode); // la conversation reprend dans SON mode
  // Une conversation d'un autre projet (recherche) bascule le projet : l'historique suit.
  Promise.resolve(loadProjects()).then(()=>loadHistory());
  toast(t('chat.session.opened'));
}
async function deleteHistory(id, title){
  if(!await askConfirm(t('chat.session.delete_confirm_prefix') + (title || t('chat.session.this_session')) + t('chat.session.delete_confirm_suffix'), {title:t('chat.session.delete_title'), okText:t('chat.session.delete_ok'), danger:true})) return;
  let r; try{ r = await jpost('/api/chat/history/delete', {id}); }catch(_){ toast(t('chat.session.network_error')); return; }
  if(!r.ok){ toast(r.error || t('chat.session.delete_error')); return; }
  loadHistory();
}
// Compaction : on demande à l'IA un résumé de la conversation destiné à la
// reprendre dans une session neuve, puis on repart d'un contexte propre seedé
// avec ce résumé. Réduit drastiquement les tokens tout en gardant le fil.
// Compaction MANUELLE : le compactage est automatique (façon Hermes) quand le
// contexte se remplit, mais ce bouton permet de le déclencher à la demande. Le
// serveur possède la conversation : on lance la compaction côté serveur et la
// progression (bannière « compactage en cours », résultat) arrive par le flux
// d'abonnement, comme pour la génération — donc visible sur tous les appareils.
async function compactContext(){
  if(!await askConfirm(t('chat.compact_confirm'), {title:t('chat.compact_title'), okText:t('chat.compact_ok')})) return;
  try{
    const r=await jfetch('/api/chat/compact',{method:'POST',headers:{'Content-Type':'application/json'},body:'{}'});
    const j=await r.json().catch(()=>({}));
    if(!j.ok) toast(j.error||t('chat.compact_unavailable'));
  }catch(e){ toast(t('common.error_prefix')+(e.message||e)); }
}
// Persistance de la conversation : on garde user+assistant en localStorage pour
// survivre à un refresh (les bulles tool/reasoning sont éphémères, non stockées).
function saveChat(){ try{ localStorage.setItem('ajean.chat', JSON.stringify(msgs)); }catch(e){} }
// Source de vérité = SERVEUR. Au chargement on ouvre le flux d'abonnement
// permanent (connectStream), qui rejoue tout le fil depuis le serveur — texte,
// appels d'outils, vitesses, raisonnement — puis suit le direct. Plus de
// localStorage : le même contexte est partagé par tous les appareils.
// Source de vérité = SERVEUR : on ouvre le flux d'abonnement permanent qui rejoue
// tout le fil (texte, outils, vitesses via les horodatages serveur, raisonnement)
// puis suit le direct. Partagé par tous les appareils.
// Voile de chargement du fil. setChatLoading(null) le masque, setChatLoading(txt)
// l'affiche avec ce libellé (« chargement… » au départ, « connexion au serveur… »
// si le flux tombe). Sans lui, une connexion lente affiche un chat vide qu'on ne
// distingue pas d'une conversation réellement vide.
// Durée minimale d'affichage du voile : sur un chargement ultra-rapide, le logo
// n'apparaîtrait qu'une fraction de seconde et « clignoterait », ce qui est moche.
// On le garde donc visible le temps d'AU MOINS un cycle de pulse (~1,1 s) une fois
// montré, puis il disparaît en fondu (transition CSS sur #chat-loading).
const CL_MIN_MS = 1150;
// Le voile est déjà affiché dans le HTML (classe show) pour éviter tout flash
// « interface → logo → interface » au premier rendu : on démarre donc le chrono dès
// le chargement du script.
let _clShownAt = Date.now(), _clHideTimer = null;
// QUIET_LOADER_UNTIL : pendant une bascule de projet, le fil se vide et se
// recharge en une fraction de seconde ; le gros logo pulsant (tenu au moins
// 1,15 s) y faisait un flash inutile. On ne l'affiche donc pas dans cette fenêtre.
let QUIET_LOADER_UNTIL=0;
function setChatLoading(msg){
  const el=document.getElementById('chat-loading');
  if(!el) return;
  if(msg && Date.now()<QUIET_LOADER_UNTIL){ el.classList.remove('show'); _clShownAt=0; return; }
  if(!msg){
    // Masquage : si le voile n'a pas encore été affiché assez longtemps, on retarde
    // le masquage du temps restant pour éviter le clignotement.
    const elapsed = _clShownAt ? (Date.now() - _clShownAt) : CL_MIN_MS;
    const wait = Math.max(0, CL_MIN_MS - elapsed);
    clearTimeout(_clHideTimer);
    _clHideTimer = setTimeout(()=>{ el.classList.remove('show'); _clShownAt = 0; }, wait);
    return;
  }
  // Affichage : la marque « J » (favicon) en grand qui pulse (comme le compactage).
  // Le SVG est déjà dans le HTML ; on garde le libellé en aria-label.
  clearTimeout(_clHideTimer); _clHideTimer = null;
  if(!_clShownAt) _clShownAt = Date.now();
  el.setAttribute('aria-label', msg);
  el.classList.add('show');
}
// --- Accueil du fil vide ---------------------------------------------------
// Le logo n'est pas dupliqué dans le HTML : on clone celui de la barre latérale
// (#brand) en retirant ses id (un id ne peut exister qu'une fois) et le numéro
// de version. Les couleurs sont reprises par les classes .ce-*.
function cloneBrandInto(boxId, wordClass){
  const box=document.getElementById(boxId), brand=document.getElementById('brand');
  if(!box || !brand || box.childElementCount) return;
  ['brand-a','brand-word'].forEach(id=>{
    const src=brand.querySelector('#'+id); if(!src) return;
    const el=src.cloneNode(true); el.removeAttribute('id');
    if(id==='brand-word' && wordClass) el.classList.add(wordClass);
    box.appendChild(el);
  });
}
function fillEmptyLogo(){ cloneBrandInto('ce-logo', 'ce-word'); }
// Affiché seulement quand le fil ne contient AUCUNE bulle et que le replay est
// terminé — sinon il apparaîtrait une fraction de seconde à chaque chargement,
// juste avant que les messages rejoués n'arrivent.
function syncChatEmpty(){
  const box=document.getElementById('chat-empty'); if(!box) return;
  fillEmptyLogo();
  const empty = !REPLAYING && !chatEl().querySelector('.msg');
  box.classList.toggle('show', empty);
}
document.addEventListener('DOMContentLoaded', ()=>{
  const c=chatEl(); if(!c) return;
  // Le fil est peuplé par des dizaines de chemins différents (replay, direct,
  // reset, effacement). On observe donc le DOM plutôt que d'appeler la synchro
  // depuis chacun d'eux — le coût est nul, le callback est groupé et sort tout
  // de suite pendant le replay.
  new MutationObserver(()=>syncChatEmpty()).observe(c, {childList:true});
  syncChatEmpty();
});
function restoreChat(){
  // On masque le chat le temps du replay pour ne pas voir défiler le haut puis
  // sauter en bas (effet de clignotement). Il est révélé, positionné en bas, au
  // signal {caught_up}. Filet de sécurité : révélé quoi qu'il arrive après 2s.
  const c=chatEl(); c.style.opacity='0';
  setChatLoading(t('chat.loading_conversation'));
  // Si {caught_up} tarde au-delà de 2s (replay anormalement long), on révèle quand
  // même — et on saute en bas DIRECTEMENT (scrollMaybe est neutralisé tant que
  // REPLAYING, donc on force ici le positionnement). Le voile, lui, RESTE : tant
  // que le replay n'est pas fini, ce qui est affiché est incomplet et il faut le
  // dire. Il finit de toute façon par tomber au {caught_up} ou au filet de 15s.
  setTimeout(()=>{ c.style.transition='opacity .15s'; c.style.opacity='1'; c.scrollTop=c.scrollHeight; }, 2000);
  setTimeout(()=>{ setChatLoading(null); }, 15000);
  connectStream();
}
// Deux modes, réglables dans Apparence (issue #44). Par défaut : Entrée envoie,
// Maj+Entrée fait un retour à la ligne. Avec « Entrée = retour à la ligne » coché,
// on inverse : Entrée insère un saut de ligne et c'est Maj/Ctrl/Cmd+Entrée qui
// envoie. isComposing évite d'envoyer en pleine saisie IME (accents, japonais…).
function onKey(e){
  if(e.key!=='Enter' || e.isComposing) return;
  const withMod = e.shiftKey || e.ctrlKey || e.metaKey;
  const shouldSend = viewOn('enter-newline') ? withMod : !e.shiftKey;
  if(shouldSend){ e.preventDefault(); send(); }
}
// Libellé sous le champ, cohérent avec l'option « Entrée = retour à la ligne »
// (issue #48 : il restait figé sur le mode par défaut). refreshSendHint ne touche
// pas au texte quand le modèle charge (état « waiting »), pour ne pas écraser le
// message d'attente posé par le flux d'état.
function sendHintText(){
  return viewOn('enter-newline')
    ? t('chat.send_hint_newline_mode')
    : t('chat.send_hint_default');
}
function refreshSendHint(){
  const h=document.getElementById('sendhint');
  if(h && !h.classList.contains('waiting')) h.textContent=sendHintText();
}
// La zone de saisie s'ajuste à son contenu : une ligne au repos, puis elle
// grandit jusqu'à sa max-height (au-delà, elle défile). Appelée à la frappe, à
// l'envoi et au chargement.
function autoGrow(ta){
  ta = ta || document.getElementById('input');
  if(!ta) return;
  ta.style.height='auto';
  const max=parseInt(getComputedStyle(ta).maxHeight,10)||200;
  ta.style.height=Math.min(ta.scrollHeight, max)+'px';
  ta.style.overflowY = ta.scrollHeight>max ? 'auto' : 'hidden';
}
