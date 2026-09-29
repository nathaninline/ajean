// 03e-seg.js — sélecteurs segmentés (.pe-seg, .seg) : une pastille glisse sous
// le choix actif au lieu d'un simple changement de fond.
//
// Chaque sélecteur reçoit un élément .seg-thumb, positionné sous le <label> dont
// la radio est cochée. On le recale quand on clique, quand une radio est cochée
// par le code (réouverture d'une fenêtre : setter `checked` intercepté), quand
// le sélecteur change de taille (fenêtre qui s'affiche) et pour tout sélecteur
// ajouté plus tard.
(function(){
  const SEL = '.pe-seg,.seg';
  // seg -> animer ? (un clic animé l'emporte sur un recalage du code dans la même image)
  const pending = new Map();
  let raf = 0;
  function place(seg, animate){
    let th = seg.querySelector(':scope>.seg-thumb');
    if(!th){ th = document.createElement('span'); th.className = 'seg-thumb'; seg.prepend(th); animate = false; }
    const lab = [...seg.querySelectorAll(':scope>label')].find(l=>{ const i = l.querySelector('input'); return i && i.checked; });
    if(!lab || !seg.offsetWidth){ th.style.opacity = '0'; return; }
    const w = lab.offsetWidth + 'px', h = lab.offsetHeight + 'px';
    const tr = 'translate(' + lab.offsetLeft + 'px,' + lab.offsetTop + 'px)';
    // Déjà en route vers cette cible (clic animé) : un recalage sans animation
    // juste après (la fenêtre se réorganise) ne doit pas couper le glissement.
    if(th.style.transform === tr && th.style.width === w && th.style.height === h && th.style.opacity === '1') return;
    if(!animate) th.style.transition = 'none';
    th.style.opacity = '1';
    th.style.width = w; th.style.height = h; th.style.transform = tr;
    if(!animate){ void th.offsetWidth; th.style.transition = ''; }
  }
  function queue(seg, animate){
    if(!seg) return;
    pending.set(seg, pending.get(seg) || animate);
    if(!raf) raf = requestAnimationFrame(()=>{ raf = 0; const list = [...pending]; pending.clear(); list.forEach(([s, a])=>place(s, a)); });
  }
  // Cochage par le code (ouverture d'un preset existant…) : sans animation.
  const desc = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'checked');
  Object.defineProperty(HTMLInputElement.prototype, 'checked', {
    configurable: true, enumerable: desc.enumerable,
    get(){ return desc.get.call(this); },
    set(v){ desc.set.call(this, v); if(this.type === 'radio'){ const s = this.closest && this.closest(SEL); if(s) queue(s, false); } }
  });
  // Clic de l'utilisateur : animé.
  document.addEventListener('change', (e)=>{ const s = e.target.closest && e.target.closest(SEL); if(s) queue(s, true); });
  const ro = new ResizeObserver(entries=>entries.forEach(en=>queue(en.target, false)));
  function watch(root){ (root.matches && root.matches(SEL) ? [root] : []).concat([...(root.querySelectorAll ? root.querySelectorAll(SEL) : [])]).forEach(s=>{ if(!s._segW){ s._segW = 1; ro.observe(s); queue(s, false); } }); }
  function init(){
    watch(document);
    new MutationObserver(ms=>ms.forEach(m=>m.addedNodes.forEach(n=>{ if(n.nodeType === 1) watch(n); }))).observe(document.body, {childList:true, subtree:true});
  }
  if(document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init); else init();
})();
