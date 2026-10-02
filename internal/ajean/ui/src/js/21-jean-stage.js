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
  let stage, scene, bubble, bubbleBody, caption, capText, capStop, inputBox, ta, sendBtn, histBtn;
  let screen, screenView, screenUrl, screenSrc='';
  let inputOpen=false, syncTimer=0, lastHTML='', userScrolled=false;

  const $=(tag, cls, parent)=>{ const n=document.createElement(tag); if(cls) n.className=cls; if(parent) parent.appendChild(n); return n; };
  const inStage=()=>document.body.classList.contains('jean-mode') && !document.body.classList.contains('jean-history');

  function build(){
    const main=document.querySelector('.main'); if(!main) return false;
    stage=$('div','', main); stage.id='jean-stage';
    histBtn=$('button','jst-hist', main); histBtn.type='button';
    histBtn.onclick=toggleHistory;
    scene=$('div','jst-scene', stage);
    bubble=$('div','jst-bubble', scene);
    bubbleBody=$('div','jst-bubble-body body', bubble);
    bubbleBody.addEventListener('scroll', ()=>{ userScrolled = bubbleBody.scrollHeight-bubbleBody.scrollTop-bubbleBody.clientHeight > 30; }, {passive:true});
    // Écran du navigateur piloté : la page qu'il consulte, en direct.
    screen=$('div','jst-screen', scene);
    screenUrl=$('div','jst-screen-url', screen);
    screenView=$('div','jst-screen-view', screen);
    const charWrap=$('div','jst-char', scene);
    const orbit=$('div','jst-orbit', charWrap);
    for(let i=0;i<3;i++) $('span','', orbit).style.setProperty('--i', i);
    const charHost=$('div','jst-char-host', charWrap);
    caption=$('div','jst-caption', scene);
    capText=$('span','jst-cap-text', caption);
    capStop=$('button','jst-cap-stop', caption); capStop.type='button'; capStop.hidden=true; capStop.onclick=()=>stopGen();
    inputBox=$('div','jst-input', scene);
    ta=$('textarea','', inputBox); ta.rows=1;
    ta.setAttribute('autocomplete','off'); ta.setAttribute('name','jean-message');
    ta.setAttribute('enterkeyhint','send'); ta.setAttribute('aria-label','message');
    sendBtn=$('button','jst-send', inputBox); sendBtn.type='button';
    sendBtn.innerHTML='<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5M5 12l7-7 7 7"/></svg>';
    sendBtn.onclick=submit;
    // Toucher n'importe où sur la scène ouvre la saisie avec le clavier (focus donné
    // dans le geste, voir openInput). Sauf la bulle de réponse (on doit pouvoir la
    // faire défiler ou y sélectionner du texte) et l'écran du navigateur.
    scene.addEventListener('click', e=>{
      if(sendBtn.contains(e.target) || bubble.contains(e.target) || screen.contains(e.target) || capStop.contains(e.target)) return;
      if(document.activeElement===ta) return;
      openInput();
    });
    // Clavier refermé sans rien écrit : la bulle de saisie se replie aussi, plutôt
    // que de rester affichée à attendre un toucher précis.
    ta.addEventListener('blur', ()=>setTimeout(()=>{ if(document.activeElement!==ta && !ta.value.trim() && inputOpen && !inputBox.classList.contains('gulp')) closeInput(); }, 200));
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
    capStop.textContent=t('jean.stage_stop');
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
    setTimeout(()=>{ inputOpen=false; stage.classList.remove('typing'); inputBox.classList.remove('gulp'); }, 420);
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
      lastHTML=html; bubbleBody.innerHTML=html;
      if(fresh){ userScrolled=false; bubbleBody.scrollTop=0; }
      else if(!userScrolled) bubbleBody.scrollTop=bubbleBody.scrollHeight;
    }
    stage.classList.toggle('has-answer', !!html.trim());
    if(settled) syncScreen(chat, answer);
    // Phrase d'activité : celle de la ligne du tour en cours (« Réfléchit… »).
    const act=chat && [...chat.querySelectorAll(':scope > .genstatus .jact')].pop();
    // Chargement : du modèle, ou de la conversation (le fil se reconstruit). Jean
    // le dit et patiente (yeux qui tournent, points autour de lui).
    const modelLoading=STATUS_SEEN && !MODEL_READY;
    const loading=modelLoading || !settled;
    const working=settled && !loading && busy && !!(ELAPSED || RUNNING_TASK);
    const hint=settled && !loading && !working && !html;
    const txt=loading ? t(modelLoading ? 'jean.stage_loading' : 'chat.loading_conversation') : working ? ((act && act.textContent) || '') : (hint ? t('jean.stage_hint') : '');
    stage.classList.toggle('loading', loading);
    stage.classList.toggle('idle-hint', hint);
    JeanAvatar&&JeanAvatar.loading(loading);
    if(capText.textContent!==txt){
      capText.textContent=txt;
      capText.classList.remove('in'); void capText.offsetWidth; if(txt) capText.classList.add('in');
    }
    stage.classList.toggle('busy', working || loading);
    capStop.hidden=!working;
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
    JeanAvatar&&JeanAvatar.browsing(on);
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
  const schedule=()=>{ if(!syncTimer) syncTimer=setTimeout(sync, 90); };

  // ---- fil complet à la demande -----------------------------------------------
  function toggleHistory(){
    const on=!document.body.classList.contains('jean-history');
    // Le fil est mis en bas AVANT de basculer : Jean y glisse vers la dernière
    // ligne, qui doit déjà être à l'écran pour que son trajet tombe juste.
    if(on){ closeInput(); const c=document.getElementById('chat'); if(c){ stickyBottom=true; c.scrollTop=c.scrollHeight; } }
    document.body.classList.toggle('jean-history', on);
    applyTexts();
  }

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
    // Mode Jean quitté : on repart de la scène la prochaine fois.
    // ⚠️ Ne toucher à la classe que si elle est là : retirer une classe absente
    // modifie quand même l'attribut, ce qui relançait cet observateur à l'infini.
    new MutationObserver(()=>{
      const b=document.body.classList;
      if(!b.contains('jean-mode')){ if(b.contains('jean-history')) b.remove('jean-history'); if(inputOpen) closeInput(); }
      if(inStage() && document.activeElement && document.activeElement.id==='input') document.activeElement.blur();
      applyTexts(); schedule();
    })
      .observe(document.body, {attributes:true, attributeFilter:['class']});
    // Taper n'importe où ouvre la bulle de saisie (la première lettre y entre).
    document.addEventListener('keydown', e=>{
      if(!inStage() || inputOpen || e.ctrlKey || e.metaKey || e.altKey || e.key.length!==1) return;
      const a=document.activeElement;
      if(a && a.id!=='input' && (a.tagName==='INPUT' || a.tagName==='TEXTAREA' || a.isContentEditable)) return;
      if(document.querySelector('.modal-ov.show')) return;
      openInput();
    });
    setInterval(()=>{ if(inStage()) sync(); }, 500); // occupé/libre change aussi sans mutation du fil
    if(inStage() && document.activeElement && document.activeElement.id==='input') document.activeElement.blur();
    sync();
  }
  if(document.readyState==='loading') document.addEventListener('DOMContentLoaded', init); else init();
})();
