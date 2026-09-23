// ===== Visionneuse d'images (lightbox) ======================================
// Un clic sur une image (pièce jointe du fil ou du composeur, image insérée par
// l'IA) l'ouvre en grand. L'image part de sa vignette et s'agrandit jusqu'au
// centre (technique FLIP : on la pose à sa place finale, puis on anime depuis la
// position de la vignette), sur un fond flouté. Les images voisines (même message,
// même composeur, même réponse) se parcourent avec ← → ou les flèches à l'écran.
// Double-clic / double-tap : zoom ×2.5 à l'endroit visé, puis on glisse pour se
// déplacer. Sur mobile, glisser vers le bas ferme. Tout passe par transform et
// opacity (pas de layout pendant les animations) pour rester fluide.
let LB = null;
const LB_EASE = 'cubic-bezier(.2,.8,.2,1)';
function lbReduced(){ return !!(window.matchMedia && matchMedia('(prefers-reduced-motion: reduce)').matches); }

function lbBuild(){
  if(LB) return LB;
  const root=document.createElement('div');
  root.id='lightbox'; root.setAttribute('role','dialog'); root.setAttribute('aria-modal','true');
  root.innerHTML=
    '<div class="lb-backdrop"></div>'+
    '<img class="lb-img" alt="" draggable="false">'+
    '<div class="lb-top">'+
      '<span class="lb-name"></span><span class="lb-count"></span>'+
      '<button type="button" class="lb-btn lb-dl"><svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v12M8 11l4 4 4-4M4 19h16"/></svg></button>'+
      '<button type="button" class="lb-btn lb-close"><svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><path d="M6 6l12 12M18 6L6 18"/></svg></button>'+
    '</div>'+
    '<button type="button" class="lb-btn lb-nav lb-prev"><svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 6l-6 6 6 6"/></svg></button>'+
    '<button type="button" class="lb-btn lb-nav lb-next"><svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M9 6l6 6-6 6"/></svg></button>';
  document.body.appendChild(root);
  const q=(c)=>root.querySelector(c);
  LB={root, img:q('.lb-img'), back:q('.lb-backdrop'), name:q('.lb-name'), count:q('.lb-count'),
      prev:q('.lb-prev'), next:q('.lb-next'), list:[], idx:0, open:false, z:1, tx:0, ty:0, returnFocus:null};
  q('.lb-close').title=q('.lb-close').ariaLabel=t('lightbox.close');
  q('.lb-dl').title=q('.lb-dl').ariaLabel=t('lightbox.download');
  LB.prev.title=LB.prev.ariaLabel=t('lightbox.prev');
  LB.next.title=LB.next.ariaLabel=t('lightbox.next');
  q('.lb-close').onclick=()=>closeLightbox();
  q('.lb-dl').onclick=()=>lbDownload();
  LB.prev.onclick=(e)=>{ e.stopPropagation(); lbStep(-1); };
  LB.next.onclick=(e)=>{ e.stopPropagation(); lbStep(1); };
  LB.back.onclick=()=>closeLightbox();
  lbGestures();
  document.addEventListener('keydown', (e)=>{
    if(!LB||!LB.open) return;
    if(e.key==='Escape'){ e.preventDefault(); closeLightbox(); }
    else if(e.key==='ArrowLeft'){ e.preventDefault(); lbStep(-1); }
    else if(e.key==='ArrowRight'){ e.preventDefault(); lbStep(1); }
  });
  window.addEventListener('resize', ()=>{ if(LB&&LB.open){ lbLayout(); lbApply(); } });
  return LB;
}

// Les images « voisines » : celles du même message / composeur / réponse, déjà
// chargées (une vignette encore vide n'a rien à montrer en grand).
function lbGroup(img){
  const scope=img.closest('.msg-files, #attach-list, .body, .msg') || img.parentNode || document.body;
  return [...scope.querySelectorAll('img.it-img, img.chat-img')].filter(i=>i.src && i.naturalWidth);
}

// Taille d'affichage « ajustée » : l'image tient dans l'écran (marges comprises)
// sans jamais être agrandie au-delà de sa taille réelle.
function lbFit(w, h){
  const mw=Math.max(80, innerWidth - (innerWidth<600 ? 16 : 120)), mh=Math.max(80, innerHeight - (innerWidth<600 ? 120 : 140));
  const s=Math.min(1, mw/w, mh/h);
  return {w:Math.round(w*s), h:Math.round(h*s)};
}
function lbLayout(){
  // Dimensions de l'original : connues d'avance (data-w/h de la vignette) même si
  // l'image pleine résolution n'est pas encore décodée.
  const im=LB.img, src=LB.list[LB.idx]||{dataset:{}};
  const W=+src.dataset.w||im.naturalWidth||src.naturalWidth||1, H=+src.dataset.h||im.naturalHeight||src.naturalHeight||1;
  const f=lbFit(W, H);
  im.style.width=f.w+'px'; im.style.height=f.h+'px';
  im.style.left=Math.round((innerWidth-f.w)/2)+'px'; im.style.top=Math.round((innerHeight-f.h)/2)+'px';
}
function lbApply(){ LB.img.style.transform = (LB.z!==1||LB.tx||LB.ty) ? `translate(${LB.tx}px,${LB.ty}px) scale(${LB.z})` : ''; }
function lbResetZoom(){ LB.z=1; LB.tx=0; LB.ty=0; LB.root.classList.remove('zoomed'); lbApply(); }

