// ===== Scène du mode Jean ====================================================
// En mode Jean, plus de fil de discussion classique : Jean en grand au centre de
// l'écran. On clique sur lui (ou on tape) : une bulle de saisie sort de lui. À
// l'envoi, la bulle est aspirée par Jean, qui l'avale. Il réfléchit (une phrase
// sous lui dit ce qu'il fait, de petits points tournent autour de lui), puis sa
// réponse arrive dans une bulle au-dessus de lui. Quand il pilote le navigateur,
// un petit écran sort au-dessus de lui et montre la page en direct, comme s'il
// travaillait sur son ordinateur.
//
// La scène n'est qu'une PRÉSENTATION : envoi, file d'attente, arrêt, flux et fil
// restent ceux de l'app. L'envoi passe par send() (09-stream.js) via le champ
// #input ; la bulle de réponse reflète la dernière réponse du fil (#chat), en
// direct comme après un rechargement. Le bouton en haut à droite bascule sur le
// fil complet (body.jean-history) et en revient.
(()=>{
  let srch, srchQ, srchList, srchSig='';
  const srchSeen=new WeakSet();
  let term, termView, termKey='', termTyped=0, termTimer=0, termSig='', avatarLooking=false, termRuns=[], termCmd='';
  const termSeen=new WeakSet(); // résultats déjà apparus (leur fondu ne se rejoue pas)
  let stage, scene, bubble, bubbleBody, caption, capPast, capText, capStop, inputBox, ta, sendBtn, micBtn, histBtn, filesBtn;
  let screen, screenView, screenUrl, screenSrc='';
  let jeanWasOn=false;
  let inputOpen=false, syncTimer=0, lastHTML='', userScrolled=false;

  const $=(tag, cls, parent)=>{ const n=document.createElement(tag); if(cls) n.className=cls; if(parent) parent.appendChild(n); return n; };
  const inStage=()=>document.body.classList.contains('jean-mode') && !document.body.classList.contains('jean-history');

  // Début du travail en cours (0 = au repos) : ELAPSED.start quand le flux est
  // là, sinon l'instant où la scène a vu Jean se mettre au travail.
  let workStart=0;
  function paintWorkTime(){
    if(!capStop) return;
    const el=capStop.querySelector('.cs-t'); if(!el) return;
    if(!workStart || capStop.hidden){ el.textContent=''; return; }
    if(ELAPSED && ELAPSED.start && ELAPSED.start<workStart) workStart=ELAPSED.start;
    const s=Math.max(0, Math.floor((Date.now()-workStart)/1000));
    el.textContent=' · '+(s<60 ? s+' s' : Math.floor(s/60)+' min '+String(s%60).padStart(2,'0')+' s');
  }
  function build(){
    const main=document.querySelector('.main'); if(!main) return false;
    stage=$('div','', main); stage.id='jean-stage';
    histBtn=$('button','jst-hist', main); histBtn.type='button';
    histBtn.onclick=toggleHistory;
    // Les fichiers de Jean (23-jean-files.js), à portée de la scène.
    filesBtn=$('button','jst-files', main); filesBtn.type='button';
    filesBtn.innerHTML='<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>';
    filesBtn.onclick=()=>{ if(typeof openJeanFiles==='function') openJeanFiles(); };
    scene=$('div','jst-scene', stage);
    bubble=$('div','jst-bubble', scene);
    bubbleBody=$('div','jst-bubble-body body', bubble);
    bubbleBody.addEventListener('scroll', ()=>{ userScrolled = bubbleBody.scrollHeight-bubbleBody.scrollTop-bubbleBody.clientHeight > 30; }, {passive:true});
    // Écran du navigateur piloté : la page qu'il consulte, en direct.
    screen=$('div','jst-screen', scene);
    screenUrl=$('div','jst-screen-url', screen);
    screenView=$('div','jst-screen-view', screen);
    // Terminal : même principe que l'écran du navigateur, pour les commandes bash.
    term=$('div','jst-term', scene);
    const tbar=$('div','jst-term-bar', term);
    for(let i=0;i<3;i++) $('span','', tbar);
    $('div','jst-term-title', tbar).textContent='terminal';
    termView=$('pre','jst-term-view', term);
    // Recherche web : la requête puis les résultats, présentés comme une page de résultats.
    srch=$('div','jst-srch', scene);
    const sbar=$('div','jst-srch-bar', srch);
    sbar.innerHTML='<svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg>';
    srchQ=$('span','jst-srch-q', sbar);
    srchList=$('div','jst-srch-list', srch);
    const charWrap=$('div','jst-char', scene);
    const orbit=$('div','jst-orbit', charWrap);
    for(let i=0;i<3;i++) $('span','', orbit).style.setProperty('--i', i);
    const charHost=$('div','jst-char-host', charWrap);
    const zzz=$('div','jst-zzz', charWrap); zzz.setAttribute('aria-hidden','true');
    for(let i=0;i<3;i++){ const z=$('span','', zzz); z.textContent='z'; z.style.setProperty('--i', i); }
    caption=$('div','jst-caption', scene);
    capPast=$('div','jst-cap-past', caption);
    capText=$('div','jst-cap-text', caption);
    capStop=$('button','jst-cap-stop', caption); capStop.type='button'; capStop.hidden=true; capStop.onclick=()=>stopGen();
    // Temps de travail du tour, dans le bouton : « Arrêter · 1 min 12 s ».
    capStop.innerHTML='<span class="cs-l"></span><span class="cs-t"></span>';
    setInterval(paintWorkTime, 1000);
    inputBox=$('div','jst-input', scene);
    ta=$('textarea','', inputBox); ta.rows=1;
    ta.setAttribute('autocomplete','off'); ta.setAttribute('name','jean-message');
    ta.setAttribute('enterkeyhint','send'); ta.setAttribute('aria-label','message');
    // Dictée vocale (24-dictation.js) : écrit dans ce champ.
    micBtn=$('button','jst-mic mic-btn', inputBox); micBtn.type='button';
    micBtn.innerHTML='<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="3" width="6" height="11" rx="3"/><path d="M5 11a7 7 0 0 0 14 0"/><path d="M12 18v3"/></svg>';
    micBtn.onmousedown=e=>e.preventDefault(); // garde le focus du champ (sinon il se referme)
    micBtn.onpointerdown=()=>{ if(typeof dictWarm==='function') dictWarm(); };
    micBtn.onclick=()=>{ if(typeof toggleDictation==='function') toggleDictation(micBtn, ta); };
    sendBtn=$('button','jst-send', inputBox); sendBtn.type='button';
    sendBtn.innerHTML='<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5M5 12l7-7 7 7"/></svg>';
    sendBtn.onclick=submit;
    // Toucher n'importe où sur la scène ouvre la saisie avec le clavier (focus donné
    // dans le geste, voir openInput). Sauf la bulle de réponse (on doit pouvoir la
    // faire défiler ou y sélectionner du texte) et l'écran du navigateur.
    scene.addEventListener('click', e=>{
      if(sendBtn.contains(e.target) || micBtn.contains(e.target) || bubble.contains(e.target) || screen.contains(e.target) || capStop.contains(e.target)) return;
      if(document.activeElement===ta) return;
      openInput();
    });
    // Clavier refermé sans rien écrit : la bulle de saisie se replie aussi, plutôt
    // que de rester affichée à attendre un toucher précis.
    ta.addEventListener('blur', ()=>setTimeout(()=>{ if(document.activeElement!==ta && !micBtn.classList.contains('rec') && !micBtn.classList.contains('busy') && !ta.value.trim() && inputOpen && !inputBox.classList.contains('gulp')) closeInput(); }, 200));
    ta.addEventListener('input', ()=>{ grow(); JeanAvatar&&JeanAvatar.listen(); });
    ta.addEventListener('keydown', e=>{
      if(e.key==='Escape'){ e.preventDefault(); closeInput(); return; }
      if(e.key!=='Enter' || e.isComposing) return;
      const withMod=e.shiftKey||e.ctrlKey||e.metaKey;
      if(viewOn('enter-newline') ? withMod : !e.shiftKey){ e.preventDefault(); submit(); }
    });
    JeanAvatar&&JeanAvatar.mount(charHost);
    applyTexts();
    return true;
  }

  function applyTexts(){
    ta.placeholder=t('jean.stage_placeholder');
    sendBtn.title=t('chat.send_button_title');
    micBtn.title=micBtn.ariaLabel=t('stt.mic_title');
    filesBtn.title=filesBtn.ariaLabel=t('jean.files_title');
    capStop.querySelector('.cs-l').textContent=t('jean.stage_stop');
    const hist=document.body.classList.contains('jean-history');
    histBtn.title=hist ? t('jean.stage_back') : t('jean.stage_history');
    histBtn.innerHTML = hist
      ? '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="8"/><circle cx="9.5" cy="11" r="1" fill="currentColor"/><circle cx="14.5" cy="11" r="1" fill="currentColor"/></svg>'
      : '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/></svg>';
  }

  function grow(){ ta.style.height='auto'; ta.style.height=Math.min(ta.scrollHeight, 160)+'px'; }

  // ---- saisie ------------------------------------------------------------------
  function openInput(){
    if(!inStage()) return;
    if(inputOpen){ ta.focus(); return; }
    inputOpen=true; stage.classList.add('typing');
    inputBox.classList.remove('gulp'); inputBox.style.transform='';
    JeanAvatar&&JeanAvatar.listen();
    // Focus TOUT DE SUITE, dans le geste : Safari iOS n'ouvre le clavier que si
    // focus() est appelé pendant le toucher (pas dans une image suivante), sinon la
    // bulle s'affichait sans clavier et il fallait viser le champ ensuite.
    ta.focus({preventScroll:true});
    requestAnimationFrame(grow);
  }
  function closeInput(){ inputOpen=false; stage.classList.remove('typing'); ta.blur(); fitViewport(); }

  // Envoi : le texte part par le composeur normal (send), et la bulle file vers
  // Jean en rétrécissant, comme aspirée.
  function submit(){
    const text=ta.value.trim(); if(!text) return;
    const input=document.getElementById('input');
    input.value=text; ta.value=''; grow();
    const from=inputBox.getBoundingClientRect(), to=stage.querySelector('.jst-char-host').getBoundingClientRect();
    const dx=(to.left+to.width/2)-(from.left+from.width/2), dy=(to.top+to.height/2)-(from.top+from.height/2);
    inputBox.style.setProperty('--gx', dx+'px'); inputBox.style.setProperty('--gy', dy+'px');
    inputBox.classList.add('gulp');
    setTimeout(()=>{ JeanAvatar&&JeanAvatar.gulp(); }, 260);
    setTimeout(()=>{ inputOpen=false; stage.classList.remove('typing'); inputBox.classList.remove('gulp'); ta.blur(); }, 420);
    send();
  }

  // ---- reflet du fil : dernière réponse, activité -----------------------------
  // On ne parse pas le flux une seconde fois : on lit ce que le fil affiche déjà.
  function sync(){
    syncTimer=0;
    if(!stage) return;
    // Fil en cours de reconstruction (chargement, changement de conversation) ou
    // lecture d'une archive : les anciennes réponses défilaient dans la bulle. La
    // bulle attend le fil final (la minuterie de sync() rattrape juste après).
    const settled=!REPLAYING && !READING;
    const chat=document.getElementById('chat');
    // .typing = l'indicateur « en train d'écrire », pas une réponse.
    const msgs=chat ? chat.querySelectorAll(':scope > .msg.user, :scope > .msg.assistant:not(.typing)') : [];
    const lastMsg=msgs[msgs.length-1];
    const answer=lastMsg && lastMsg.classList.contains('assistant') ? lastMsg : null;
    const html=settled ? (answer ? (answer.querySelector('.body')||answer).innerHTML : '') : lastHTML;
    if(html!==lastHTML){
      const fresh=!lastHTML;
      const keep=new Map();
      for(const im of bubbleBody.querySelectorAll('img[src]')) if(im.complete && im.naturalWidth) keep.set(im.getAttribute('src'), im);
      lastHTML=html; bubbleBody.innerHTML=html;
      // Mots en cours d'apparition : la copie relance leur fondu. On recale le délai
      // sur leur heure d'apparition, sinon chaque mise à jour les faisait clignoter.
      const now=performance.now();
      for(const w of bubbleBody.querySelectorAll('span.wi[data-t]')) w.style.animationDelay=Math.round(+w.dataset.t-now)+'ms';
      adoptImages(keep);
      if(fresh){ userScrolled=false; bubbleBody.scrollTop=0; }
      else if(!userScrolled) bubbleBody.scrollTop=bubbleBody.scrollHeight;
    }
    stage.classList.toggle('has-answer', !!html.trim());
    if(settled) syncScreen(chat, answer);
    if(settled) syncTerm(chat, answer);
    // Un seul appel par passage : basculer faux puis vrai faisait clignoter Jean.
    const looking=stage.classList.contains('browsing') || stage.classList.contains('terminal') || stage.classList.contains('searching');
    if(looking!==avatarLooking){ avatarLooking=looking; JeanAvatar&&JeanAvatar.browsing(looking); }
    // Phrase d'activité : celle de la ligne du tour en cours (« Réfléchit… »).
    const act=chat && [...chat.querySelectorAll(':scope > .genstatus .jact')].pop();
    // Chargement : du modèle, ou de la conversation (le fil se reconstruit). Jean
    // le dit et patiente (yeux qui tournent, points autour de lui).
    const modelLoading=STATUS_SEEN && !MODEL_READY;
    const loading=modelLoading || !settled;
    // Message envoyé mais pas encore confirmé par le serveur (PENDS) : il travaille
    // déjà. Après un rafraîchissement, la confirmation met 2 à 3 s à arriver et
    // l'invite « Clique sur moi pour me parler » revenait entre-temps.
    const sent=PENDS.length>0;
    const working=settled && !loading && (busy && !!(ELAPSED || RUNNING_TASK) || sent);
    const hint=settled && !loading && !working && !html;
    // Sommeil : Jean range sa mémoire (consolidation). Endormi, « z » qui montent.
    const asleep=(JEAN_SLEEPING || Date.now()<JEAN_SLEEP_HOLD) && !working && !loading && !inputOpen;
    stage.classList.toggle('sleeping', asleep);
    JeanAvatar&&JeanAvatar.sleeping&&JeanAvatar.sleeping(asleep);
    if(Date.now()<JEAN_SLEEP_HOLD) setTimeout(schedule, 1000);
    const txt=loading ? t(modelLoading ? 'jean.stage_loading' : 'chat.loading_conversation') : working ? ((act && act.textContent) || (sent ? t('jean.act_read') : '')) : asleep ? t('jean.sleep_caption') : (hint ? t('jean.stage_hint') : '');
    stage.classList.toggle('loading', loading);
    stage.classList.toggle('idle-hint', hint);
    JeanAvatar&&JeanAvatar.loading(loading);
    // Étape précédente, en petit au-dessus de l'étape en cours : on voit le fil
    // du travail sans liste qui s'allonge. Seulement les étapes du tour en cours.
    let past='';
    if(working && chat){
      const steps=[...chat.querySelectorAll(':scope > .msg.user, :scope > .msg.tool.has-desc:not(.working)')];
      const last=steps[steps.length-1];
      if(last && !last.classList.contains('user')){ const l=last.querySelector('.label .txt')||last.querySelector('.label'); past=l ? l.textContent : ''; }
      if(past===txt) past='';
    }
    if(capPast.textContent!==past){
      capPast.textContent=past;
      capPast.classList.remove('in'); void capPast.offsetWidth; if(past) capPast.classList.add('in');
    }
    if(capText.textContent!==txt){
      capText.textContent=txt;
      capText.classList.remove('in'); void capText.offsetWidth; if(txt) capText.classList.add('in');
    }
    stage.classList.toggle('busy', working || loading);
    capStop.hidden=!working;
    if(working && !workStart) workStart=(ELAPSED && ELAPSED.start) || Date.now();
    if(!working) workStart=0;
    paintWorkTime();
  }
  // Écran : reflète la carte « aperçu du navigateur » du fil (09-stream.js), qui
  // n'existe que pendant la navigation ; nouvelle capture = fondu enchaîné. Il
  // n'est montré que si la DERNIÈRE chose faite est une action dans le navigateur :
  // dès que Jean écrit sa réponse après, la bulle prend la place (la carte descend
  // sous chaque nouvelle action, d'où la comparaison d'ordre dans le fil).
  function syncScreen(chat, answer){
    const cards=chat ? chat.querySelectorAll(':scope > .cu-shot') : [];
    const card=cards[cards.length-1];
    const frames=card ? card.querySelectorAll('img.cu-frame') : [];
    const frame=frames[frames.length-1];
    const answerAfter=!!(card && answer && (card.compareDocumentPosition(answer) & Node.DOCUMENT_POSITION_FOLLOWING));
    const on=!!(card && frame) && !answerAfter;
    stage.classList.toggle('browsing', on);
    if(!on){ screenSrc=''; return; }
    const url=(card.querySelector('.cu-shot-t')||{}).textContent||'';
    if(screenUrl.textContent!==url) screenUrl.textContent=url;
    if(frame.src===screenSrc) return;
    screenSrc=frame.src;
    const img=new Image(); img.alt=''; img.src=frame.src;
    screenView.appendChild(img);
    setTimeout(()=>img.classList.add('in'), 20);
    setTimeout(()=>{ let x=img.previousElementSibling; while(x){ const p=x.previousElementSibling; x.remove(); x=p; } }, 480);
  }
  // Terminal : les commandes bash du tour en cours, la dernière tapée lettre à
  // lettre, puis son résultat. Visible tant que la dernière action est une
  // commande et que la réponse n'a pas commencé ; le navigateur reste prioritaire.
  function syncTerm(chat, answer){
    const all=chat ? [...chat.querySelectorAll(':scope > .msg.user, :scope > .msg.tool')] : [];
    let from=0; for(let i=all.length-1;i>=0;i--){ if(all[i].classList.contains('user')){ from=i+1; break; } }
    const tools=all.slice(from);
    // Ouvert dès la première commande du tour et jusqu'à la réponse : le fermer à
    // chaque outil d'un autre type le faisait s'ouvrir et se refermer sans cesse.
    const runs=tools.filter(el=>el.dataset.tool==='bash' && el._tu).slice(-4);
    const last=runs[runs.length-1];
    // Terminal ou recherche : c'est le dernier des deux utilisé qui s'affiche.
    const panels=tools.filter(el=>(el.dataset.tool==='bash' || el.dataset.tool==='web_search') && el._tu);
    const lastPanel=panels[panels.length-1];
    syncSearch(lastPanel && lastPanel.dataset.tool==='web_search' ? lastPanel : null, answer);
    if(lastPanel && lastPanel!==last){ stage.classList.remove('terminal'); return; }
    const answerAfter=!!(last && answer && (last.compareDocumentPosition(answer) & Node.DOCUMENT_POSITION_FOLLOWING));
    const on=!!last && !answerAfter && !stage.classList.contains('browsing') && busy && !!(ELAPSED || RUNNING_TASK) && !viewOn('hide-term');
    stage.classList.toggle('terminal', on);
    if(!on){ return; }
    const cur=runs[runs.length-1]._tu;
    const cmd=String(cur.label||'');
    const key=runs.length+'|'+cmd;
    // La commande arrive par morceaux pendant que le modèle l'écrit : même commande
    // qui s'allonge = on continue de taper, sans repartir du début (le curseur
    // sautait au début à chaque morceau).
    if(key!==termKey){
      const prev=termKey.slice(termKey.indexOf('|')+1);
      const grows=termKey.split('|')[0]===String(runs.length) && cmd.startsWith(prev);
      termKey=key;
      if(!grows) termTyped=cur.done ? cmd.length : 0;
    }
    termRuns=runs; termCmd=cmd;
    paintTerm(runs, cmd);
    if(termTyped<cmd.length && !termTimer){
      // Lit toujours la commande la plus récente (termCmd) : une copie figée se
      // battait avec la version qui s'allonge et faisait sauter le curseur.
      termTimer=setInterval(()=>{
        const step=Math.max(1, Math.ceil(termCmd.length/45)); // ~1,5 s au plus pour taper
        termTyped=Math.min(termCmd.length, termTyped+step);
        paintTerm(termRuns, termCmd);
        if(termTyped>=termCmd.length){ clearInterval(termTimer); termTimer=0; }
      }, 33);
    }
  }
  function syncSearch(el, answer){
    const answerAfter=!!(el && answer && (el.compareDocumentPosition(answer) & Node.DOCUMENT_POSITION_FOLLOWING));
    const on=!!el && !answerAfter && !stage.classList.contains('browsing') && busy && !!(ELAPSED || RUNNING_TASK);
    stage.classList.toggle('searching', on);
    if(!on) return;
    const tu=el._tu, q=String(tu.label||'');
    const sig=q+'|'+(tu.done?1:0)+'|'+(tu.result||'').length;
    if(sig===srchSig) return;
    srchSig=sig;
    if(srchQ.textContent!==q) srchQ.textContent=q;
    const fresh=tu.done && !srchSeen.has(el); if(tu.done) srchSeen.add(el);
    const frag=document.createDocumentFragment();
    if(!tu.done){
      for(let i=0;i<3;i++){ const sk=$('div','jst-srch-sk', frag); $('i','',sk); $('i','',sk); $('i','',sk); }
    } else {
      const items=parseSearch(tu.result);
      if(!items.length){ const n=$('div','jst-srch-none', frag); n.textContent=t('jean.stage_no_results'); }
      items.slice(0,6).forEach((it,i)=>{
        const row=$('div','jst-srch-item'+(fresh?' in':''), frag); row.style.setProperty('--i', i);
        const src=$('div','jst-srch-src', row);
        const ic=$('span','jst-srch-ic', src); ic.textContent=(it.host[0]||'?').toUpperCase();
        $('span','', src).textContent=it.host;
        $('div','jst-srch-t', row).textContent=it.title;
        if(it.snip) $('div','jst-srch-s', row).textContent=it.snip;
      });
    }
    srchList.replaceChildren(frag);
    srchList.scrollTop=0;
  }
  // « 1. Titre / adresse / extrait » (toolWebSearch côté serveur).
  function parseSearch(txt){
    const out=[]; const lines=String(txt||'').split('\n');
    for(let i=0;i<lines.length;i++){
      const m=lines[i].match(/^\d+\.\s+(.+)$/); if(!m) continue;
      const url=(lines[i+1]||'').trim(), snip=(lines[i+2]||'').trim();
      let host=''; try{ host=new URL(url).hostname.replace(/^www\./,''); }catch(_){ continue; }
      out.push({title:m[1].trim(), host, snip: /^\d+\.\s/.test(snip) ? '' : snip});
    }
    return out;
  }
  function tail(s, n){ const l=String(s||'').replace(/\s+$/,'').split('\n'); return l.slice(-n).join('\n'); }
  function paintTerm(runs, cmd){
    // Rien n'a changé (cas le plus fréquent : le fil bouge ailleurs) : on ne touche
    // pas au DOM, sinon tout se reconstruit dix fois par seconde et clignote.
    const sig=termTyped+'|'+runs.map(el=>{ const tu=el._tu; return (tu.label||'').length+':'+(tu.done?1:0)+':'+(tu.result||'').length; }).join(',');
    if(sig===termSig) return;
    termSig=sig;
    const frag=document.createDocumentFragment();
    runs.forEach((el,i)=>{
      const tu=el._tu, isLast=i===runs.length-1;
      const line=document.createElement('div'); line.className='jt-cmd';
      const ps=document.createElement('span'); ps.className='jt-ps'; ps.textContent='$ ';
      line.appendChild(ps);
      line.appendChild(document.createTextNode(isLast ? cmd.slice(0, termTyped) : String(tu.label||'')));
      const typing=isLast && termTyped<cmd.length;
      const waiting=isLast && !typing && !tu.done;
      if(typing || waiting){ const c=document.createElement('span'); c.className='jt-caret'; line.appendChild(c); }
      frag.appendChild(line);
      if(tu.done && !(isLast && typing) && tu.result){
        const fresh=!termSeen.has(el); termSeen.add(el);
        const out=document.createElement('div'); out.className='jt-out'+(fresh?' in':'');
        out.textContent=tail(tu.result, isLast ? 40 : 6);
        frag.appendChild(out);
      }
    });
    termView.replaceChildren(frag);
    termView.scrollTop=termView.scrollHeight;
  }
  // Images de la bulle : innerHTML recopie le fil, sans les écouteurs (clic vers
  // la visionneuse) et en recréant chaque <img> à chaque mise à jour (rechargement,
  // image cassée un instant). On garde les images déjà chargées, une image encore
  // en route (sans src) devient un emplacement animé, et l'arrivée se fait en fondu.
  function adoptImages(keep){
    for(const img of [...bubbleBody.querySelectorAll('img')]){
      const src=img.getAttribute('src');
      if(!src){ const ph=document.createElement('span'); ph.className='jimg-ph'; img.replaceWith(ph); continue; }
      const old=keep.get(src);
      if(old){ keep.delete(src); img.replaceWith(old); continue; }
      img.classList.add('chat-img','jimg');
      img.onclick=e=>{ e.stopPropagation(); if(img.naturalWidth && typeof openLightbox==='function') openLightbox(img); };
      img.addEventListener('error', ()=>{ img.style.display='none'; }, {once:true});
      if(img.complete && img.naturalWidth) img.classList.add('jimg-in');
      else img.addEventListener('load', ()=>img.classList.add('jimg-in'), {once:true});
    }
  }
  const schedule=()=>{ if(!syncTimer) syncTimer=setTimeout(sync, 90); };

  // ---- fil complet à la demande -----------------------------------------------
  function toggleHistory(){
    const on=!document.body.classList.contains('jean-history');
    // Le fil est mis en bas AVANT de basculer : Jean y glisse vers la dernière
    // ligne, qui doit déjà être à l'écran pour que son trajet tombe juste.
    if(on){ closeInput(); const c=document.getElementById('chat'); if(c){ stickyBottom=true; c.scrollTop=c.scrollHeight; } }
    document.body.classList.toggle('jean-history', on);
    // Choix retenu sur cet appareil : qui préfère le fil classique le retrouve
    // à chaque ouverture du mode Jean, rechargement compris.
    try{ localStorage.setItem('ajean-jean-view', on ? 'chat' : 'stage'); }catch(_){}
    applyTexts();
  }
  const prefersChat=()=>{ try{ return localStorage.getItem('ajean-jean-view')==='chat'; }catch(_){ return false; } };

  // Clavier virtuel (iPhone) : il réduit la zone visible sans changer la hauteur de
  // la page. La scène se cale alors exactement sur la zone visible au-dessus du
  // clavier (classe .kb) : sans ça, le clavier recouvrait la saisie, puis Safari
  // faisait défiler toute la page et la scène se retrouvait décalée.
  function fitViewport(){
    const vv=window.visualViewport; if(!vv || !stage) return;
    const kb=window.innerHeight - vv.height > 120 && inStage() && inputOpen;
    stage.classList.toggle('kb', kb);
    if(kb){
      stage.style.setProperty('--vv-top', vv.offsetTop+'px');
      stage.style.setProperty('--vv-h', vv.height+'px');
      if(window.scrollY) window.scrollTo(0, 0);
    }
  }

  function init(){
    if(!build()) return;
    if(window.visualViewport){
      visualViewport.addEventListener('resize', fitViewport);
      visualViewport.addEventListener('scroll', fitViewport);
    }
    ta.addEventListener('focus', ()=>setTimeout(fitViewport, 50));
    ta.addEventListener('blur', ()=>setTimeout(fitViewport, 50));
    const chat=document.getElementById('chat');
    if(chat) new MutationObserver(schedule).observe(chat, {childList:true, subtree:true, characterData:true});
    // Mode Jean quitté : la classe part ; à l'entrée, elle revient si l'utilisateur
    // a choisi le fil classique (prefersChat).
    // ⚠️ Ne toucher à la classe que si elle doit changer : modifier l'attribut
    // relance cet observateur, à l'infini si on le faisait à chaque passage.
    new MutationObserver(()=>{
      const b=document.body.classList;
      if(!b.contains('jean-mode')){ if(b.contains('jean-history')) b.remove('jean-history'); if(inputOpen) closeInput(); }
      else if(!b.contains('jean-history') && prefersChat() && !jeanWasOn){ jeanWasOn=true; b.add('jean-history'); return; }
      jeanWasOn=b.contains('jean-mode');
      if(inStage() && document.activeElement && document.activeElement.id==='input') document.activeElement.blur();
      applyTexts(); schedule();
    })
      .observe(document.body, {attributes:true, attributeFilter:['class']});
    // Page chargée directement en mode Jean : même règle.
    if(document.body.classList.contains('jean-mode')){ jeanWasOn=true; if(prefersChat()) document.body.classList.add('jean-history'); }
    // Taper n'importe où ouvre la bulle de saisie (la première lettre y entre).
    document.addEventListener('keydown', e=>{
      if(!inStage() || inputOpen || e.ctrlKey || e.metaKey || e.altKey || e.key.length!==1) return;
      const a=document.activeElement;
      // Seul un champ VISIBLE garde la frappe : la recherche de l'historique, masquée
      // en mode Jean, pouvait garder le focus et empêcher la bulle de s'ouvrir.
      const shown=el=>!!el && el.getClientRects().length>0;
      if(a && a.id!=='input' && (a.tagName==='INPUT' || a.tagName==='TEXTAREA' || a.isContentEditable) && shown(a)) return;
      if([...document.querySelectorAll('.modal-ov.show,.ask-overlay.show')].some(shown)) return;
      if(a && a!==document.body && !shown(a)) a.blur();
      openInput();
    });
    setInterval(()=>{ if(inStage()) sync(); }, 500); // occupé/libre change aussi sans mutation du fil
    if(inStage() && document.activeElement && document.activeElement.id==='input') document.activeElement.blur();
    sync();
  }
  if(document.readyState==='loading') document.addEventListener('DOMContentLoaded', init); else init();
})();
