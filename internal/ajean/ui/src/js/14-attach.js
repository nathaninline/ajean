// ===== Pièces jointes du chat ==============================================
// Un fichier joint n'est PAS lu par le navigateur ni injecté dans le contexte :
// il est déposé tel quel dans le dossier de travail de l'IA (uploads/, côté
// serveur), et seul son chemin est annoncé dans le message. Le modèle l'ouvre
// s'il en a besoin, avec ses outils — ce qui marche pour n'importe quel type de
// fichier sans que l'UI ait à en connaître le format.
//
// Le transfert passe par du base64 dans du JSON (et non du multipart) : c'est la
// seule forme qui traverse le tunnel E2E d'app.ajean.link, donc l'accès distant
// fonctionne sans code particulier.
// Doivent rester alignés sur uploadMaxBytes / uploadChunkMax (web_upload.go).
// Le fichier part par MORCEAUX : ni le navigateur ni le serveur n'en tiennent
// plus d'un à la fois en mémoire, ce qui rend le gigaoctet possible. Envoyer
// 1 Go en un seul corps JSON demanderait ~1,4 Go de base64 de chaque côté.
const ATTACH_MAX = 1024*1024*1024;
const ATTACH_CHUNK = 8*1024*1024;
// Délai par MORCEAU (et pas les 30 s d'un appel /api/* ordinaire) : une tranche de
// 8 Mo, base64 + tunnel E2E, peut dépasser 30 s sur une liaison lente.
const UPLOAD_CHUNK_TIMEOUT_MS = 120000;
let ATTACH = [];
let ATTACH_SEQ = 0;