// Transformation qui ramène l'image affichée (position finale) sur un rectangle
// source (la vignette) : base de l'animation d'ouverture et de fermeture.
function lbFromRect(r){
  const im=LB.img, W=parseFloat(im.style.width), H=parseFloat(im.style.height);
  const L=parseFloat(im.style.left), T=parseFloat(im.style.top);
  const s=Math.max(r.width/W, r.height/H);
  const dx=(r.left+r.width/2)-(L+W/2), dy=(r.top+r.height/2)-(T+H/2);
  return `translate(${dx}px,${dy}px) scale(${s})`;
}
// La vignette d'origine est-elle encore à l'écran ? (fermeture vers elle, sinon fondu)
function lbVisibleRect(el){
  if(!el || !el.isConnected) return null;
  const r=el.getBoundingClientRect();
  if(!r.width || r.bottom<0 || r.top>innerHeight) return null;
  return r;
}

function openLightbox(img){
  if(!img || !img.src || !img.naturalWidth) return;
  lbBuild();
  let list=lbGroup(img), idx=list.indexOf(img);
  if(idx<0){ list=[img]; idx=0; }
  LB.list=list; LB.idx=idx; LB.open=true;
  LB.returnFocus=document.activeElement;
  document.documentElement.classList.add('lb-lock');
  LB.root.classList.add('show');
  lbShow(true);
  LB.root.querySelector('.lb-close').focus({preventScroll:true});
}

function lbShow(fromThumb, dir){
  const src=LB.list[LB.idx], im=LB.img;
  lbResetZoom();
  // On démarre avec la VIGNETTE (déjà décodée, mêmes proportions) : poser d'emblée
  // l'original, pas encore décodé, faisait démarrer l'animation sur une image vide
  // puis la faisait apparaître en plein vol (saccade). L'original est décodé à part
  // et prend la place, sans transition visible, dès qu'il est prêt.
  im.src=src.src; im.alt=src.alt||'';
  const full=src.dataset.full;
  if(full && full!==src.src){
    const pre=new Image(); pre.src=full;
    (pre.decode ? pre.decode() : Promise.resolve()).then(()=>{ if(LB.open && LB.list[LB.idx]===src) im.src=full; }).catch(()=>{});
  }
  LB.name.textContent=src.dataset.name||src.alt||'';
  const many=LB.list.length>1;
  LB.count.textContent=many ? (LB.idx+1)+' / '+LB.list.length : '';
  LB.prev.hidden=LB.next.hidden=!many;
  lbLayout();
  if(lbReduced()){ LB.root.classList.add('in'); return; }
  const r=fromThumb ? lbVisibleRect(src) : null;
  if(r){
    // Ouverture : la vignette « grandit » jusqu'au centre, le fond se floute.
    im.animate([{transform:lbFromRect(r), opacity:.9}, {transform:'none', opacity:1}],
      {duration:320, easing:LB_EASE});
    requestAnimationFrame(()=>LB.root.classList.add('in'));
  } else {
    LB.root.classList.add('in');
    const from = dir ? `translateX(${dir*40}px)` : 'scale(.96)';
    im.animate([{transform:from, opacity:0}, {transform:'none', opacity:1}], {duration:220, easing:LB_EASE});
  }
}

function lbStep(d){
  if(!LB||!LB.open||LB.list.length<2) return;
  LB.idx=(LB.idx+d+LB.list.length)%LB.list.length;
  lbShow(false, d);
}

function closeLightbox(){
  if(!LB||!LB.open) return;
  LB.open=false;
  const im=LB.img, src=LB.list[LB.idx];
  const finish=()=>{
    LB.root.classList.remove('show','in','dragging','zoomed');
    im.style.transform=''; im.style.opacity=''; LB.back.style.opacity='';
    document.documentElement.classList.remove('lb-lock');
    const f=LB.returnFocus; LB.returnFocus=null;
    if(f && f.focus) try{ f.focus({preventScroll:true}); }catch(_){}
  };
  LB.root.classList.remove('in');
  if(lbReduced()){ finish(); return; }
  const r=lbVisibleRect(src);
  const cur=getComputedStyle(im).transform;
  const start={transform: cur==='none' ? 'none' : cur, opacity: im.style.opacity||1};
  const end = r ? {transform:lbFromRect(r), opacity:.9} : {transform:(cur==='none'?'':cur+' ')+'scale(.92)', opacity:0};
  const a=im.animate([start, end], {duration:r?260:200, easing:LB_EASE, fill:'forwards'});
  a.onfinish=()=>{ a.cancel(); finish(); };
}

