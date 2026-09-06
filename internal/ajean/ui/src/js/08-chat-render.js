let stickyBottom = true;
const chatEl = () => document.getElementById('chat');
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
      if(force || now - _lastScrollWrite >= SCROLL_MIN_GAP){
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
function renderBody(el, text){ const b=bodyOf(el); b.innerHTML = md(encodeMdLinkSpaces(text)); markNotices(b); addCopyButtons(b); markFileLinks(b); scrollMaybe(); }
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
  if(tu.result){ lbl += '  ·  ' + Math.max(1, Math.round(tu.result.length/4)) + ' ' + t('chat.tok_unit'); }
  setLabel(el, lbl);
  // Volume de l'écriture (final si le diff est là, provisoire pendant la frappe)
  // reporté sur l'étiquette, pour rester lisible bulle repliée.
  let add=0, del=0;
  if(tu.diff && tu.diff.length){ tu.diff.forEach(l=>{ if(l.op==='+') add++; else if(l.op==='-') del++; }); }
  else if(tu.body){ add=tu.body.split('\n').length; }
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
    const code=document.createElement('code'); code.textContent=tu.result;
    pre.appendChild(code); body.appendChild(pre);
  } else if(!tu.done && !tu.typing){
    const wait=document.createElement('div'); wait.className='tool-wait'; wait.textContent=t('chat.execution_in_progress');
    body.appendChild(wait);
  }
  addCopyButtons(body); scrollMaybe();
}
// Inject a "copier" button into every <pre> code block (idempotent).
function addCopyButtons(root){
  root.querySelectorAll('pre').forEach(pre=>{
    if(pre.querySelector('.copybtn')) return;
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
    pre.appendChild(btn);
  });
}
// Nouvelle conversation POUR TOUS LES APPAREILS : le serveur vide le fil et
// diffuse un {reset} ; le flux d'abonnement nettoie alors l'affichage.
function resetChat(){ jfetch('/api/chat/reset',{method:'POST'}).catch(()=>{}); toast(t('chat.new_conversation')); }
// ===== Sessions ============================================================
// Chaque conversation est une session persistante à id stable. Le modal les
// gère : ouvrir (garde tout dans la liste), renommer, favori, supprimer, et
// démarrer une nouvelle session.
function openHistoryModal(){ showModal('history-modal'); loadHistory(); }
function closeHistoryModal(){ hideModal('history-modal'); }
function fmtHistDate(ms){
  const d = new Date(ms||0);
  try{ return d.toLocaleString([], {dateStyle:'medium', timeStyle:'short'}); }
  catch(_){ return d.toLocaleString(); }
}
// Icônes SVG en ligne (l'app n'a pas de police d'icônes) : traits nets, prennent
// la couleur courante. On renvoie une chaîne SVG posée en innerHTML.
const SESS_ICONS = {
  star: '<path d="M12 17.75l-6.172 3.245l1.179 -6.873l-5 -4.867l6.9 -1l3.086 -6.253l3.086 6.253l6.9 1l-5 4.867l1.179 6.873z"/>',
  pencil: '<path d="M4 20h4l10.5 -10.5a2.83 2.83 0 1 0 -4 -4l-10.5 10.5v4"/><path d="M13.5 6.5l4 4"/>',
  trash: '<path d="M4 7h16"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M5 7l1 12a2 2 0 0 0 2 2h8a2 2 0 0 0 2 -2l1 -12"/><path d="M9 7v-3a1 1 0 0 1 1 -1h4a1 1 0 0 1 1 1v3"/>',
  doc: '<path d="M14 3v4a1 1 0 0 0 1 1h4"/><path d="M17 21h-10a2 2 0 0 1 -2 -2v-14a2 2 0 0 1 2 -2h7l5 5v11a2 2 0 0 1 -2 2z"/><path d="M9 9h1"/><path d="M9 13h6"/><path d="M9 17h6"/>',
  move: '<path d="M9 6l6 0"/><path d="M9 6l-3 3l3 3"/><path d="M15 12l3 3l-3 3"/><path d="M6 9v10a1 1 0 0 0 1 1h11"/>'
};
function sessIconSvg(name, filled){
  return '<svg viewBox="0 0 24 24" width="17" height="17" fill="'+(filled?'currentColor':'none')+'" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">'+SESS_ICONS[name]+'</svg>';
}
// Une ligne de session. Cliquer la ligne OUVRE la session (sauf l'active). L'étoile
// bascule le favori, le crayon renomme, la corbeille supprime.
function sessionRow(c, active){
  const row = document.createElement('div'); row.className = 'sess-row' + (active?' active':'');
  if(!active){ row.tabIndex = 0; row.title = t('chat.session.open_this');
    row.onclick = ()=>restoreHistory(c.id);
    row.onkeydown = (e)=>{ if(e.key==='Enter'||e.key===' '){ e.preventDefault(); restoreHistory(c.id); } };
  }
  // Étoile favori (clic = bascule), indépendante du clic d'ouverture.
  const star = document.createElement('button');
  star.className = 'sess-star' + (c.fav?' on':''); star.innerHTML = sessIconSvg('star', c.fav);
  star.title = c.fav?t('chat.session.unfav'):t('chat.session.fav');
  star.onclick = (e)=>{ e.stopPropagation(); favHistory(c.id, !c.fav); };

  const info = document.createElement('div'); info.className = 'sess-info';
  const name = document.createElement('div'); name.className = 'sess-name'; name.textContent = c.title || t('chat.session.default_name');
  const meta = document.createElement('div'); meta.className = 'sess-meta';
  const n = c.turns || 0;
  meta.textContent = fmtHistDate(c.saved_at) + ' · ' + n + ' ' + (n>1?t('chat.session.messages'):t('chat.session.message')) + (active?' · '+t('chat.session.ongoing'):'');
  info.appendChild(name); info.appendChild(meta);

  const acts = document.createElement('div'); acts.className = 'sess-acts';
  if(active){ const badge = document.createElement('span'); badge.className = 'sess-badge'; badge.textContent = t('chat.session.ongoing'); acts.appendChild(badge); }
  const actBtn = (name, title, fn)=>{
    const b = document.createElement('button'); b.className = 'sess-act'; b.innerHTML = sessIconSvg(name);
    b.title = title; b.onclick = (e)=>{ e.stopPropagation(); fn(); };
    return b;
  };
  acts.appendChild(actBtn('pencil', t('chat.session.rename'), ()=>renameHistory(c.id, c.title)));
  acts.appendChild(actBtn('trash', t('chat.session.delete_permanently'), ()=>deleteHistory(c.id, c.title)));

  row.appendChild(star); row.appendChild(info); row.appendChild(acts);
  return row;
}
async function loadHistory(){
  const box = document.getElementById('history-list'); if(!box) return;
  // Ne pas vider tout de suite : on garde l'affichage précédent (ou un discret
  // « chargement » à la toute première ouverture) le temps de la requête, pour
  // éviter le clignotement vide→plein.
  if(!box.children.length) box.innerHTML = '<span class="muted" style="font-size:12px">'+t('chat.session.loading')+'</span>';
  let list = [], active = '';
  try{ const r = await jget('/api/chat/history'); list = (r && r.conversations) || []; active = (r && r.active) || ''; }
  catch(_){ box.innerHTML = '<span class="muted" style="font-size:12px">'+t('chat.session.load_error')+'</span>'; return; }
  const cnt = document.getElementById('sess-count'); if(cnt) cnt.textContent = list.length || '';
  if(!list.length){ box.innerHTML = '<span class="muted" style="font-size:12px">'+t('chat.session.empty')+'</span>'; return; }
  box.innerHTML = '';
  const favs = list.filter(c=>c.fav), others = list.filter(c=>!c.fav);
  const section = (label)=>{ const h=document.createElement('div'); h.className='sess-head'; h.textContent=label; box.appendChild(h); };
  if(favs.length){ section(t('chat.session.favorites')); favs.forEach(c=>box.appendChild(sessionRow(c, c.id===active))); }
  if(others.length){ if(favs.length) section(t('chat.session.recent')); others.forEach(c=>box.appendChild(sessionRow(c, c.id===active))); }
}
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
  let r; try{ r = await jpost('/api/chat/history/clear', {}); }catch(_){ toast(t('chat.session.network_error')); return; }
  if(!r.ok){ toast(r.error || t('chat.session.delete_error')); return; }
  toast((r.deleted||0) + ' ' + ((r.deleted>1)?t('chat.session.deleted_plural'):t('chat.session.deleted_singular')));
  loadHistory();
}
async function restoreHistory(id){
  let r; try{ r = await jpost('/api/chat/history/restore', {id}); }catch(_){ toast(t('chat.session.network_error')); return; }
  if(!r.ok){ toast(r.error || t('chat.session.open_error')); return; }
  closeHistoryModal();
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
function setChatLoading(msg){
  const el=document.getElementById('chat-loading');
  if(!el) return;
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