function fmtSize(n){
  if(n >= 1024*1024*1024) return (n/(1024*1024*1024)).toFixed(1)+' Go';
  if(n >= 1024*1024) return (n/(1024*1024)).toFixed(n >= 10*1024*1024 ? 0 : 1)+' Mo';
  if(n >= 1024) return Math.round(n/1024)+' Ko';
  return n+' o';
}
// Est-ce une image ? (pour montrer une vignette plutôt que le seul nom.)
const IMG_RE=/\.(png|jpe?g|gif|webp|bmp|svg|avif|heic|heif)$/i;
function isImageName(n){ return IMG_RE.test(String(n||'')); }
// Famille d'un fichier d'après son extension, pour choisir son icône.
function fileKind(name){
  const e=(String(name||'').split('.').pop()||'').toLowerCase();
  if(e==='pdf') return 'pdf';
  if(/^(zip|rar|7z|tar|gz|tgz|bz2|xz|zst)$/.test(e)) return 'archive';
  if(/^(mp3|wav|ogg|flac|m4a|aac|opus)$/.test(e)) return 'audio';
  if(/^(mp4|mov|mkv|webm|avi|m4v)$/.test(e)) return 'video';
  if(/^(xlsx?|csv|tsv|ods|numbers)$/.test(e)) return 'sheet';
  if(/^(docx?|odt|rtf|pages|pptx?|odp|key)$/.test(e)) return 'doc';
  if(/^(js|ts|jsx|tsx|py|go|rs|c|h|cpp|hpp|cs|java|kt|swift|rb|php|sh|ps1|bat|json|ya?ml|toml|xml|html?|css|sql|lua|ini|env)$/.test(e)) return 'code';
  if(/^(txt|md|log|rst)$/.test(e)) return 'text';
  return 'file';
}
// Icônes au trait (currentColor : elles suivent le thème).
const FILE_ICON_BODY = {
  file:'', text:'<path d="M9 13h6M9 17h6"/>', pdf:'<path d="M9 15h1.5a1.5 1.5 0 0 0 0-3H9v5"/><path d="M14 12v5"/>',
  doc:'<path d="M8.5 12.5l1 4.5 1.5-3.5 1.5 3.5 1-4.5"/>', sheet:'<path d="M8 12h8v6H8zM8 15h8M12 12v6"/>',
  code:'<path d="M10 12.5l-2 2 2 2M14 12.5l2 2-2 2"/>', archive:'<path d="M12 9v1M12 11.5v1M12 14v1"/><rect x="11" y="16" width="2" height="2.2" rx=".5"/>',
  audio:'<path d="M10 17.5v-5l5-1.5v5"/><circle cx="9" cy="17.5" r="1.2"/><circle cx="14" cy="16" r="1.2"/>',
  video:'<path d="M10.5 12.5v5l4-2.5z"/>'
};
function fileIconSvg(name){
  const k=fileKind(name);
  return '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5"/>'+(FILE_ICON_BODY[k]||'')+'</svg>';
}
// Pastille de fichier, partagée par le composeur et les bulles du fil : même
// objet visuel des deux côtés, seul le contexte (CSS) change. Une IMAGE devient
// une tuile (imageTile) : la voir compte plus que lire son nom.
// `imgSrc` (URL déjà prête, ex. un objectURL du fichier local dans le composeur)
// ou `imgPath` (chemin dans le dossier de travail, chargé à la demande).
function fileChip(name, size, opts){
  opts=opts||{};
  if(isImageName(name) && (opts.imgSrc || opts.imgPath)) return imageTile(name, size, opts);
  const chip=document.createElement('div');
  chip.className='chip-file'+(opts.cls?' '+opts.cls:'');
  const ic=document.createElement('span');
  ic.className='cf-icon'; ic.innerHTML=fileIconSvg(name);
  const n=document.createElement('span');
  n.className='cf-name'; n.textContent=name; n.title=opts.title||name;
  const s=document.createElement('span');
  s.className='cf-size'; s.textContent=opts.sizeText||fmtSize(size);
  chip.appendChild(ic); chip.appendChild(n); chip.appendChild(s);
  if(opts.onRemove) chip.appendChild(removeBtn(name, opts.onRemove));
  return chip;
}
function removeBtn(name, fn){
  const x=document.createElement('button');
  x.type='button'; x.className='cf-x'; x.innerHTML='<svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>';
  x.title=t('attach.remove_title');
  x.setAttribute('aria-label',t('attach.remove_title')+' '+name);
  x.onclick=(e)=>{ e.stopPropagation(); fn(e); };
  return x;
}
// Tuile d'image : la vignette elle-même, cliquable pour l'ouvrir en grand
// (openLightbox). Reflet animé tant qu'elle charge, fondu à l'arrivée ; si le
// chargement échoue, la tuile retombe sur une icône et le nom du fichier.
function imageTile(name, size, opts){
  const tile=document.createElement('div');
  tile.className='img-tile'+(opts.cls?' '+opts.cls:'');
  tile.title=opts.title||name;
  tile.tabIndex=0; tile.setAttribute('role','button');
  tile.setAttribute('aria-label',t('lightbox.open_prefix')+name);
  const img=document.createElement('img');
  img.className='it-img'; img.alt=name; img.decoding='async'; img.draggable=false;
  img.dataset.name=name;
  if(opts.imgPath) img.dataset.path=opts.imgPath;
  img.onload=()=>tile.classList.add('loaded');
  img.onerror=()=>tileBroken(tile, name);
  tile.appendChild(img);
  const prog=document.createElement('div'); prog.className='it-prog'; tile.appendChild(prog);
  if(opts.onRemove) tile.appendChild(removeBtn(name, opts.onRemove));
  const open=()=>{ if(tile.classList.contains('loaded') && typeof openLightbox==='function') openLightbox(img); };
  tile.onclick=open;
  tile.onkeydown=(e)=>{ if(e.key==='Enter'||e.key===' '){ e.preventDefault(); open(); } };
  if(opts.imgFile){ img.dataset.full=opts.imgSrc||''; makeThumbURL(opts.imgFile).then(th=>{ if(th){ img.dataset.w=th.w; img.dataset.h=th.h; } img.src=(th&&th.url)?th.url:opts.imgSrc; }); }
  else if(opts.imgSrc) img.src=opts.imgSrc;
  else loadThumb(img, opts.imgPath, tile, name);
  return tile;
}
function tileBroken(tile, name){
  if(tile.classList.contains('broken')) return;
  tile.classList.add('broken');
  const f=document.createElement('div'); f.className='it-fallback';
  f.innerHTML=fileIconSvg(name)+'<span></span>'; f.lastChild.textContent=name;
  tile.appendChild(f);
}
// Fichiers joints à un message DÉJÀ envoyé : posés AU-DESSUS de la bulle, dans
// leur propre rangée. Les mettre dedans étirait la bulle vers le haut et donnait
// un bloc bâtard, moitié texte moitié pastilles.
function addMsgFiles(el, files){
  if(!el || !files || !files.length) return;
  const box=document.createElement('div');
  box.className='msg-files';
  // Plusieurs images : tuiles plus petites, en grille, pour ne pas faire un mur.
  if(files.filter(f=>isImageName(f.name)).length>1) box.classList.add('multi');
  for(const f of files) box.appendChild(fileChip(f.name, f.size, {title:f.name, imgPath:f.path||f.name}));
  el.parentNode.insertBefore(box, el);
  // Envoi sans un mot : la bulle serait un rectangle vide sous les pastilles.
  markEmptyMsg(el);
  return box;
}
// Une bulle sans texte n'a rien à montrer — seules ses pièces jointes parlent.
function markEmptyMsg(el){
  const b=el&&el.querySelector('.body');
  if(b) el.classList.toggle('empty', b.textContent==='');
}
// La rangée de fichiers précède la bulle : c'est là qu'on vérifie sa présence
// avant d'en ajouter une seconde (replay + bulle en attente).
function hasMsgFiles(el){
  const p=el&&el.previousElementSibling;
  return !!(p&&p.classList.contains('msg-files'));
}
// ===== Fichiers remis par l'IA ============================================
// Aucun outil, aucune syntaxe maison : l'IA écrit un lien Markdown ordinaire
// vers un fichier de son dossier de travail — [Télécharger](rapport.pdf) — et
// c'est l'UI qui le transforme en téléchargement. Le modèle n'a rien à savoir
// de plus que ce qu'il fait déjà naturellement.
//
// Un simple href ne suffirait pas : la clé de pilotage voyage dans un en-tête
// Authorization que le navigateur ne mettrait pas sur une navigation, et le
// chemin de base change derrière le tunnel (/u/<id>). D'où le fetch + blob.
const DL_CHUNK = 8*1024*1024;   // aligné sur downloadChunkMax (web_upload.go)
// Décode une tranche base64 en octets.
function b64ToBytes(s){
  const bin=atob(s), out=new Uint8Array(bin.length);
  for(let i=0;i<bin.length;i++) out[i]=bin.charCodeAt(i);
  return out;
}
// Récupère le fichier en tranches base64. C'est la voie OBLIGATOIRE derrière
// app.ajean.link : le proxy chiffré réemballe toute réponse en JSON, et du
// binaire n'y survit pas — on téléchargeait l'enveloppe JSON à la place du
// fichier. Le base64, lui, traverse intact.
async function fetchFileB64(path, size, chip){
  const parts=[]; let off=0;
  do{
    const r=await jfetch('/api/chat/file?b64=1&len='+DL_CHUNK+'&offset='+off+'&path='+encodeURIComponent(path));
    const j=await r.json();
    if(!r.ok || !j.ok) throw new Error(j.error||('HTTP '+r.status));
    const bytes=b64ToBytes(j.data||'');
    parts.push(bytes);
    // Une tranche vide sans marque de fin ne ferait pas avancer la boucle : on
    // s'arrête plutôt que de tourner indéfiniment sur le même octet.
    if(!bytes.length && !j.eof) throw new Error('transfert interrompu');
    off=(j.offset||0)+bytes.length;
    if(chip && size) chip.title=t('attach.download_progress_prefix')+Math.round(off*100/size)+t('attach.percent_suffix');
    if(j.eof) break;
  } while(off<size);
  return new Blob(parts);
}
// Ramène un fichier du dossier de travail sous forme de Blob, en choisissant
// tout seul la bonne voie : base64 par tranches derrière le tunnel E2E, corps
// binaire ordinaire sinon (voir downloadWorkspaceFile pour le pourquoi).
async function getWorkspaceBlob(path){
  const m=await jfetch('/api/chat/file?meta=1&path='+encodeURIComponent(path));
  const meta=await m.json().catch(()=>({}));
  if(!m.ok || !meta.ok) throw new Error(meta.error||('HTTP '+m.status));
  // e2e = tunnel chiffré ; remote = fichier sur un poste distant. Dans les deux cas
  // le binaire brut ne traverse pas : on passe par les tranches base64.
  if(meta.e2e || meta.remote) return await fetchFileB64(path, meta.size||0, null);
  const r=await jfetch('/api/chat/file?path='+encodeURIComponent(path));
  if(!r.ok) throw new Error('HTTP '+r.status);
  return await r.blob();
}
// Charge la vignette d'une image du fil. En cas d'échec on ne fait rien : la
// pastille reste avec son nom, comme avant. L'objectURL n'est pas révoqué —
// la vignette vit aussi longtemps que la bulle, et le fil n'en accumule pas des
// milliers.
async function loadThumb(img, path, tile, name){
  try{ setThumb(img, await getWorkspaceBlob(path)); }
  catch(_){ if(tile) tileBroken(tile, name||path); else img.remove(); }
}
// VRAIE vignette : l'image d'origine (une photo de 12 Mpx) était affichée telle
// quelle dans une tuile de 260 px — décodée et redessinée à chaque défilement, d'où
// les saccades. On la réduit UNE fois (createImageBitmap décode hors du fil
// principal) ; l'original reste disponible pour la visionneuse (data-full).
const THUMB_MAX = 560;
async function makeThumbURL(blob){
  if(typeof createImageBitmap!=='function' || !blob) return null;
  try{
    const bmp=await createImageBitmap(blob);
    const w=bmp.width, h=bmp.height;
    const s=Math.min(1, THUMB_MAX/Math.max(w,h));
    // Petite image déjà légère : inutile de la réencoder.
    if(s>=1 && blob.size<400*1024){ if(bmp.close) bmp.close(); return {url:null, w, h}; }
    const cw=Math.max(1,Math.round(w*s)), ch=Math.max(1,Math.round(h*s));
    const cv=document.createElement('canvas'); cv.width=cw; cv.height=ch;
    cv.getContext('2d').drawImage(bmp, 0, 0, cw, ch);
    if(bmp.close) bmp.close();
    // PNG/GIF/WebP peuvent être transparents : on garde un format qui le supporte.
    const type=/png|gif|webp/.test(blob.type||'') ? 'image/png' : 'image/jpeg';
    const out=await new Promise(res=>cv.toBlob(res, type, 0.85));
    return {url: out ? URL.createObjectURL(out) : null, w, h};
  }catch(_){ return null; }   // format non décodable ici (HEIC…) : on garde l'original
}
// Pose l'original (data-full) puis, dès qu'elle est prête, la vignette réduite.
async function setThumb(img, blob){
  const full=URL.createObjectURL(blob);
  img.dataset.full=full;
  const th=await makeThumbURL(blob);
  if(th){ img.dataset.w=th.w; img.dataset.h=th.h; }
  img.src=(th && th.url) ? th.url : full;
}
async function downloadWorkspaceFile(path, name, a){
  if(a) a.classList.add('busy');
  const wasTitle=a?a.title:'';
  try{
    // La fiche d'abord : elle est minuscule, elle passe partout, et elle dit si
    // on est derrière le tunnel — ce que le client ne peut pas deviner seul, le
    // proxy lui rendant des réponses JSON parfaitement ordinaires.
    const m=await jfetch('/api/chat/file?meta=1&path='+encodeURIComponent(path));
    const meta=await m.json().catch(()=>({}));
    if(!m.ok || !meta.ok){
      toast(t('attach.download_failed_prefix')+(meta.error||('HTTP '+m.status))); return;
    }
    let blob;
    if(meta.e2e || meta.remote){
      blob=await fetchFileB64(path, meta.size||0, a);
    } else {
      const r=await jfetch('/api/chat/file?path='+encodeURIComponent(path));
      if(!r.ok){
        let msg='HTTP '+r.status; try{ msg=(await r.json()).error||msg; }catch(_){}
        toast(t('attach.download_failed_prefix')+msg); return;
      }
      blob=await r.blob();
    }
    const blobURL=URL.createObjectURL(blob);
    const link=document.createElement('a');
    link.href=blobURL; link.download=name||meta.name||'fichier';
    document.body.appendChild(link); link.click(); link.remove();
    // Révocation différée : Safari annule le téléchargement si l'URL disparaît
    // dans la foulée du clic.
    setTimeout(()=>URL.revokeObjectURL(blobURL), 10000);
  }catch(e){ toast(t('attach.download_failed_prefix')+((e&&e.message)||t('attach.generic_error'))); }
  finally{ if(a){ a.classList.remove('busy'); a.title=wasTitle; } }
}
// Markdown refuse les espaces NON échappés dans la cible d'un lien : le modèle
// écrit [le rapport](mon rapport.pdf), et rien n'est rendu du tout — même pas un
// lien mort. Or les noms de fichiers à espaces sont la règle, pas l'exception. On
// les encode donc avant le rendu (fileLinkPath les redécode).
//
// ⚠️ Un VRAI titre Markdown — [x](chemin "Titre") — a ses espaces syntaxiques et
// doit être laissé tranquille. Mais on ne peut PAS détecter le titre par la simple
// présence d'un guillemet/apostrophe : les noms de fichiers français en contiennent
// (« Capture d'écran … »), et cette apostrophe désactivait alors l'encodage des
// espaces → lien coupé au premier espace → téléchargement impossible. Le titre se
// reconnaît à une chaîne guillemetée EN FIN de cible, précédée d'un espace
// (` "…"` / ` '…'`) ; une apostrophe au milieu du nom n'en est pas un.
const MD_LINK_TITLE_RE = /\s+(["'])(?:(?!\1)[\s\S])*\1\s*$/;
function encodeMdLinkSpaces(text){
  return String(text).replace(/\]\(([^)\n]*)\)/g, (whole, target)=>{
    if(target.indexOf(' ')<0) return whole;                   // pas d'espace : rien à faire
    if(/^[a-z][a-z0-9+.-]*:\/\//i.test(target)) return whole; // URL : pas notre affaire
    if(MD_LINK_TITLE_RE.test(target)) return whole;           // vrai titre : espaces syntaxiques
    return '](' + target.replace(/ /g, '%20') + ')';
  });
}
// Chemin visé par un lien de la réponse, ramené au dossier de travail — ou ''
// si le lien pointe ailleurs (page web, ancre, mailto…).
//
// Le modèle écrit indifféremment un chemin relatif, un chemin absolu du serveur,
// ou le préfixe `sandbox:` qu'il a vu ailleurs : les trois doivent marcher, on
// ne va pas lui demander d'être rigoureux sur un détail pareil.
function fileLinkPath(href){
  if(!href) return '';
  let h=href.trim();
  if(/^(https?:|mailto:|tel:|data:|blob:|#)/i.test(h)) return '';
  h=h.replace(/^sandbox:/i,'').replace(/^file:\/\//i,'');
  try{ h=decodeURI(h); }catch(_){}
  h=h.replace(/\\/g,'/');
  // Chemin absolu : on ne garde que ce qui suit le dossier de travail. Le serveur
  // revérifie de toute façon — c'est lui qui fait autorité sur le périmètre.
  const m=h.match(/(?:^|\/)workspace\/(.+)$/);
  if(m) h=m[1];
  h=h.replace(/^\/+/,'').replace(/^\.\//,'');
  if(!h || h.indexOf('..')>=0) return '';
  return h;
}
// Transforme en téléchargements les liens d'une réponse qui visent un fichier.
function markFileLinks(root){
  for(const a of root.querySelectorAll('a[href]')){
    const p=fileLinkPath(a.getAttribute('href'));
    if(!p) continue;
    a.classList.add('filelink');
    a.removeAttribute('target');
    a.setAttribute('href','#');
    const name=p.split('/').pop();
    a.title=t('attach.download_title_prefix')+name;
    a.onclick=(e)=>{ e.preventDefault(); downloadWorkspaceFile(p, name, a); };
  }
}
// Le modèle écrit parfois une capture en syntaxe IMAGE Markdown ![](fichier.png)
// (ex. browser_screenshot). Le src relatif ne pointe vers aucune URL servable →
// image cassée (petit carré « ? », vu par Nathan). On résout donc ces <img> vers
// le dossier de travail, comme markFileLinks le fait pour les liens : on récupère
// le blob (E2E-safe via getWorkspaceBlob) et on l'affiche inline.
const WS_IMG_CACHE = {}; // path -> objectURL, pour ne PAS re-télécharger à chaque re-render du streaming
function cssEsc(s){ try{ return CSS.escape(s); }catch(_){ return s.replace(/["\\]/g,'\\$&'); } }
function markWorkspaceImages(root){
  for(const img of root.querySelectorAll('img[src]')){
    const p=fileLinkPath(img.getAttribute('src'));
    if(!p) continue;
    img.classList.add('chat-img');
    if(!img.getAttribute('alt')) img.alt=p.split('/').pop();
    img.dataset.path=p; img.dataset.name=p.split('/').pop();
    if(!img._lb){ img._lb=true; img.addEventListener('click', ()=>{ if(img.naturalWidth && typeof openLightbox==='function') openLightbox(img); }); }
    if(WS_IMG_CACHE[p]){ img.dataset.full=WS_IMG_CACHE[p+'\u0000full']||WS_IMG_CACHE[p]; img.src=WS_IMG_CACHE[p]; continue; }
    img.removeAttribute('src');            // évite le flash « image cassée »
    img.setAttribute('data-wsimg', p);
    getWorkspaceBlob(p).then(async blob=>{
      const full=URL.createObjectURL(blob);
      const th=await makeThumbURL(blob);
      const url=(th&&th.url)?th.url:full; WS_IMG_CACHE[p]=url; WS_IMG_CACHE[p+'\u0000full']=full;
      // l'<img> a pu être recréé par un re-render du markdown : on recible par data-wsimg.
      document.querySelectorAll('img[data-wsimg="'+cssEsc(p)+'"]').forEach(i=>{ i.dataset.full=full; i.src=url; });
    }).catch(()=>{});
  }
}
function attachListEl(){ return document.getElementById('attach-list'); }
// renderAttach met la liste du composeur en phase avec ATTACH SANS la reconstruire :
// chaque fichier garde son élément (ATTACH_EL), seul son état change. Avant, tout
// était recréé à chaque avancée d'un envoi : les vignettes rechargeaient, les
// animations se rejouaient, la liste clignotait.
const ATTACH_EL = new Map();
function renderAttach(){
  const el = attachListEl(); if(!el) return;
  // Les pièces jointes comptent comme « contenu à envoyer » : le bouton envoyer/stop
  // doit se réévaluer (issue #74 : envoyer pendant une génération).
  if(typeof syncSendBtn==='function') syncSendBtn();
  const live=new Set(ATTACH.map(a=>a.id));
  // Sortants : ils s'effacent en douceur puis quittent le DOM.
  for(const [id, node] of ATTACH_EL){
    if(live.has(id)) continue;
    ATTACH_EL.delete(id);
    node.classList.add('leaving');
    let done=false;
    const gone=()=>{ if(done) return; done=true; node.remove(); if(!ATTACH.length && !el.querySelector('.leaving')) el.classList.remove('show'); };
    node.addEventListener('animationend', gone, {once:true});
    setTimeout(gone, 400);   // filet si l'animation est coupée (reduced motion…)
  }
  if(ATTACH.length) el.classList.add('show');
  for(const a of ATTACH){
    let node=ATTACH_EL.get(a.id);
    if(!node){
      node=fileChip(a.name, a.size, {
        imgSrc: a.thumb, imgFile: a.file,
        onRemove: ()=>{ releaseThumbLater(a); ATTACH=ATTACH.filter(o=>o.id!==a.id); renderAttach(); }
      });
      node.classList.add('entering');
      node.addEventListener('animationend', ()=>node.classList.remove('entering'), {once:true});
      ATTACH_EL.set(a.id, node);
      el.appendChild(node);
    }
    node.classList.toggle('up', a.state==='up');
    node.classList.toggle('err', a.state==='err');
    node.title = a.error || a.name;
    // Un gros fichier prend du temps : on montre l'avancement plutôt qu'un
    // anneau qui tourne sans rien dire. Sous un morceau, il n'y a rien à suivre.
    const pct = a.state==='up' ? (a.size>ATTACH_CHUNK ? Math.round((a.sent||0)*100/a.size) : -1) : 100;
    node.style.setProperty('--p', Math.max(0,pct));
    node.classList.toggle('indet', pct<0);
    const sz=node.querySelector('.cf-size');
    if(sz) sz.textContent = a.state==='err' ? t('attach.failed') : (pct>=0 && a.state==='up') ? pct+t('attach.percent_suffix') : fmtSize(a.size);
  }
}
// L'objectURL d'une vignette du composeur tient un blob en mémoire : on le libère
// dès que le fichier quitte la liste (retrait ou envoi terminé).
function releaseThumb(a){ if(a&&a.thumb){ try{ URL.revokeObjectURL(a.thumb); }catch(_){} a.thumb=null; } }
function releaseThumbLater(a){ const u=a&&a.thumb; if(!u) return; a.thumb=null; setTimeout(()=>{ try{ URL.revokeObjectURL(u); }catch(_){} }, 600); }
function clearAttach(){ for(const a of ATTACH) releaseThumbLater(a); ATTACH=[]; renderAttach(); }
// Ce qui part avec le message, pour l'afficher dans sa bulle.
function attachSent(){ return ATTACH.filter(a=>a.state==='ok').map(a=>({name:a.name,size:a.size,path:a.path})); }

// Lecture en base64. readAsDataURL préfixe par "data:<mime>;base64," — le
// serveur sait retirer cet en-tête, on n'a donc rien à découper ici.
function readAsBase64(file){
  return new Promise((res,rej)=>{
    const fr=new FileReader();
    fr.onload=()=>res(String(fr.result||''));
    fr.onerror=()=>rej(new Error('lecture impossible'));
    fr.readAsDataURL(file);
  });
}
// Envoie UN morceau. VIA jfetch, pas un fetch brut : derrière app.ajean.link,
// jfetch est réécrit pour traverser la boîte noire E2E — un POST direct vers
// /api/chat/upload n'atteint PAS ton serveur (le relais est aveugle) et l'envoi
// échouait « impossible d'envoyer ». On fournit notre PROPRE signal pour ne pas
// hériter du plafond de 30 s de jfetch (calibré pour de petits appels), un morceau
// pouvant être plus long ; le download passe déjà par jfetch, on symétrise.
async function sendChunk(payload){
  const ac=new AbortController();
  const timer=setTimeout(()=>ac.abort(), UPLOAD_CHUNK_TIMEOUT_MS);
  try{
    const r=await jfetch('/api/chat/upload',{
      method:'POST', headers:{'Content-Type':'application/json'},
      body:JSON.stringify(payload), signal:ac.signal
    });
    const j=await r.json().catch(()=>({}));
    if(!r.ok || !j.ok) throw new Error(j.error||('erreur '+r.status));
    return j;
  } finally { clearTimeout(timer); }
}
// Le dépôt n'a lieu qu'à l'ENVOI du message, jamais à l'ajout : tant qu'on n'a
// pas cliqué, rien ne doit atterrir dans le dossier de travail de l'IA. Retirer
// un fichier de la liste ne laisse donc aucune trace sur le disque.
//
// Découpage en morceaux de 8 Mo : le navigateur ne lit qu'une tranche à la fois
// (Blob.slice), le serveur l'écrit et l'oublie. Un fichier d'un gigaoctet passe
// donc sans que personne ne le tienne entier en mémoire — et on sait dire où on
// en est, ce qu'un envoi monolithique ne permettait pas.
async function uploadAttach(a){
  a.state='up'; a.sent=0; renderAttach();
  try{
    let id='';
    // `do…while` et non `while` : un fichier plus petit qu'un morceau doit quand
    // même faire un tour de boucle, sinon rien ne part.
    let off=0;
    do{
      const end=Math.min(off+ATTACH_CHUNK, a.size);
      const data=await readAsBase64(a.file.slice(off, end));
      const last=end>=a.size;
      // `size` au premier morceau : le serveur vérifie la place disque AVANT
      // d'entamer un envoi d'un gigaoctet, plutôt que de le découvrir à la fin.
      const j=await sendChunk({name:a.name, data:data, id:id, more:!last, size:(id?0:a.size)});
      if(j.id) id=j.id;
      off=end;
      a.sent=off; renderAttach();
      if(last){ a.path=j.path; }
    } while(off<a.size);
    a.state='ok'; a.file=null;   // le contenu ne sert plus à rien côté page
  }catch(e){
    a.state='err'; a.error=(e&&e.message)||t('attach.failed');
    toast(t('attach.quote_open')+a.name+t('attach.quote_close')+' : '+a.error);
  }
  renderAttach();
}
function addFiles(files){
  for(const f of files||[]){
    if(f.size>ATTACH_MAX){ toast(t('attach.quote_open')+f.name+t('attach.quote_close')+t('attach.too_big_middle')+fmtSize(f.size)+t('attach.too_big_max')+fmtSize(ATTACH_MAX)); continue; }
    if(!f.size){ toast(t('attach.quote_open')+f.name+t('attach.quote_close')+t('attach.empty_middle')); continue; }
    const rec={id:++ATTACH_SEQ, name:f.name||t('attach.unnamed_file'), size:f.size, file:f, path:null, state:'queued'};
    // Vignette immédiate depuis le fichier local, sans le renvoyer : la même image
    // qu'on verra dans la bulle une fois envoyée.
    if(isImageName(rec.name)){ try{ rec.thumb=URL.createObjectURL(f); }catch(_){} }
    ATTACH.push(rec);
  }
  renderAttach();
}
function onAttachPick(e){
  addFiles(e.target.files);
  e.target.value='';   // sinon re-choisir le même fichier ne déclenche pas change
}
// Coller un fichier (capture d'écran, fichier copié depuis l'explorateur). On ne
// touche à rien quand le presse-papier ne contient que du texte : le collage
// normal dans la zone de saisie doit rester intact.
function onPaste(e){
  const items=(e.clipboardData&&e.clipboardData.files)||[];
  if(!items.length) return;
  e.preventDefault();
  addFiles(items);
}
// Glisser-déposer sur toute la fenêtre. Le compteur de profondeur est nécessaire :
// dragenter/dragleave se déclenchent aussi au passage d'un enfant à l'autre, et
// sans lui le voile clignote dès que le curseur traverse une bulle du fil.
let DRAG_DEPTH=0;
function dropVeil(on){
  const v=document.getElementById('dropveil'); if(v) v.classList.toggle('show', on);
}
function hasFiles(e){
  const dt=e.dataTransfer; if(!dt) return false;
  if(dt.types) for(const t of dt.types){ if(t==='Files') return true; }
  return false;
}
window.addEventListener('dragenter', e=>{ if(!hasFiles(e)) return; e.preventDefault(); DRAG_DEPTH++; dropVeil(true); });
window.addEventListener('dragover', e=>{ if(hasFiles(e)) e.preventDefault(); });
window.addEventListener('dragleave', e=>{ if(!hasFiles(e)) return; DRAG_DEPTH=Math.max(0,DRAG_DEPTH-1); if(!DRAG_DEPTH) dropVeil(false); });
window.addEventListener('drop', e=>{
  if(!hasFiles(e)) return;
  e.preventDefault(); DRAG_DEPTH=0; dropVeil(false);
  addFiles(e.dataTransfer.files);
});
// Appelé par send() : c'est ICI que les fichiers partent réellement vers le
// serveur. Ceux qui échouent sont ignorés — leur pastille reste à l'écran en
// rouge, elle dit déjà ce qui s'est passé.
async function attachPaths(){
  await Promise.all(ATTACH.filter(a=>a.state==='queued').map(uploadAttach));
  return ATTACH.filter(a=>a.state==='ok'&&a.path).map(a=>a.path);
}
renderAttach();
