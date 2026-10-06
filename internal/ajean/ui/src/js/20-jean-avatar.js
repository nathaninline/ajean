// ===== Avatar de Jean ========================================================
// Jean en personne : un rond, de petites lunettes rondes et une fine moustache.
// Mode Jean uniquement. Trois places (voir placeNow) :
//   - la scène plein écran (21-jean-stage.js), en grand : sa place habituelle ;
//   - le fil complet (bouton historique) : à la place du logo « J » de la dernière
//     ligne d'activité, à gauche de ce qu'il fait ; il y reste entre deux tours ;
//   - fil vide dans le fil complet : en grand au centre de l'accueil.
// Entre la scène et le fil, il vole de l'une à l'autre (glide).
// Monochrome comme l'app : rond à la couleur du texte, yeux à la couleur du fond,
// donc il s'inverse tout seul entre thème clair et sombre.
//
// L'expression passe par les yeux derrière les verres (regard, forme), la
// moustache (qui frémit quand il parle) et le mouvement du rond.
// Animation : une boucle requestAnimationFrame et des RESSORTS amortis : chaque
// grandeur suit sa cible avec un peu d'inertie, d'où un mouvement souple. La
// boucle ne tourne que lorsqu'il est visible.
//
// États : idle, listen (tu écris), think, search (mémoire), web, work (machine,
// tâches), talk (il répond), happy (réponse finie), sleep, plus deux surcouches :
// browse (devant l'écran du navigateur) et loading (modèle ou fil en chargement).
// Branché sur le flux (09-stream.js : act, speak, done, attach, detach) et sur la
// scène (21-jean-stage.js : mount, listen, gulp, browsing, loading).
// `var` et non `const` : le code du fil (09-stream.js), assemblé AVANT ce fichier
// dans le même script, peut l'appeler tôt ; il voit alors undefined, pas une erreur.
var JeanAvatar = (()=>{
  const SVG_NS='http://www.w3.org/2000/svg';
  const reduced=matchMedia('(prefers-reduced-motion: reduce)');
  let root=null, wrap=null, P=null, raf=0, last=0, t=0;
  let host=null;                                      // ligne d'activité du tour en cours
  let state='idle', stateUntil=0, lastActivity=performance.now();
  let blinkAt=0, blinkT=1, hopT=1, pulse=0, pointer=null, browsing=false, loadingNow=false;

  // Ressort amorti : x tend vers target avec raideur k et amortissement d.
  const spring=(k,d)=>({x:0,v:0,target:0,k,d});
  const S={ lookX:spring(90,13), lookY:spring(90,13), squash:spring(220,12),
    open:spring(170,16), happy:spring(120,14), tilt:spring(70,11) };
  S.open.x=S.open.target=1;
  const step=(s,dt)=>{ s.v+=(s.k*(s.target-s.x)-s.d*s.v)*dt; s.x+=s.v*dt; };
  const clamp=(v,a,b)=>Math.max(a,Math.min(b,v));
  const el=(name, attrs, parent)=>{ const n=document.createElementNS(SVG_NS,name); for(const k in attrs) n.setAttribute(k, attrs[k]); if(parent) parent.appendChild(n); return n; };

  function build(){
    wrap=document.createElement('span'); wrap.id='jean-avatar'; wrap.setAttribute('aria-hidden','true');
    const svg=el('svg',{viewBox:'0 0 100 100',class:'jav-svg'});
    const body=el('g',{},svg);
    el('circle',{cx:50,cy:50,r:44,class:'jav-body'},body);
    const face=el('g',{},body);                       // suit un peu le regard
    const eyes=[35,65].map(cx=>({cx,
      open:el('ellipse',{cx,cy:44,rx:3.4,ry:4.6,class:'jav-eye'},face),
      happy:el('path',{d:'',class:'jav-line'},face)}));
    // Lunettes : deux verres ronds et un pont, en trait fin.
    el('circle',{cx:35,cy:44,r:11.5,class:'jav-frame'},face);
    el('circle',{cx:65,cy:44,r:11.5,class:'jav-frame'},face);
    el('path',{d:'M46.5 43 Q50 40.5 53.5 43',class:'jav-frame'},face);
    // Moustache : fine, en guidon discret.
    const stache=el('path',{class:'jav-stache',d:'M50 64 C45 60 37 60 32 66 C37 64.5 44 66 50 67.5 C56 66 63 64.5 68 66 C63 60 55 60 50 64 Z'},face);
    wrap.appendChild(svg);
    wrap.addEventListener('click', ()=>{ hopT=0; S.squash.v-=6; wake(); });
    P={body, face, eyes, stache};
  }

  function wake(){ lastActivity=performance.now(); if(state==='sleep'){ hopT=0.35; setState('idle'); } }
  function setState(s, ms){
    if(s===state && !ms) return;
    state=s; stateUntil=ms ? performance.now()+ms : 0;
    if(s!=='sleep') lastActivity=performance.now();
  }
  // Un morceau de texte arrive : le rond pulse un peu, puis se calme.
  function speak(n){ pulse=Math.min(1, pulse+0.12*(n||1)); if(state!=='talk') setState('talk'); }

  // Cibles des ressorts selon l'état.
  function targets(now){
    let lx=0, ly=0, open=1, happy=0, tilt=0;
    // Devant son écran (navigation) : il le regarde et lit, quel que soit l'état.
    const st = loadingNow ? 'loading' : browsing && state!=='happy' && state!=='sleep' ? 'browse' : state;
    switch(st){
      // Modèle en cours de chargement : les yeux tournent lentement, comme une roue.
      case 'loading': lx=Math.cos(now/520)*0.85; ly=Math.sin(now/520)*0.85; open=0.9; break;
      case 'browse': lx=0.55*Math.sin(now/650); ly=-0.85+0.12*Math.sin(now/1300); open=0.95; tilt=-0.05; break;
      case 'listen': ly=0.9; break;
      case 'think': lx=0.65+0.15*Math.sin(now/900); ly=-0.9; open=0.85; tilt=-0.12; break;
      case 'search': lx=Math.sin(now/260); ly=0.2; open=0.9; break;
      case 'web': lx=Math.sin(now/340)*0.9; ly=-0.1+0.25*Math.sin(now/700); break;
      case 'work': lx=0.35*Math.sin(now/1300); ly=0.6; open=0.8; tilt=0.06; break;
      case 'talk': lx=0.15*Math.sin(now/1700); ly=0.1; break;
      case 'happy': happy=1; tilt=0.08*Math.sin(now/180); break;
      case 'sleep': open=0; ly=0.3; tilt=0.15; break;
      default: // idle : suit la souris, sinon regarde un peu autour de lui
        if(pointer && now-pointer.t<6000){ const a=wrap.getBoundingClientRect();
          lx=clamp((pointer.x-(a.left+a.width/2))/420,-1,1); ly=clamp((pointer.y-(a.top+a.height/2))/420,-1,1); }
        else { lx=0.5*Math.sin(now/2600); ly=0.25*Math.sin(now/3700); }
    }
    S.lookX.target=lx; S.lookY.target=ly; S.open.target=open; S.happy.target=happy; S.tilt.target=tilt;
  }

  function frame(now){
    raf=0;
    if(!visible()) return;
    const dt=Math.min(0.05, (now-(last||now))/1000); last=now; t+=dt;
    if(stateUntil && now>stateUntil){ stateUntil=0; setState('idle'); }
    if(state==='idle' && now-lastActivity>90000) setState('sleep');
    pulse*=Math.pow(0.03, dt);
    if(state==='talk' && pulse<0.03 && now-lastActivity>1200) setState('idle');
    targets(now);
    for(const k in S) step(S[k], dt);
    if(now>blinkAt){ blinkT=0; blinkAt=now+2200+Math.random()*3800; if(Math.random()<0.18) blinkAt=now+260; }
    blinkT=Math.min(1, blinkT+dt*7); hopT=Math.min(1, hopT+dt*2.2);
    paint();
    raf=requestAnimationFrame(frame);
  }

  function paint(){
    const still=reduced.matches;
    // Flottement + respiration, petit saut au clic, pulsation quand il parle.
    const bob=still ? 0 : Math.sin(t*2.1)*2.2;
    const hop=Math.sin(Math.PI*hopT)*(1-hopT)*16;
    const breath=still ? 0 : Math.sin(t*2.1+0.6)*0.012;
    const s=1+breath+pulse*0.05, sq=S.squash.x*0.06;
    const tilt=S.tilt.x*10 + (still ? 0 : S.lookX.v*0.3);
    P.body.setAttribute('transform', `translate(0 ${(-bob-hop).toFixed(2)}) translate(50 94) rotate(${tilt.toFixed(2)}) scale(${(s+sq).toFixed(4)} ${(s-sq).toFixed(4)}) translate(-50 -94)`);
    // Yeux : regard (déplacement), clignement et ouverture (hauteur), joie (« ^^ »).
    const blink=1-Math.sin(Math.PI*Math.min(1,blinkT));
    const happy=clamp(S.happy.x,0,1), open=clamp(S.open.x*blink*(1-happy),0,1);
    // Le visage (lunettes, moustache) suit un peu le regard ; les yeux bougent
    // davantage, mais restent derrière leurs verres.
    P.face.setAttribute('transform', `translate(${(S.lookX.x*3).toFixed(2)} ${(S.lookY.x*2).toFixed(2)})`);
    const dx=S.lookX.x*4.5, dy=S.lookY.x*4;
    P.eyes.forEach(e=>{
      const cx=e.cx+dx, cy=44+dy;
      e.open.setAttribute('cx', cx.toFixed(2)); e.open.setAttribute('cy', cy.toFixed(2));
      e.open.setAttribute('ry', Math.max(0.01, 4.6*open).toFixed(2));
      e.open.setAttribute('opacity', open>0.15 ? 1 : 0);
      // Yeux fermés (sommeil, clignement) = un trait ; joie = un petit arc « ^ ».
      const arc=happy*3.2;
      e.happy.setAttribute('d', `M${(cx-4).toFixed(2)} ${(cy+arc*0.4).toFixed(2)} Q${cx.toFixed(2)} ${(cy-arc).toFixed(2)} ${(cx+4).toFixed(2)} ${(cy+arc*0.4).toFixed(2)}`);
      e.happy.setAttribute('opacity', open>0.15 ? 0 : 1);
    });
    // Moustache : frémit quand il parle, se relève quand il est content.
    const talk=pulse*Math.sin(t*28)*0.5+pulse*0.4;
    P.stache.setAttribute('transform', `translate(50 64) translate(0 ${(-happy*1.6).toFixed(2)}) scale(${(1+happy*0.08).toFixed(3)} ${(1+talk*0.35-happy*0.1).toFixed(3)}) translate(-50 -64)`);
  }

  // ---- placement --------------------------------------------------------------
  // Sur la ligne d'activité du tour en cours (host) s'il y en a une, sinon sur la
  // DERNIÈRE ligne du fil (il y reste entre deux tours), sinon en grand au centre
  // de l'accueil si le fil est vide, sinon rangé (invisible).
  let line=null;  // ligne qui l'accueille en ce moment (son logo « J » est masqué)
  let stage=null; // scène plein écran du mode Jean (21-jean-stage.js), prioritaire
  function place(){
    if(!wrap) return;
    const fromMode=wrap.className, from=fromMode ? wrap.getBoundingClientRect() : null;
    placeNow();
    const toMode=wrap.className;
    if(from && from.width && fromMode!==toMode && ((fromMode==='jav-stage' && toMode==='jav-inline') || (fromMode==='jav-inline' && toMode==='jav-stage'))) glide(from);
  }
  // Fait voler Jean de l'ancien rectangle `from` jusqu'à sa nouvelle place. On
  // anime une COPIE posée au-dessus de tout (position fixe) : pendant le fondu, la
  // scène recouvre encore le fil, et le vrai Jean, déjà rangé dans le fil, y
  // serait caché. Le vrai reste invisible le temps du vol, puis prend le relais.
  // La copie est toujours dessinée à la PLUS GRANDE des deux tailles, puis réduite
  // par transform : agrandir un petit dessin le rendait flou pendant le vol (il
  // n'était redessiné net qu'à l'arrivée).
  function glide(from){
    const to=wrap.getBoundingClientRect(); if(!to.width) return;
    // Vol précédent interrompu (clics rapides) : on repart de là où la copie se
    // trouve à l'écran, pas de la place du vrai Jean (saut).
    if(glide.g){ const r=glide.g.getBoundingClientRect(); if(r.width) from=r; glide.g.remove(); }
    const big=from.width>=to.width ? from : to;
    const at=r=>`translate(${(r.left-big.left).toFixed(1)}px, ${(r.top-big.top).toFixed(1)}px) scale(${(r.width/big.width).toFixed(4)})`;
    const ghost=glide.g=wrap.cloneNode(true);
    ghost.removeAttribute('id'); ghost.className='jav-ghost';
    // Le vrai Jean est déjà caché si un vol est en cours : la copie héritait de son
    // visibility:hidden et Jean disparaissait le temps du vol.
    ghost.style.visibility='';
    Object.assign(ghost.style, {left:big.left+'px', top:big.top+'px', width:big.width+'px', height:big.height+'px', transition:'none', transform:at(from)});
    document.body.appendChild(ghost);
    // visibility et non opacity : l'animation d'apparition (javin) imposait son
    // opacité et le vrai Jean restait visible à l'arrivée, en double.
    wrap.style.visibility='hidden';
    void ghost.offsetWidth;
    ghost.style.transition=''; ghost.style.transform=at(to);
    clearTimeout(glide.t);
    glide.t=setTimeout(()=>{ ghost.remove(); glide.g=null; wrap.style.visibility=''; S.squash.v+=4; }, 760);
  }
  function placeNow(){
    if(wrap.className==='jav-hero') wrap.style.transform=''; // position d'accueil oubliée
    const jean=document.body.classList.contains('jean-mode');
    // Scène plein écran : il y vit en grand, tant qu'on n'affiche pas le fil.
    if(jean && stage && !document.body.classList.contains('jean-history')){
      if(line){ line.classList.remove('jav-host'); line=null; }
      if(wrap.parentNode!==stage) stage.appendChild(wrap);
      wrap.className='jav-stage';
      start(); return;
    }
    const chat=document.getElementById('chat');
    const empty=document.getElementById('chat-empty');
    let target=null;
    if(jean){
      if(host && host.isConnected) target=host;
      else if(chat){ const all=chat.querySelectorAll(':scope > .genstatus'); target=all.length ? all[all.length-1] : null; }
    }
    if(line && line!==target) line.classList.remove('jav-host');
    line=target;
    if(target){
      target.classList.add('jav-host');
      if(wrap.parentNode!==target) target.insertBefore(wrap, target.firstChild);
      wrap.className='jav-inline';
    } else if(jean && empty && empty.classList.contains('show')){
      if(wrap.parentNode!==root) root.appendChild(wrap);
      wrap.className='jav-hero';
      const comp=document.getElementById('composer'), ch=comp ? comp.offsetHeight : 120;
      const size=Math.min(120, root.clientWidth*0.3);
      wrap.style.setProperty('--jav-size', size+'px');
      wrap.style.transform=`translate(${(root.clientWidth/2-size/2).toFixed(1)}px, ${((root.clientHeight-ch)/2-30-size/2).toFixed(1)}px)`;
    } else {
      if(wrap.parentNode!==root) root.appendChild(wrap);
      wrap.className=''; wrap.style.transform='';
    }
    start();
  }

  function lastLine(chat){ const all=chat.querySelectorAll(':scope > .genstatus'); return all.length ? all[all.length-1] : null; }

  function visible(){ return !document.hidden && !!wrap.className; }
  function start(){ if(!raf && visible()){ last=0; raf=requestAnimationFrame(frame); } }

  function init(){
    root=document.querySelector('.main'); if(!root) return;
    build(); root.appendChild(wrap);
    place();
    addEventListener('resize', place);
    const empty=document.getElementById('chat-empty');
    if(empty) new MutationObserver(place).observe(empty, {attributes:true, attributeFilter:['class']});
    // Le fil change (nouveau tour, rechargement, lot ancien inséré) : il se recale
    // sur la dernière ligne si la sienne a disparu ou n'est plus la dernière.
    const chat=document.getElementById('chat');
    if(chat) new MutationObserver(()=>{ if(!REPLAYING && !host && (!line || !line.isConnected || line!==lastLine(chat))) place(); }).observe(chat, {childList:true});
    new MutationObserver(place).observe(document.body, {attributes:true, attributeFilter:['class']});
    document.addEventListener('visibilitychange', start);
    addEventListener('pointermove', e=>{ pointer={x:e.clientX, y:e.clientY, t:performance.now()}; if(state==='sleep' && performance.now()-lastActivity>1500) wake(); }, {passive:true});
    const input=document.getElementById('input');
    if(input){
      const listen=()=>{ wake(); if(!busy && (state==='idle'||state==='listen')) setState('listen', 2500); };
      input.addEventListener('input', listen); input.addEventListener('focus', listen);
    }
  }
  if(document.readyState==='loading') document.addEventListener('DOMContentLoaded', init); else init();

  return {
    // Activité du tour : même clé que la phrase de la ligne d'activité.
    act(k){
      if(!k) return;
      wake();
      const map={'jean.act_think':'think','jean.act_read':'think','jean.act_search':'search','jean.act_remember':'search','jean.act_forget':'search',
        'jean.act_web':'web','jean.act_browse':'web','jean.act_look':'web','jean.act_machine':'work','jean.act_task':'work','jean.act_work':'work'};
      setState(map[k]||'think');
    },
    speak,
    // Fin de réponse : content un instant. Il reste sur la ligne du tour.
    done(){ setState('happy', 1600); },
    // Le modèle se charge : il patiente (yeux qui tournent).
    loading(on){ loadingNow=!!on; },
    // Il travaille sur son écran (navigateur piloté) : il le regarde.
    browsing(on){ if(on && !browsing) wake(); browsing=!!on; },
    // Scène plein écran : le conteneur qui l'accueille en grand.
    mount(el){ stage=el; place(); },
    // On lui écrit : il regarde la bulle de saisie.
    listen(){ wake(); if(state==='idle'||state==='listen'||state==='happy') setState('listen', 3000); },
    // Le message envoyé est « avalé » : il s'écrase puis rebondit.
    gulp(){ wake(); S.squash.v+=9; setTimeout(()=>{ S.squash.v-=7; hopT=0.25; }, 140); },
    // La ligne d'activité d'un tour (GENEL) l'accueille à la place du logo « J ».
    attach(l){ if(host===l) return; host=l; place(); },
    // Ligne du tour figée ou retirée : il se repose sur la dernière ligne du fil.
    detach(){ host=null; place(); },
  };
})();
