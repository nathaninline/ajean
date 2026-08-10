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
const ATTACH_MAX = 24*1024*1024;   // doit rester aligné sur uploadMaxBytes (web_upload.go)
let ATTACH = [];
let ATTACH_SEQ = 0;

function fmtSize(n){
  if(n >= 1024*1024) return (n/(1024*1024)).toFixed(n >= 10*1024*1024 ? 0 : 1)+' Mo';
  if(n >= 1024) return Math.round(n/1024)+' Ko';
  return n+' o';
}
// Pastille de fichier, partagée par le composeur et les bulles du fil : même
// objet visuel des deux côtés, seul le contexte (CSS) change.
function fileChip(name, size, opts){
  opts=opts||{};
  const chip=document.createElement('div');
  chip.className='chip-file'+(opts.cls?' '+opts.cls:'');
  const n=document.createElement('span');
  n.className='cf-name'; n.textContent=name; n.title=opts.title||name;
  const s=document.createElement('span');
  s.className='cf-size'; s.textContent=opts.sizeText||fmtSize(size);
  chip.appendChild(n); chip.appendChild(s);
  if(opts.onRemove){
    const x=document.createElement('button');
    x.type='button'; x.textContent='×'; x.title='retirer';
    x.setAttribute('aria-label','retirer '+name);
    x.onclick=opts.onRemove;
    chip.appendChild(x);
  }
  return chip;
}
// Fichiers joints à un message DÉJÀ envoyé : posés AU-DESSUS de la bulle, dans
// leur propre rangée. Les mettre dedans étirait la bulle vers le haut et donnait
// un bloc bâtard, moitié texte moitié pastilles.
function addMsgFiles(el, files){
  if(!el || !files || !files.length) return;
  const box=document.createElement('div');
  box.className='msg-files';
  for(const f of files) box.appendChild(fileChip(f.name, f.size, {title:f.path||f.name}));
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
async function downloadWorkspaceFile(path, name, a){
  if(a) a.classList.add('busy');
  try{
    const r=await jfetch('/api/chat/file?path='+encodeURIComponent(path));
    if(!r.ok){
      let m='HTTP '+r.status; try{ m=(await r.json()).error||m; }catch(_){}
      toast('téléchargement impossible : '+m); return;
    }
    const blobURL=URL.createObjectURL(await r.blob());
    const link=document.createElement('a');
    link.href=blobURL; link.download=name||'fichier';
    document.body.appendChild(link); link.click(); link.remove();
    // Révocation différée : Safari annule le téléchargement si l'URL disparaît
    // dans la foulée du clic.
    setTimeout(()=>URL.revokeObjectURL(blobURL), 10000);
  }catch(e){ toast('téléchargement impossible : '+((e&&e.message)||'erreur')); }
  finally{ if(a) a.classList.remove('busy'); }
}
// Markdown refuse les espaces NON échappés dans la cible d'un lien : le modèle
// écrit [le rapport](mon rapport.pdf), et rien n'est rendu du tout — même pas un
// lien mort. Or les noms de fichiers à espaces sont la règle, pas l'exception. On
// les encode donc avant le rendu (fileLinkPath les redécode).
//
// Les cibles avec un titre — [x](chemin "Titre") — sont laissées tranquilles :
// leurs espaces sont syntaxiques.
function encodeMdLinkSpaces(text){
  return String(text).replace(/\]\(([^)\n]*)\)/g, (whole, target)=>{
    if(target.indexOf(' ')<0 || /["']/.test(target)) return whole;
    if(/^[a-z][a-z0-9+.-]*:\/\//i.test(target)) return whole; // URL : pas notre affaire
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
    a.title='télécharger '+name;
    a.onclick=(e)=>{ e.preventDefault(); downloadWorkspaceFile(p, name, a); };
  }
}
function attachListEl(){ return document.getElementById('attach-list'); }
function renderAttach(){
  const el = attachListEl(); if(!el) return;
  el.innerHTML='';
  el.classList.toggle('show', ATTACH.length>0);
  for(const a of ATTACH){
    el.appendChild(fileChip(a.name, a.size, {
      cls: a.state==='up' ? 'up' : (a.state==='err' ? 'err' : ''),
      title: a.error || a.name,
      sizeText: a.state==='err' ? 'échec' : fmtSize(a.size),
      onRemove: ()=>{ ATTACH=ATTACH.filter(o=>o.id!==a.id); renderAttach(); }
    }));
  }
}
function clearAttach(){ ATTACH=[]; renderAttach(); }
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
// Le dépôt n'a lieu qu'à l'ENVOI du message, jamais à l'ajout : tant qu'on n'a
// pas cliqué, rien ne doit atterrir dans le dossier de travail de l'IA. Retirer
// un fichier de la liste ne laisse donc aucune trace sur le disque.
async function uploadAttach(a){
  a.state='up'; renderAttach();
  try{
    const data=await readAsBase64(a.file);
    // Pas de jfetch ici : son délai de 30 s est calibré pour de petits appels,
    // et couperait le transfert d'un fichier de plusieurs Mo.
    const r=await fetch(API_BASE+'/api/chat/upload',{
      method:'POST', headers:authHeaders({'Content-Type':'application/json'}),
      body:JSON.stringify({name:a.name, data:data})
    });
    const j=await r.json().catch(()=>({}));
    if(!r.ok || !j.ok) throw new Error(j.error||('erreur '+r.status));
    a.path=j.path; a.state='ok'; a.file=null;   // le contenu ne sert plus à rien côté page
  }catch(e){
    a.state='err'; a.error=(e&&e.message)||'échec';
    toast('« '+a.name+' » : '+a.error);
  }
  renderAttach();
}
function addFiles(files){
  for(const f of files||[]){
    if(f.size>ATTACH_MAX){ toast('« '+f.name+' » fait '+fmtSize(f.size)+' — maximum '+fmtSize(ATTACH_MAX)); continue; }
    if(!f.size){ toast('« '+f.name+' » est vide'); continue; }
    ATTACH.push({id:++ATTACH_SEQ, name:f.name||'fichier', size:f.size, file:f, path:null, state:'queued'});
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
