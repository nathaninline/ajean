// ===== Mode vocal de Jean ====================================================
// Un bouton micro sur la scène de Jean ouvre une conversation à voix haute, mains
// libres : on parle, une courte pause suffit pour envoyer, Jean répond et sa
// réponse est lue dès la première phrase écrite (sans attendre la fin). Parler
// pendant qu'il parle le coupe : il se tait et écoute.
//
// Rien de nouveau côté conversation : le texte dicté part par send() (09-stream.js)
// avec window.VOICE_SEND, donc mémoire, outils et fil restent ceux de Jean. La
// transcription et la voix passent par /api/voice/* (voice.go), relayés vers le
// serveur audio.cpp de la machine. La réponse lue est celle que le fil affiche.
(()=>{
  let enabled=false, on=false, btn=null, pill=null, sens=2, silenceMs=800;
  let ctx=null, stream=null, srcNode=null, proc=null;
  // listen = attend la parole ; rec = enregistre ; stt = transcription ;
  // wait = Jean réfléchit ; speak = Jean parle.
  let state='off';
  let floor=0.006, pre=[], frames=[], loudMs=0, speechMs=0, silMs=0, recMs=0, lastNudge=0;
  let turn=null, queue=[], playing=false, gen=0, tickTimer=0, deafUntil=0;
  const player=new Audio(); player.playsInline=true; player.preload='auto';
  const SILENT='data:audio/wav;base64,UklGRiQAAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQAAAAA=';

  const MIC='<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><rect x="9" y="3" width="6" height="11" rx="3"/><path d="M5 11a7 7 0 0 0 14 0M12 18v3"/></svg>';

  // ---- interface ------------------------------------------------------------
  function build(){
    const main=document.querySelector('.main'); if(!main) return;
    btn=document.createElement('button'); btn.type='button'; btn.className='jst-voice'; btn.innerHTML=MIC;
    btn.onclick=()=>on ? stop() : start();
    main.appendChild(btn);
    pill=document.createElement('div'); pill.className='jst-voice-st';
    pill.onclick=interrupt;
    main.appendChild(pill);
    // Capture : le toucher qui interrompt Jean n'ouvre pas en plus la saisie.
    document.addEventListener('pointerdown', e=>{
      if(!on || !document.body.classList.contains('jean-mode') || (btn && btn.contains(e.target))) return;
      if(interrupt()){ e.preventDefault(); e.stopPropagation(); }
    }, true);
    texts();
  }
  function texts(){ if(btn) btn.title=btn.ariaLabel=t(on ? 'jean.voice_off' : 'jean.voice_on'); }
  function setState(s){
    state=s;
    document.body.classList.toggle('jean-voice', on);
    if(btn){ btn.classList.toggle('on', on); btn.dataset.st=on ? s : ''; }
    const msg={listen:'jean.voice_listen', rec:'jean.voice_rec', stt:'jean.voice_stt', speak:'jean.voice_speak'}[s];
    if(pill){ pill.textContent=on && msg ? t(msg) : ''; pill.classList.toggle('in', !!(on && msg)); }
  }

  // ---- micro ----------------------------------------------------------------
  async function start(){
    if(on) return;
    // Dans le geste (iOS) : contexte audio et lecteur débloqués avant tout await.
    try{ ctx=new (window.AudioContext||window.webkitAudioContext)(); }catch(_){ toast(t('jean.voice_unsupported')); return; }
    ctx.resume().catch(()=>{});
    player.src=SILENT; player.play().catch(()=>{});
    try{
      stream=await navigator.mediaDevices.getUserMedia({audio:{echoCancellation:true, noiseSuppression:true, autoGainControl:true}});
    }catch(e){ try{ ctx.close(); }catch(_){} ctx=null; toast(t('jean.voice_mic_denied')); return; }
    srcNode=ctx.createMediaStreamSource(stream);
    proc=ctx.createScriptProcessor(2048, 1, 1);
    proc.onaudioprocess=onAudio;
    srcNode.connect(proc); proc.connect(ctx.destination); // sans sortie, onaudioprocess ne tourne pas
    on=true; pre=[]; frames=[]; loudMs=0; floor=0.006;
    texts(); setState('listen');
    tickTimer=setInterval(tick, 200);
  }
  function stop(){
    if(!on) return;
    on=false; stopSpeech();
    clearInterval(tickTimer); tickTimer=0;
    try{ proc&&proc.disconnect(); srcNode&&srcNode.disconnect(); }catch(_){}
    if(stream) stream.getTracks().forEach(tr=>tr.stop());
    try{ ctx&&ctx.close(); }catch(_){}
    ctx=stream=srcNode=proc=null; turn=null;
    texts(); setState('off');
  }

  const sumMs=a=>a.reduce((n,f)=>n+f.length, 0)/(ctx ? ctx.sampleRate : 48000)*1000;
  function onAudio(e){
    if(!on) return;
    const x=e.inputBuffer.getChannelData(0);
    const f=new Float32Array(x);
    let s=0; for(let i=0;i<f.length;i++) s+=f[i]*f[i];
    const rms=Math.sqrt(s/f.length), ms=f.length/ctx.sampleRate*1000;
    // Jean parle (ou vient de parler) : le micro capte sa propre voix par le
    // haut-parleur, l'anti-écho du navigateur ne la retire pas. On n'écoute pas :
    // pour l'interrompre, on touche l'écran (interrupt).
    if(state==='speak' || playing || Date.now()<deafUntil){ pre=[]; loudMs=0; return; }
    // Sensibilité réglable (Paramètres > Voix) : faible = il faut parler plus fort.
    const thr=sens===1 ? Math.max(floor*4, 0.022) : sens===3 ? Math.max(floor*2.2, 0.009) : Math.max(floor*3, 0.014);
    const loud=rms>thr;
    if(state==='listen' || state==='wait'){
      if(!loud && state==='listen') floor=Math.min(0.03, Math.max(0.002, floor*0.97+rms*0.03));
      pre.push(f); while(pre.length>1 && sumMs(pre)>450) pre.shift();
      if(loud){ loudMs+=ms; if(loudMs>=140) beginRec(); }
      else loudMs=Math.max(0, loudMs-ms/2);
    } else if(state==='rec'){
      frames.push(f); recMs+=ms;
      if(loud){ speechMs+=ms; silMs=0; } else silMs+=ms;
      if(Date.now()-lastNudge>400 && loud){ lastNudge=Date.now(); window.JeanAvatar&&JeanAvatar.listen(); }
      if(silMs>=silenceMs || recMs>60000) endRec();
    }
  }
  function beginRec(){
    frames=pre; pre=[]; recMs=sumMs(frames); speechMs=loudMs; silMs=0; loudMs=0;
    setState('rec');
  }
  async function endRec(){
    const fr=frames; frames=[];
    if(speechMs<350){ setState(turn ? 'wait' : 'listen'); return; }
    setState('stt');
    const wav=encodeWav(fr, ctx.sampleRate);
    let text='';
    try{
      const r=await jfetch('/api/voice/stt', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({wav})});
      const j=await r.json(); if(!r.ok) throw new Error(j.error||'');
      text=(j.text||'').trim();
    }catch(e){ toast(t('jean.voice_stt_error')); }
    if(!on) return;
    if(!text || !/[\p{L}\d]/u.test(text)){ setState(turn ? 'wait' : 'listen'); return; }
    say(text);
  }
  // 16 kHz mono, 16 bits : ce qu'attend la transcription, et 3x plus léger.
  function encodeWav(fr, rate){
    const n=fr.reduce((a,f)=>a+f.length, 0), all=new Float32Array(n);
    let o=0; for(const f of fr){ all.set(f, o); o+=f.length; }
    const ratio=rate/16000, len=Math.floor(n/ratio), pcm=new Int16Array(len);
    for(let i=0;i<len;i++){
      const a=Math.floor(i*ratio), b=Math.min(n, Math.floor((i+1)*ratio));
      let s=0; for(let j=a;j<b;j++) s+=all[j];
      const v=Math.max(-1, Math.min(1, s/Math.max(1, b-a)));
      pcm[i]=v<0 ? v*0x8000 : v*0x7fff;
    }
    const buf=new ArrayBuffer(44+pcm.length*2), dv=new DataView(buf);
    const w=(p, s)=>{ for(let i=0;i<s.length;i++) dv.setUint8(p+i, s.charCodeAt(i)); };
    w(0,'RIFF'); dv.setUint32(4, 36+pcm.length*2, true); w(8,'WAVE'); w(12,'fmt ');
    dv.setUint32(16,16,true); dv.setUint16(20,1,true); dv.setUint16(22,1,true);
    dv.setUint32(24,16000,true); dv.setUint32(28,32000,true); dv.setUint16(32,2,true); dv.setUint16(34,16,true);
    w(36,'data'); dv.setUint32(40, pcm.length*2, true);
    new Int16Array(buf, 44).set(pcm);
    const bytes=new Uint8Array(buf); let bin='';
    for(let i=0;i<bytes.length;i+=0x8000) bin+=String.fromCharCode.apply(null, bytes.subarray(i, i+0x8000));
    return btoa(bin);
  }

  // ---- envoi et lecture de la réponse ---------------------------------------
  function say(text){
    const input=document.getElementById('input'); if(!input) return;
    input.value=text;
    window.VOICE_SEND=true;
    stopSpeech();
    turn={spoken:0, at:Date.now()};
    send();
    setState('wait');
  }
  // Ce qui se dit du tour : dans l'ordre du fil, les phrases d'outil (« Je cherche
  // la météo… », la description que Jean donne à chaque appel) et le texte de ses
  // bulles, sans code, tableaux ni images. Une phrase d'outil est complète dès
  // qu'elle s'affiche ; le texte, lui, s'écrit encore.
  function segments(){
    const chat=document.getElementById('chat'); if(!chat) return [];
    const all=[...chat.querySelectorAll(':scope > .msg.user, :scope > .msg.assistant:not(.typing), :scope > .msg.tool')];
    let i=all.length-1; while(i>=0 && !all[i].classList.contains('user')) i--;
    const out=[];
    for(const el of all.slice(i+1)){
      if(el.classList.contains('tool')){
        const tu=el._tu;
        if(tu && tu.desc && !tu.typing) out.push({tool:true, text:String(tu.desc).trim()});
        continue;
      }
      const b=(el.querySelector('.body')||el).cloneNode(true);
      b.querySelectorAll('pre,table,img,.katex,details,.jimg-ph,button').forEach(n=>n.remove());
      b.querySelectorAll('p,li,h1,h2,h3,h4,h5,h6,br,blockquote').forEach(n=>n.append('\n'));
      out.push({tool:false, text:b.textContent||''});
    }
    return out;
  }
  // Découpe en phrases sur un texte aplati : le fil se reformate pendant l'écriture
  // (Markdown, paragraphes), donc on compte des PHRASES lues, pas une position dans
  // le texte (une position décalée faisait sauter des morceaux de phrase).
  function sentencesOf(full){
    const flat=full.replace(/\s+/g, ' ').trim();
    const done=[]; const re=/[^.!?…]+?(?:[.!?…]+["»”)]*)(?=\s|$)/g; let m, end=0;
    while((m=re.exec(flat))){ done.push(m[0].trim()); end=re.lastIndex; }
    return {done, rest:flat.slice(end).trim()};
  }
  function tick(){
    if(!on || !turn) return;
    const done=!busy && !(typeof PENDS!=="undefined" && PENDS.length) && Date.now()-turn.at>1500;
    const segs=segments();
    // Liste des choses prêtes à dire. Le dernier bloc de texte en cours d'écriture
    // garde sa dernière phrase en réserve (« 3. », « M. » peuvent encore s'allonger).
    const ready=[];
    segs.forEach((sg, k)=>{
      if(sg.tool){ if(sg.text) ready.push({tool:true, text:sg.text}); return; }
      const sp=sentencesOf(sg.text), last=k===segs.length-1;
      const parts=last && !done ? sp.done.slice(0, -1) : sp.done.concat(sp.rest ? [sp.rest] : []);
      for(const x of parts) ready.push({tool:false, text:x});
    });
    if(ready.length>turn.spoken){
      let text='';
      for(const it of ready.slice(turn.spoken)){
        if(it.tool){ if(text){ enqueue(text); text=''; } enqueue(it.text, true); }
        else text+=(text ? ' ' : '')+it.text;
      }
      if(text) enqueue(text);
      turn.spoken=ready.length;
    }
    if(done && turn.spoken>=ready.length && !queue.length && !playing){
      turn=null; if(state==='wait' || state==='speak') setState('listen');
    }
  }
  function enqueue(txt, tool){
    let clean=txt.replace(/https?:\/\/\S+/g, '').replace(/[*_#`>|~\[\]]/g, '').replace(/\s+/g, ' ').trim();
    if(!/[\p{L}\d]/u.test(clean)) return;
    if(tool){
      if(!/[.!?…]$/.test(clean)) clean+='.';
      // Outils enchaînés plus vite qu'il ne parle : on ne garde que le plus récent
      // pas encore lu (sinon il raconte avec un temps de retard).
      queue=queue.filter(it=>!it.tool || it.cur);
      queue.push({text:clean, p:null, tool:true});
      pump(); return;
    }
    // Morceaux de taille raisonnable : la première phrase part vite, les suivantes
    // se regroupent (moins d'appels, intonation plus naturelle).
    const last=queue[queue.length-1];
    if(last && !last.p && !last.tool && (last.text+' '+clean).length<220) last.text+=' '+clean;
    else queue.push({text:clean, p:null});
    pump();
  }
  async function fetchTts(text, g){
    try{
      const r=await jfetch('/api/voice/tts', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({text})});
      const j=await r.json(); if(!r.ok || !j.wav || g!==gen) return null;
      const bin=atob(j.wav), u8=new Uint8Array(bin.length);
      for(let i=0;i<bin.length;i++) u8[i]=bin.charCodeAt(i);
      return URL.createObjectURL(new Blob([u8], {type:'audio/wav'}));
    }catch(_){ return null; }
  }
  async function pump(){
    const g=gen;
    // Le morceau suivant se prépare pendant que le précédent est lu.
    for(const it of queue.slice(0, 2)) if(!it.p) it.p=fetchTts(it.text, g);
    if(playing || !queue.length) return;
    playing=true;
    const item=queue[0]; item.cur=true;
    const url=await item.p;
    if(g!==gen) return;
    queue=queue.filter(x=>x!==item);
    if(url){
      setState('speak');
      await new Promise(res=>{ player.onended=res; player.onerror=res; player.src=url; player.play().catch(res); });
      URL.revokeObjectURL(url);
      deafUntil=Date.now()+700;
    }
    if(g!==gen) return;
    playing=false;
    if(queue.length) pump();
    else if(state==='speak') setState(turn ? 'wait' : 'listen');
  }
  // Toucher l'écran pendant qu'il parle : il se tait (et arrête d'écrire).
  function interrupt(){
    if(!on || !(state==='speak' || playing)) return false;
    stopSpeech(); turn=null; if(busy) stopGen();
    deafUntil=Date.now()+300; setState('listen');
    return true;
  }
  function stopSpeech(){
    gen++; queue=[]; playing=false; deafUntil=Date.now()+700;
    try{ player.pause(); player.removeAttribute('src'); player.load(); }catch(_){}
  }

  async function refresh(){
    try{
      const r=await jfetch('/api/voice/status'); const j=await r.json();
      enabled=!!j.enabled; sens=j.sens||2; silenceMs=j.silence_ms||800;
    }catch(_){ enabled=false; }
    document.body.classList.toggle('voice-ready', enabled);
    if(!enabled && on) stop();
  }
  // ---- démarrage --------------------------------------------------------------
  async function init(){
    build();
    await refresh();
    // Réglages modifiés (25-voice-settings.js) : pris en compte sans recharger.
    window.addEventListener('voice-settings', refresh);
    // Mode Jean quitté : la conversation vocale s'arrête (micro rendu).
    new MutationObserver(()=>{ if(on && !document.body.classList.contains('jean-mode')) stop(); texts(); })
      .observe(document.body, {attributes:true, attributeFilter:['class']});
  }
  if(document.readyState==='loading') document.addEventListener('DOMContentLoaded', init); else init();
})();
