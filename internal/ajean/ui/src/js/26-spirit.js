// ===== Petit esprit ==========================================================
// Une orbe de lumière en pixels qui accompagne la conversation, dans tous les modes.
// - Au chargement, le J du voile laisse place à l'orbe qui naît au centre, puis
//   elle va se poser au-dessus de la zone de saisie.
// - Pendant une réponse, elle se pose à la place du petit J de la ligne
//   d'activité (.genstatus) et s'agite : elle réfléchit.
// - À la fin, elle lance un éclat puis revient se poser.
// - Au repos, elle flotte doucement autour de sa place.
// - Sans activité pendant IDLE_MS, elle glisse se ranger (en bas à gauche, au-dessus
//   de la zone de saisie) ou sur la dernière ligne d'état, qui s'écarte pour
//   lui faire de la place, puis se fond en une petite étoile fixe (boucle arrêtée).
//   Un clic, une touche, la molette ou une réponse la réveillent.
// Rendu : un tampon de pixels en niveaux de gris (même dessin que l'orbe de référence),
// quantifié avec un léger tramage, puis peint dans la couleur du texte du thème
// (blanc en sombre, noir en clair) avec l'intensité comme opacité. Le canvas est
// petit et suit l'orbe : on ne calcule que les pixels autour d'elle.
// Option d'apparence « masquer le petit esprit » (data-hide-spirit).
var Spirit = (()=>{
  const SEAT_X=12;           // centre de la place réservée à gauche d'une ligne d'état (largeur 24 px)
  const GO_SLEEP_MS=1800;     // durée du trajet vers la place de repos
  const IDLE_MS=15000;       // inactivité avant qu'elle aille dormir
  const PX=3;                 // un pixel du dessin = 3 px écran
  const B=120;                // tampon de 120 x 120 pixels autour de l'orbe
  const reduced=matchMedia('(prefers-reduced-motion: reduce)');
  let main=null, cv=null, g=null, img=null, buf=null, raf=0, last=0, t=0;
  let x=0, y=0, mvx=0, mvy=0;  // position (px écran, relative à .main) et vitesse de vol
  let typeFirst=-1e9, ttx=null, tty=null;  // début de la frappe, cible lissée pendant la frappe
  let k=0, kT=0;              // taille de l'orbe (1 = grande, 0.55 = compagne)
  let phase='wait';           // wait (voile affiché), birth, live
  let birthAt=0, burstAt=-1e9, wasBusy=false, color='', seat=null, sleepEl=null;
  let lastAct=performance.now(), asleep=false, still=0, goFrom=null, goT0=0;  // still : 0 orbe vivante, 1 petite étoile de repos
  const sparks=[];
  // Réactions : on la touche (sursaut, éclat, elle s'agite) ; la souris s'approche
  // (elle esquive). off* = décalage d'affichage qui revient à zéro tout seul.
  let typeAt=-1e9, measure=null;   // frappe dans la zone de saisie
  let pokeAt=-1e9, offX=0, offY=0, offVX=0, offVY=0;  // décalage et sa vitesse (px, px/s)
  const N=11, D2=[[0,.5],[.75,.25]];
  const quant=(v,i,j)=>Math.min(1,Math.floor(v*N+D2[j&1][i&1])/N);

  function hidden(){ return document.documentElement.getAttribute('data-hide-spirit')==='1'; }

  function mount(){
    main=document.querySelector('.main'); if(!main) return false;
    cv=document.createElement('canvas'); cv.id='spirit'; cv.width=B; cv.height=B; cv.setAttribute('aria-hidden','true');
    main.appendChild(cv); g=cv.getContext('2d'); img=g.createImageData(B,B); buf=new Float32Array(B*B);
    const r=main.getBoundingClientRect(); x=r.width/2; y=r.height/2;
    document.addEventListener('visibilitychange', ()=>{ if(!document.hidden) kick(); });
    // toute action dans l'app la réveille
    for(const ev of ['pointerdown','keydown','wheel','touchstart']) addEventListener(ev, wake, {passive:true, capture:true});
    addEventListener('resize', ()=>{ if(asleep){ asleep=false; kick(); } });
    addEventListener('pointerdown', e=>{ const v=near(e,34); if(v) poke(v); }, {passive:true, capture:true});
    addEventListener('input', e=>{ if(e.target && e.target.id==='input'){ const n=performance.now(); if(n-typeAt>2500) typeFirst=n; typeAt=n; wake(); } }, {capture:true});
    addEventListener('pointermove', e=>{ if(e.pointerType!=='mouse' || hidden()) return; const v=near(e,52);
      if(v){ const f=(52-v.d)/52*60; offVX-=v.dx/v.d*f; offVY-=v.dy/v.d*f; lastAct=performance.now(); if(asleep){ asleep=false; } kick(); } }, {passive:true});
    return true;
  }
  // Le pointeur est-il sur l'orbe ? (distance au centre affiché)
  function near(e,rad){
    if(!main || hidden() || phase!=='live') return null;
    const mr=main.getBoundingClientRect(), dx=e.clientX-(mr.left+x+offX), dy=e.clientY-(mr.top+y+offY), d=Math.hypot(dx,dy)||1;
    return d<rad ? {dx,dy,d} : null;
  }
  function poke(v){
    const now=performance.now();
    pokeAt=now; burstAt=now; lastAct=now;
    offVX-=v.dx/v.d*300; offVY-=v.dy/v.d*300+60;        // poussée : elle part en arrière en freinant
    for(let i=0;i<8;i++){ const a=i/8*6.283; sparks.push({x:x+Math.cos(a)*6, y:y+Math.sin(a)*6, vx:Math.cos(a)*60, vy:Math.sin(a)*60, life:1}); }
    if(asleep) asleep=false;
    kick();
  }
  // Pendant la frappe : au-dessus de la zone de saisie, à peu près à l'endroit où
  // l'on écrit (largeur mesurée du texte de la ligne en cours, avant le curseur).
  function typeSpot(){
    const ta=document.getElementById('input'); if(!ta) return null;
    const r=main.getBoundingClientRect(), tr=ta.getBoundingClientRect(); if(!tr.width) return null;
    const cs=getComputedStyle(ta);
    if(!measure) measure=document.createElement('canvas').getContext('2d');
    measure.font=cs.fontSize+' '+cs.fontFamily;
    const before=ta.value.slice(0, ta.selectionStart==null? ta.value.length : ta.selectionStart);
    const line=before.slice(before.lastIndexOf('\n')+1);
    const w=tr.width-parseFloat(cs.paddingLeft)-parseFloat(cs.paddingRight);
    const cx=parseFloat(cs.paddingLeft)+Math.min(w, measure.measureText(line).width % Math.max(40,w));
    return [Math.max(30, Math.min(r.width-30, tr.left-r.left+cx+14)), tr.top-r.top-60];
  }
  function wake(){ lastAct=performance.now(); if(asleep){ asleep=false; kick(); } }
  function kick(){ if(!raf && !hidden()) { last=performance.now(); raf=requestAnimationFrame(frame); } }

  // ---- dessin dans le tampon (coordonnées du tampon, orbe au centre) ----
  const put=(px,py,v)=>{ px|=0; py|=0; if(px<0||py<0||px>=B||py>=B) return; const i=py*B+px; if(v>buf[i]) buf[i]=v; };
  const add=(px,py,v)=>{ px|=0; py|=0; if(px<0||py<0||px>=B||py>=B) return; const i=py*B+px; buf[i]=Math.min(1,buf[i]+v); };
  function sparkle(px,py,size,b){ put(px,py,b); for(let q=1;q<=size;q++){ const f=b*(1-q/(size+1)); put(px+q,py,f); put(px-q,py,f); put(px,py+q,f); put(px,py-q,f); } }

  function drawOrb(cx,cy,s,busy){
    const sp=busy?1.8:1;
    const pulse=.5+.5*Math.sin(t*4*sp);
    // halo qui respire
    // un peu plus resserré qu'à l'origine, et une chute plus douce vers le bord
    // pour qu'on ne devine plus le cercle
    const GR=(24+pulse*5)*s;
    for(let j=-GR;j<=GR;j++) for(let i=-GR;i<=GR;i++){ const d=Math.hypot(i,j)/GR; if(d<1) add(cx+i,cy+j,.55*Math.pow(1-d,2.9)); }
    // rayons qui tournent lentement
    for(let q=0;q<8;q++){ const a=q/8*Math.PI*2+t*.4*sp, L=(q%2?14:22)*s*(.8+.2*Math.sin(t*3+q));
      for(let r=6*s;r<L;r+=.5) put(cx+Math.cos(a)*r, cy+Math.sin(a)*r, .5*(1-r/L)); }
    // cœur : bord qui ondule comme une flamme, vacillement
    const flick=.9+.1*Math.sin(t*23)*Math.sin(t*9.7), R=Math.ceil(16*s);
    for(let j=-R;j<=R;j++) for(let i=-R;i<=R;i++){
      const a=Math.atan2(j,i), wob=1+.18*Math.sin(a*5+t*6*sp)+.1*Math.sin(a*3-t*9*sp);
      const d=Math.hypot(i,j)/wob/s;
      put(cx+i,cy+j,(d<2.6?1:.97*Math.exp(-Math.pow((d-2.6)/3.4,1.4)))*flick);
    }
    // petites lueurs qui tournent autour du cœur
    for(let q=0;q<3;q++){ const a=t*2.2*sp+q*2.09; put(cx+Math.cos(a)*9*s, cy+Math.sin(a)*5*s, .85); }
    if(pulse>.88) sparkle(cx,cy,Math.round(13*s),1);
  }

  // Étoile de repos : toujours le même dessin (pas de hasard ni d'animation), un
  // point, quatre branches et un halo tramé.
  function drawStar(cx,cy,a){
    for(let j=-8;j<=8;j++) for(let i=-8;i<=8;i++){ const d=Math.hypot(i,j)/8; if(d<1) add(cx+i,cy+j,.32*a*Math.pow(1-d,2)); }
    put(cx,cy,a); put(cx+1,cy,a*.9); put(cx,cy+1,a*.9); put(cx+1,cy+1,a*.9);
    for(let q=1;q<=4;q++){ const f=a*(1-q/5)*.85; put(cx-q,cy,f); put(cx+1+q,cy,f); put(cx,cy-q,f); put(cx,cy+1+q,f); put(cx+1,cy-q,f*.5); put(cx+1,cy+1+q,f*.5); }
  }
  // ---- où se poser ----
  function home(){
    const r=main.getBoundingClientRect(), comp=document.getElementById('composer');
    const cr=comp? comp.getBoundingClientRect() : {top:r.bottom-120,right:r.right-40};
    // bouton « descendre en bas » affiché : elle se décale à sa gauche, sans le couvrir
    const sb=document.getElementById('scrollbtn');
    if(sb && sb.classList.contains('show')){ const br=sb.getBoundingClientRect();
      if(br.width) return [br.left-r.left-44, br.top-r.top+br.height/2]; }
    return [Math.min(r.width-40, cr.right-r.left-34), cr.top-r.top-34];
  }
  // Place de sommeil : le petit logo étoile de la DERNIÈRE ligne d'état du fil
  // (sous la dernière réponse) ; elle s'y pose et le remplace. Sans ligne visible :
  // en bas à gauche du fil, au-dessus de la zone de saisie.
  function sleepSpot(){
    const r=main.getBoundingClientRect(), comp=document.getElementById('composer'), chat=document.getElementById('chat');
    const lines=chat? chat.querySelectorAll(':scope > .genstatus') : [];
    const gs=lines.length? lines[lines.length-1] : null;
    if(gs){ const lr=gs.getBoundingClientRect(), chr=chat.getBoundingClientRect();
      if(lr.height && lr.top>chr.top+4 && lr.bottom<chr.bottom-4) return [lr.left-r.left+SEAT_X, lr.top-r.top+lr.height/2, gs]; }
    const cr=comp? comp.getBoundingClientRect() : {top:r.bottom-120,left:r.left+40};
    // début de la colonne du fil (même calcul que le padding de #chat)
    return [Math.max(26, r.width/2-380+14), cr.top-r.top-22, null];
  }
  function busyTarget(){
    const chat=document.getElementById('chat'); if(!chat) return null;
    // GENEL (09-stream.js) = la ligne d'activité de la réponse EN COURS ; elle vaut
    // null une fois le tour fini. Les lignes d'état des réponses passées restent
    // dans le fil : s'y fier ferait croire que l'IA travaille encore.
    const gs=(typeof GENEL!=='undefined' && GENEL && GENEL.isConnected) ? GENEL : null;
    if(!gs) return null;
    const r=gs.getBoundingClientRect(), mr=main.getBoundingClientRect(), cr=chat.getBoundingClientRect();
    if(r.bottom<cr.top+10 || r.top>cr.bottom-10) return {off:true};   // ligne hors de vue
    return {x:r.left-mr.left+SEAT_X, y:r.top-mr.top+r.height/2, el:gs};
  }

  function frame(now){
    raf=0;
    if(hidden()||!main){ cv&&(cv.style.opacity='0'); if(seat){ seat.classList.remove('orb-seat'); seat=null; } return; }
    if(document.hidden) return;
    const dt=Math.min(.05,(now-last)/1000); last=now; t+=dt;
    const r=main.getBoundingClientRect();
    const loading=document.getElementById('chat-loading');
    const veil=loading && loading.classList.contains('show');
    const appOpen=(()=>{ const a=document.getElementById('app-run'); return a && !a.hidden; })();
    let tx, ty, busy=false, sit=false;
    if(phase==='wait'){
      // chargement : l'orbe vit déjà, en petit, au centre du voile
      if(veil){ tx=r.width/2; ty=r.height/2; kT=.34; if(k<.01){ x=tx; y=ty; k=.05; } }
      else { phase='birth'; birthAt=now; burstAt=now; }
    }
    if(phase==='birth'){
      const e=(now-birthAt)/1000;
      kT=1; tx=r.width/2; ty=r.height/2;
      if(e>1.1){ phase='live'; }
    }
    if(phase==='live'){
      const bt=busyTarget();
      busy=!!bt;
      if(busy) lastAct=now;
      asleep=false;
      if(bt && !bt.off){ tx=bt.x; ty=bt.y; kT=.42; sit=true; }
      else if(now-typeAt<2500 && now-typeFirst>450 && typeSpot()){
        // curieuse : elle vient voir ce qu'on écrit ; la cible glisse au lieu de sauter
        const [px,py]=typeSpot(); if(ttx==null){ ttx=x; tty=y; }
        const a=1-Math.exp(-dt*1.6); ttx+=(px-ttx)*a; tty+=(py-tty)*a;
        tx=ttx+Math.sin(t*1.1)*12; ty=tty+Math.sin(t*1.7)*6; kT=.5; }
      else if(now-lastAct>IDLE_MS){ [tx,ty,sleepEl]=sleepSpot(); kT=.42; asleep=true; }
      else { const [hx,hy]=home(); tx=hx+Math.sin(t*.6)*22+Math.sin(t*1.9)*3; ty=hy+Math.sin(t*1.2)*8+Math.sin(t*7)*.8; kT=.55; }
      if(wasBusy && !busy) burstAt=now;                    // réponse finie : éclat
      wasBusy=busy;
    }
    // ressort : elle rattrape sa cible avec un peu de retard, comme un être vivant
    // approche amortie sans rebond : elle ralentit en arrivant, ne dépasse jamais
    if(reduced.matches){ x=tx; y=ty; }
    else if(asleep){
      // trajet vers le repos : départ en douceur, petite courbe en l'air, arrivée
      // ralentie (pas de « paf » d'un coup)
      if(!goFrom){ goFrom=[x,y]; goT0=now; } mvx=mvy=0;
      const p=Math.min(1,(now-goT0)/GO_SLEEP_MS), e=p<.5?4*p*p*p:1-Math.pow(-2*p+2,3)/2;
      x=goFrom[0]+(tx-goFrom[0])*e; y=goFrom[1]+(ty-goFrom[1])*e-Math.sin(p*Math.PI)*28;
    }
    else if(sit){ goFrom=null; const a=1-Math.exp(-dt*9); x+=(tx-x)*a; y+=(ty-y)*a; mvx=mvy=0; }
    else {
      // vol libre : ressort amorti critique (accélère et freine en douceur, ne
      // dépasse pas), vitesse plafonnée pour qu'aucun changement de cible ne donne d'à-coup
      goFrom=null;
      const w=2.6; mvx+=(w*w*(tx-x)-2*w*mvx)*dt; mvy+=(w*w*(ty-y)-2*w*mvy)*dt;
      const sp=Math.hypot(mvx,mvy), MAX=260; if(sp>MAX){ mvx*=MAX/sp; mvy*=MAX/sp; }
      x+=mvx*dt; y+=mvy*dt;
    }
    if(now-typeAt>=2500) ttx=tty=null;
    k+=(kT-k)*Math.min(1,dt*(phase==='birth'?7:4));
    // arrivée à sa place de repos : l'orbe se fond en sa petite étoile, toujours la même
    if(asleep && Math.hypot(tx-x,ty-y)<4) still=Math.min(1,still+dt*1.2); else if(!asleep) still=0;
    // posée sur le petit J de la ligne d'activité : on le cache, elle le remplace
    // la ligne où elle se pose s'écarte en douceur pour lui faire de la place
    const near=Math.hypot(tx-x,ty-y)<90 && phase==='live';
    const want=near ? (sit ? (busyTarget()||{}).el||null : (asleep ? sleepEl : null)) : null;
    if(want!==seat){ if(seat) seat.classList.remove('orb-seat'); seat=want; if(seat) seat.classList.add('orb-seat'); }
    // ---- tampon ----
    buf.fill(0);
    const c=B/2;
    const s=Math.max(.05,k);
    // étincelles en coordonnées écran, dessinées relativement à l'orbe
    if(!asleep && !reduced.matches && Math.random()<dt*(busy?20:10)*s) sparks.push({x:x+(Math.random()-.5)*8*PX*s, y:y+(Math.random()-.5)*8*PX*s, vx:(Math.random()-.5)*6*PX, vy:(4+Math.random()*6)*PX*.6, life:1});
    for(let i=sparks.length-1;i>=0;i--){ const p=sparks[i]; p.life-=dt*.9; p.x+=p.vx*dt; p.y+=p.vy*dt;
      if(p.life<=0){ sparks.splice(i,1); continue; }
      if(Math.sin(t*20+i)>-.3) sparkle(c+(p.x-x)/PX, c+(p.y-y)/PX, Math.round(p.life*3*Math.max(.6,s)), .4+.6*p.life); }
    const pk=Math.max(0,1-(now-pokeAt)/1200);           // 1 juste après qu'on l'a touchée
    // ressort amorti presque critique : elle s'éloigne puis revient en douceur, sans rebond
    offVX+=(-offX*30-offVX*10.5)*dt; offVY+=(-offY*30-offVY*10.5)*dt; offX+=offVX*dt; offY+=offVY*dt;
    drawOrb(c,c,s*(1+.35*pk*pk),busy||pk>0);
    if(still>0){ for(let i=0;i<buf.length;i++) buf[i]*=1-still; drawStar(c,c,still); }
    const be=(now-burstAt)/1000;                          // éclat (naissance, fin de réponse)
    if(be>=0 && be<.7){ const q=be/.7; sparkle(c,c,Math.round(10+q*30*s),1-q);
      const rr=6+q*40*s; for(let a=0;a<6.283;a+=.08) put(c+Math.cos(a)*rr, c+Math.sin(a)*rr, .6*(1-q)); }
    const d=img.data;
    for(let i=0;i<buf.length;i++){ const v=quant(buf[i],i%B,(i/B)|0); const o=i*4; d[o]=d[o+1]=d[o+2]=0; d[o+3]=Math.round(v*255); }
    g.putImageData(img,0,0);
    // teinte = couleur du texte du thème
    const col=getComputedStyle(document.body).color;
    if(col!==color) color=col;
    g.globalCompositeOperation='source-in'; g.fillStyle=color; g.fillRect(0,0,B,B); g.globalCompositeOperation='source-over';
    cv.style.opacity=appOpen?'0':'1';
    cv.style.transform=`translate(${x+offX-c*PX}px,${y+offY-c*PX}px)`;
    // étoile de repos formée : plus rien ne bouge jusqu'au réveil
    if(asleep && still>=1 && !sparks.length && Math.abs(offX)+Math.abs(offY)<.5 && Math.abs(offVX)+Math.abs(offVY)<1 && pk<=0) return;
    raf=requestAnimationFrame(frame);
  }

  function init(){ if(!mount()) return; kick();
    // l'option d'apparence peut changer à tout moment
    new MutationObserver(()=>{ if(hidden()){ cv.style.opacity='0'; if(seat){ seat.classList.remove('orb-seat'); seat=null; } } else kick(); })
      .observe(document.documentElement,{attributes:true,attributeFilter:['data-hide-spirit']});
  }
  if(document.readyState==='loading') document.addEventListener('DOMContentLoaded', init); else init();
  return {kick, wake};
})();
