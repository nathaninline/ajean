// Clé de pilotage : envoyée en Authorization: Bearer sur chaque appel /api/*.
// Sur un 401 on la (re)demande et on rejoue la requête. Stockée en localStorage.
let TOKEN = localStorage.getItem('jean.key') || '';
function authHeaders(h){ h = Object.assign({}, h||{}); if(TOKEN) h['Authorization']='Bearer '+TOKEN; return h; }
// Base d'URL : "" en local (page à "/"), "/u/<id>" servie via le relais ajean.link.
// Garde les chemins absolus (/api/…) corrects derrière le tunnel.
const API_BASE = location.pathname.replace(/\/(index\.html)?$/, '');
async function jfetch(u, opts){
  opts = opts || {};
  opts.headers = authHeaders(opts.headers);
  if(u.charAt(0) === '/') u = API_BASE + u;
  let r = await fetch(u, opts);
  if(r.status === 401){
    const k = await askPrompt('Clé de pilotage jean requise :', {title:'Authentification', placeholder:'clé…'});
    if(k){ TOKEN = k.trim(); localStorage.setItem('jean.key', TOKEN); opts.headers = authHeaders(opts.headers); r = await fetch(u, opts); }
  }
  return r;
}
async function jget(u){ const r=await jfetch(u); return r.json(); }
async function jpost(u,b){ const r=await jfetch(u,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(b||{})}); return r.json(); }
