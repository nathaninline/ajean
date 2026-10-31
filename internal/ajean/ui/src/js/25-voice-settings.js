// ===== Paramètres > Voix de Jean ==============================================
// Réglages du mode vocal (24-jean-voice.js), rangés côté serveur (voice.go) :
// interrupteur, serveur audio, voix, sensibilité du micro, pause avant envoi,
// et la bibliothèque de voix (création à partir d'une description, écoute,
// suppression). Chaque changement prévient le mode vocal (« voice-settings »).
let VOICE_SET=null;
const _voicePlayer=new Audio();

function _voicePlay(b64){
  try{
    const bin=atob(b64), u8=new Uint8Array(bin.length);
    for(let i=0;i<bin.length;i++) u8[i]=bin.charCodeAt(i);
    const url=URL.createObjectURL(new Blob([u8], {type:'audio/wav'}));
    _voicePlayer.onended=()=>URL.revokeObjectURL(url);
    _voicePlayer.src=url; _voicePlayer.play().catch(()=>{});
  }catch(_){}
}

function _voiceFill(s){
  VOICE_SET=s;
  const $=id=>document.getElementById(id);
  if(!$('voice-block')) return;
  $('voice-toggle').checked=!!s.on;
  const url=$('voice-url');
  if(document.activeElement!==url) url.value=s.url||'';
  url.placeholder=s.url_default||'http://127.0.0.1:8095';
  const sel=$('voice-name');
  const names=(s.voices||[]).slice();
  if(s.voice && !names.includes(s.voice)) names.unshift(s.voice);
  sel.replaceChildren(...names.map(n=>{ const o=document.createElement('option'); o.value=o.textContent=n; return o; }));
  if(!names.length){ const o=document.createElement('option'); o.value=''; o.textContent=t('voice.none'); sel.appendChild(o); }
  sel.value=s.voice||'';
  $('voice-sens').value=String(s.sens||2);
  const sil=$('voice-silence');
  if(![...sil.options].some(o=>o.value===String(s.silence_ms))){ const o=document.createElement('option'); o.value=String(s.silence_ms); o.textContent=(s.silence_ms/1000).toLocaleString()+' s'; sil.appendChild(o); }
  sil.value=String(s.silence_ms||800);
  $('voice-del').disabled=!(s.voices||[]).includes(s.voice);
  const configured=!!(s.url||s.url_default);
  setBadge('voice-badge', !configured || !s.on ? false : s.reachable ? true : 'warn',
    !configured || !s.on ? t('voice.badge_off') : s.reachable ? t('voice.badge_ok') : t('voice.badge_down'));
}

async function loadVoiceSettings(){
  try{ const r=await jfetch('/api/voice/settings'); if(r.ok) _voiceFill(await r.json()); }catch(_){}
}

async function voiceSave(patch){
  try{
    const r=await jfetch('/api/voice/settings', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify(patch)});
    const j=await r.json(); if(!r.ok) throw new Error(j.error||r.status);
    _voiceFill(j);
    window.dispatchEvent(new Event('voice-settings'));
  }catch(e){ toast(t('voice.error').replace('{msg}', e.message)); loadVoiceSettings(); }
}

async function voiceTest(){
  const btn=document.querySelector('#voice-block .voice-play'); if(btn) btn.disabled=true;
  try{
    const voice=document.getElementById('voice-name').value;
    const r=await jfetch('/api/voice/tts', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({text:t('voice.test_text'), voice})});
    const j=await r.json(); if(!r.ok) throw new Error(j.error||r.status);
    _voicePlay(j.wav);
  }catch(e){ toast(t('voice.error').replace('{msg}', e.message)); }
  finally{ if(btn) btn.disabled=false; }
}

async function voiceCreate(){
  const desc=await askPrompt(t('voice.create_desc'), {title:t('voice.create_title'), placeholder:t('voice.create_desc_ph')});
  if(!desc || !desc.trim()) return;
  const raw=await askPrompt(t('voice.create_name'), {title:t('voice.create_title'), placeholder:t('voice.create_name_ph')});
  if(!raw || !raw.trim()) return;
  const name=raw.trim().toLowerCase().normalize('NFD').replace(/[̀-ͯ]/g,'').replace(/[^a-z0-9_-]+/g,'-').replace(/^-+|-+$/g,'').slice(0,32);
  toast(t('voice.creating'));
  try{
    const r=await jfetch('/api/voice/design', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({name, description:desc.trim()})});
    const j=await r.json(); if(!r.ok) throw new Error(j.error||r.status);
    _voicePlay(j.wav);
    toast(t('voice.created').replace('{name}', j.name));
    await voiceSave({voice:j.name});
  }catch(e){ toast(t('voice.error').replace('{msg}', e.message)); }
}

async function voiceDelete(){
  const name=document.getElementById('voice-name').value;
  if(!name || !(VOICE_SET && (VOICE_SET.voices||[]).includes(name))) return;
  if(!await askConfirm(t('voice.delete_confirm').replace('{name}', name))) return;
  try{
    const r=await jfetch('/api/voice/delete', {method:'POST', headers:{'Content-Type':'application/json'}, body:JSON.stringify({name})});
    const j=await r.json(); if(!r.ok) throw new Error(j.error||r.status);
    const left=j.voices||[];
    await voiceSave({voice: left[0]||''});
  }catch(e){ toast(t('voice.error').replace('{msg}', e.message)); }
}

if(document.readyState==='loading') document.addEventListener('DOMContentLoaded', loadVoiceSettings); else loadVoiceSettings();
