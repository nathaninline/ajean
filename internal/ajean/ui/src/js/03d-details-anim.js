// 03d-details-anim.js — ouverture / fermeture des sections du menu.
//
// Ouverture : la section s'ouvre d'un coup et son contenu apparaît en fondu
// rapide (pas de dépliement). Fermeture : le contenu s'efface PENDANT que la
// section se resserre en douceur. Sans ce resserrement, la page perdait toute
// la hauteur de la section en une image : en bas du menu, le navigateur recalait
// alors le défilement d'un coup et la liste « claquait » vers le haut.
// L'état open/close reste celui du <details> ; les boutons posés dans un summary
// (+ des presets, ? d'aide) bloquent déjà le clic : on les ignore.
(function(){
  const IN = 150, OUT = 190;
  const EASE = 'cubic-bezier(.4,0,.2,1)';
  const body = (d, sum)=>[...d.children].filter(c=>c !== sum);
  function stop(d){
    (d._anims || []).forEach(a=>a.cancel()); d._anims = [];
    d.style.overflow = ''; d.style.height = '';
  }
  document.addEventListener('click', (e)=>{
    const sum = e.target.closest && e.target.closest('#side details>summary');
    if(!sum || e.defaultPrevented) return;
    const d = sum.parentElement;
    if(matchMedia('(prefers-reduced-motion: reduce)').matches) return; // comportement natif
    e.preventDefault();
    if(!d.open || d._closing){
      stop(d);
      d._closing = false; d.classList.remove('closing');
      d.open = true;
      d._anims = body(d, sum).map(c=>c.animate({opacity:[0,1]}, {duration: IN, easing:'ease-out'}));
      return;
    }
    stop(d);
    d._closing = true; d.classList.add('closing'); // le « + » des presets s'efface dès le début
    // Hauteur fermée = le titre + la bordure du haut (sans basculer open, pour ne
    // pas déclencher d'événements « toggle » parasites).
    const cs = getComputedStyle(d);
    const closedH = sum.offsetHeight + (parseFloat(cs.borderTopWidth) || 0);
    const fromH = d.offsetHeight;
    d.style.overflow = 'hidden';
    const shrink = d.animate({height:[fromH + 'px', closedH + 'px']}, {duration: OUT, easing: EASE});
    const fades = body(d, sum).map(c=>c.animate({opacity:[1,0]}, {duration: OUT * .7, easing:'ease-in', fill:'forwards'}));
    d._anims = [shrink, ...fades];
    shrink.onfinish = ()=>{
      if(!d._closing) return;
      d._closing = false; d.classList.remove('closing');
      d.open = false;
      stop(d);
    };
  });
})();