function lbDownload(){
  const src=LB.list[LB.idx]; if(!src) return;
  const name=src.dataset.name||src.alt||'image';
  if(src.dataset.path && typeof downloadWorkspaceFile==='function'){ downloadWorkspaceFile(src.dataset.path, name, null); return; }
  const a=document.createElement('a'); a.href=src.dataset.full||src.src; a.download=name;
  document.body.appendChild(a); a.click(); a.remove();
}

// Gestes : double-clic/tap = zoom ; glisser = déplacer (zoomé) ou fermer vers le
// bas (non zoomé) ; molette = zoom progressif sur ordinateur.
function lbGestures(){
  const im=LB.img;
  let down=null, lastTap=0;
  const zoomAt=(cx, cy, z)=>{
    // Zoom centré sur le point visé : on garde ce point immobile sous le doigt.
    const r=im.getBoundingClientRect();
    const ox=cx-(r.left+r.width/2), oy=cy-(r.top+r.height/2);
    const k=z/LB.z;
    LB.tx=LB.tx - ox*(k-1); LB.ty=LB.ty - oy*(k-1); LB.z=z;
    if(z<=1.01){ LB.z=1; LB.tx=0; LB.ty=0; }
    LB.root.classList.toggle('zoomed', LB.z>1);
    im.style.transition='transform .25s '+LB_EASE; lbApply();
    setTimeout(()=>{ im.style.transition=''; }, 260);
  };
  im.addEventListener('dblclick', (e)=>{ e.preventDefault(); zoomAt(e.clientX, e.clientY, LB.z>1 ? 1 : 2.5); });
  im.addEventListener('wheel', (e)=>{
    if(!LB.open) return; e.preventDefault();
    const z=Math.min(6, Math.max(1, LB.z*(e.deltaY<0 ? 1.15 : 1/1.15)));
    const r=im.getBoundingClientRect(), ox=e.clientX-(r.left+r.width/2), oy=e.clientY-(r.top+r.height/2), k=z/LB.z;
    LB.tx-=ox*(k-1); LB.ty-=oy*(k-1); LB.z=z;
    if(z<=1.01){ LB.z=1; LB.tx=0; LB.ty=0; }
    LB.root.classList.toggle('zoomed', LB.z>1); lbApply();
  }, {passive:false});
  im.addEventListener('pointerdown', (e)=>{
    if(e.button>0) return;
    // Double-tap tactile (le dblclick n'arrive pas toujours sur mobile).
    const now=Date.now();
    if(e.pointerType==='touch' && now-lastTap<300){ lastTap=0; zoomAt(e.clientX, e.clientY, LB.z>1 ? 1 : 2.5); return; }
    lastTap=now;
    down={x:e.clientX, y:e.clientY, tx:LB.tx, ty:LB.ty, moved:false};
    im.setPointerCapture(e.pointerId);
  });
  im.addEventListener('pointermove', (e)=>{
    if(!down) return;
    const dx=e.clientX-down.x, dy=e.clientY-down.y;
    if(!down.moved && Math.hypot(dx,dy)<4) return;
    down.moved=true; LB.root.classList.add('dragging');
    if(LB.z>1){ LB.tx=down.tx+dx; LB.ty=down.ty+dy; lbApply(); return; }
    // Non zoomé : l'image suit le doigt verticalement, le fond s'estompe.
    const k=Math.min(1, Math.abs(dy)/300);
    im.style.transform=`translate(${dx*0.3}px,${dy}px) scale(${1-k*0.15})`;
    LB.back.style.opacity=String(1-k*0.8);
  });
  const up=(e)=>{
    if(!down) return;
    const dy=e.clientY-down.y, moved=down.moved; down=null;
    LB.root.classList.remove('dragging');
    if(!moved || LB.z>1) return;
    if(Math.abs(dy)>110){ closeLightbox(); return; }
    // Pas assez loin : retour en place.
    im.animate([{transform:im.style.transform}, {transform:'none'}], {duration:200, easing:LB_EASE});
    im.style.transform=''; LB.back.style.opacity='';
  };
  im.addEventListener('pointerup', up);
  im.addEventListener('pointercancel', up);
  // Clic simple sur l'image non zoomée : rien (seul le fond ferme), pour ne pas
  // fermer par erreur en visant un détail.
}
