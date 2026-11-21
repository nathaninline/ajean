// Clé de pilotage : envoyée en Authorization: Bearer sur chaque appel /api/*.
// Sur un 401 on la (re)demande et on rejoue la requête. Stockée en localStorage.
let TOKEN = localStorage.getItem('ajean.key') || '';
function authHeaders(h){ h = Object.assign({}, h||{}); if(TOKEN) h['Authorization']='Bearer '+TOKEN; return h; }
// Base d'URL : "" en local (page à "/"), "/u/<id>" servie via le relais ajean.link.
// Garde les chemins absolus (/api/…) corrects derrière le tunnel.
const API_BASE = location.pathname.replace(/\/(index\.html)?$/, '');
// Délai maximal d'un appel /api/* ordinaire. Sans lui, une requête que le
// navigateur met en file d'attente (plafond de ~6 connexions par domaine, atteint
// dès qu'on laisse traîner plusieurs onglets AJEAN) reste suspendue POUR TOUJOURS :
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
    if(e && e.name==='AbortError'){ throw new Error(t('api.timeout_error')); }
    throw e;
  }
  finally{ if(timer) clearTimeout(timer); }
  if(r.status === 401){
    // Le serveur dit quoi demander : « login » = un mot de passe protège
    // l'interface (session par cookie), sinon c'est la clé de pilotage.
    let login = false;
    try{ const j = await r.clone().json(); login = !!(j && j.login); }catch(_){}
    const k = login ? await askLoginOnce() : await askKeyOnce();
    if(k){ opts.headers = authHeaders(opts.headers); r = await fetch(u, opts); }
  }
  return r;
}
// askLoginOnce : UNE seule demande de mot de passe à la fois (même raison que
// askKeyOnce). Le mot de passe part au serveur, qui répond par un cookie de
// session HttpOnly : rien n'est gardé côté page, ni mot de passe ni jeton.
let _loginAsk = null;
function askLoginOnce(){
  if(!_loginAsk){
    _loginAsk = (async()=>{
      let msg = t('auth.login_msg');
      for(;;){
        const pw = await askPrompt(msg, {title:t('auth.login_title'), placeholder:t('auth.password_placeholder'), okText:t('auth.login_ok'), password:true});
        if(!pw) return false;
        let r;
        try{ r = await fetch(API_BASE+'/api/auth/login', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({password:pw})}); }
        catch(e){ msg = t('auth.login_unreachable'); continue; }
        if(r.ok) return true;
        let j = {}; try{ j = await r.json(); }catch(_){}
        if(r.status === 429) msg = t('auth.login_wait_prefix') + (j.wait||60) + t('auth.login_wait_suffix');
        else if(r.status === 401) msg = t('auth.login_wrong');
        else if(r.status === 409) return false; // mot de passe retiré entre-temps : on rejoue sans
        else msg = j.error || ('HTTP ' + r.status);
      }
    })().then(v => { _loginAsk = null; return v; }, e => { _loginAsk = null; throw e; });
  }
  return _loginAsk;
}
// askKeyOnce : UNE seule demande de clé à la fois, partagée par tous les appels.
//
// Le chargement de l'UI lance une dizaine d'appels /api/* en parallèle, et le
// rafraîchissement périodique en relance autant. Sans ce partage, CHAQUE 401
// rouvrait la modale, laquelle vide son champ à l'ouverture : impossible de
// taper la clé, les caractères disparaissaient au fur et à mesure. Les appels
// concurrents attendent désormais la même promesse, et repartent tous avec la
// clé saisie une seule fois.
let _keyAsk = null;
function askKeyOnce(){
  if(!_keyAsk){
    _keyAsk = askPrompt(t('api.key_required_msg'), {title:t('api.auth_title'), placeholder:t('api.key_placeholder')})
      .then(k => {
        _keyAsk = null;
        if(k){ TOKEN = k.trim(); localStorage.setItem('ajean.key', TOKEN); }
        return k;
      }, e => { _keyAsk = null; throw e; });
  }
  return _keyAsk;
}
async function jget(u){ const r=await jfetch(u); return r.json(); }
async function jpost(u,b){ const r=await jfetch(u,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(b||{})}); return r.json(); }
