// Clé de pilotage : envoyée en Authorization: Bearer sur chaque appel /api/*.
// Sur un 401 on la (re)demande et on rejoue la requête. Stockée en localStorage.
let TOKEN = localStorage.getItem('jean.key') || '';
function authHeaders(h){ h = Object.assign({}, h||{}); if(TOKEN) h['Authorization']='Bearer '+TOKEN; return h; }
// Base d'URL : "" en local (page à "/"), "/u/<id>" servie via le relais ajean.link.
// Garde les chemins absolus (/api/…) corrects derrière le tunnel.
const API_BASE = location.pathname.replace(/\/(index\.html)?$/, '');
// Délai maximal d'un appel /api/* ordinaire. Sans lui, une requête que le
// navigateur met en file d'attente (plafond de ~6 connexions par domaine, atteint
// dès qu'on laisse traîner plusieurs onglets Jean) reste suspendue POUR TOUJOURS :
// ni réponse, ni erreur, et une UI figée sur « chargement… » sans rien à afficher.
// Mieux vaut une erreur franche. Les flux longs (SSE) passent leur propre signal
// et ne sont donc jamais concernés.
const API_TIMEOUT_MS = 30000;
async function jfetch(u, opts){
  opts = opts || {};
  opts.headers = authHeaders(opts.headers);
  if(u.charAt(0) === '/') u = API_BASE + u;
  let timer=null;
  if(!opts.signal){
    const ac=new AbortController();
    opts.signal=ac.signal;
    timer=setTimeout(()=>ac.abort(), API_TIMEOUT_MS);
  }
  let r;
  try{ r = await fetch(u, opts); }
  catch(e){
    if(e && e.name==='AbortError'){ throw new Error('requête sans réponse après 30 s (trop d\'onglets AJEAN ouverts ?)'); }
    throw e;
  }
  finally{ if(timer) clearTimeout(timer); }
  if(r.status === 401){
    const k = await askPrompt('Clé de pilotage jean requise :', {title:'Authentification', placeholder:'clé…'});
    if(k){ TOKEN = k.trim(); localStorage.setItem('jean.key', TOKEN); opts.headers = authHeaders(opts.headers); r = await fetch(u, opts); }
  }
  return r;
}
async function jget(u){ const r=await jfetch(u); return r.json(); }
async function jpost(u,b){ const r=await jfetch(u,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(b||{})}); return r.json(); }
