// 17-push.js — notifications Web Push côté client (voir push.go / sw.js).
//
// Le SERVEUR pousse une notif à la fin d'un tour utilisateur, même app fermée /
// iPhone verrouillé. Ici on gère juste l'INSCRIPTION : enregistrer le service
// worker, demander la permission (sur clic — geste utilisateur obligatoire),
// s'abonner avec la clé publique VAPID du serveur, et transmettre l'abonnement.
//
// ⚠️ Le service worker est un fichier de l'ORIGINE (/sw.js), pas un appel /api :
// en local et sur app.ajean.link (GitHub Pages) il est servi à la racine. La clé
// VAPID et l'enregistrement de l'abonnement, eux, passent par jfetch (donc E2E).

// pushSupported : le navigateur sait-il faire du Web Push ? (Safari macOS < 16.1,
// iOS Safari hors PWA installée, vieux navigateurs → non.)
function pushSupported(){
  return ('serviceWorker' in navigator) && ('PushManager' in window) && ('Notification' in window);
}

// La clé VAPID est renvoyée en base64url ; PushManager.subscribe veut un Uint8Array.
function urlB64ToUint8Array(base64){
  const pad = '='.repeat((4 - base64.length % 4) % 4);
  const b64 = (base64 + pad).replace(/-/g, '+').replace(/_/g, '/');
  const raw = atob(b64);
  const out = new Uint8Array(raw.length);
  for(let i=0;i<raw.length;i++) out[i]=raw.charCodeAt(i);
  return out;
}

// Enregistre (une seule fois) le service worker et renvoie sa registration.
// Scope racine : le worker doit couvrir toute l'app pour recevoir les push.
let _swReg = null;
async function pushRegisterSW(){
  if(_swReg) return _swReg;
  _swReg = await navigator.serviceWorker.register('/sw.js', {scope:'/'});
  return _swReg;
}

function pushSetStatus(msg){
  const el = document.getElementById('push-status');
  if(el) el.textContent = msg || '';
}

// Reflète l'état réel (abonné ou non, permission refusée, non supporté) dans
// l'interrupteur + la ligne d'état. Appelé au chargement et après chaque bascule.
async function pushRefresh(){
  const cb = document.getElementById('push-toggle');
  if(!cb) return;
  if(!pushSupported()){
    cb.checked = false; cb.disabled = true;
    // iOS ne sait faire du Web Push QUE depuis une PWA ajoutée à l'écran d'accueil.
    if(document.documentElement.getAttribute('data-pwa')!=='1' && /iphone|ipad|ipod/i.test(navigator.userAgent))
      pushSetStatus('Sur iPhone : ajoute d\'abord AJEAN à l\'écran d\'accueil, puis rouvre-le depuis l\'icône pour activer les notifications.');
    else
      pushSetStatus('Ce navigateur ne prend pas en charge les notifications.');
    return;
  }
  if(Notification.permission === 'denied'){
    cb.checked = false; cb.disabled = false;
    pushSetStatus('Notifications bloquées dans les réglages du navigateur — réautorise-les pour ce site.');
    return;
  }
  try{
    const reg = await pushRegisterSW();
    const sub = await reg.pushManager.getSubscription();
    cb.checked = !!sub; cb.disabled = false;
    pushSetStatus(sub ? 'Activées : une notif à chaque réponse terminée.' : '');
  }catch(e){ cb.checked=false; pushSetStatus(''); }
}

// togglePush : abonne ou désabonne selon l'état de l'interrupteur.
async function togglePush(){
  const cb = document.getElementById('push-toggle');
  const want = cb.checked;
  if(!pushSupported()){ cb.checked=false; await pushRefresh(); return; }
  try{
    if(want){
      // Permission (geste utilisateur = ce clic). Refus → on éteint et on explique.
      const perm = await Notification.requestPermission();
      if(perm !== 'granted'){ cb.checked=false; pushSetStatus('Permission refusée — rien ne sera envoyé.'); return; }
      const reg = await pushRegisterSW();
      let sub = await reg.pushManager.getSubscription();
      if(!sub){
        const r = await jget('/api/push/key');
        if(!r || !r.key){ cb.checked=false; pushSetStatus('Clé serveur indisponible — réessaie.'); return; }
        sub = await reg.pushManager.subscribe({
          userVisibleOnly: true,               // exigé par Chrome : pas de push silencieux
          applicationServerKey: urlB64ToUint8Array(r.key)
        });
      }
      const res = await jpost('/api/push/subscribe', sub.toJSON());
      if(!res || !res.ok){ pushSetStatus('Échec de l\'enregistrement côté serveur.'); }
      else pushSetStatus('Activées : une notif à chaque réponse terminée.');
      toast('notifications activées');
    } else {
      const reg = await pushRegisterSW();
      const sub = await reg.pushManager.getSubscription();
      if(sub){
        const ep = sub.endpoint;
        await sub.unsubscribe().catch(()=>{});
        await jpost('/api/push/unsubscribe', {endpoint:ep}).catch(()=>{});
      }
      pushSetStatus('');
      toast('notifications coupées');
    }
  }catch(e){
    cb.checked = !want;
    pushSetStatus('Erreur : ' + (e && e.message ? e.message : e));
  }
}

// Au chargement : enregistre le SW en avance (pour recevoir les push même sans
// ouvrir les réglages) et cale l'interrupteur. Silencieux si non supporté.
if(pushSupported()){
  navigator.serviceWorker.register('/sw.js', {scope:'/'}).then(r=>{ _swReg=r; }).catch(()=>{});
}
document.addEventListener('DOMContentLoaded', ()=>{ pushRefresh(); });
