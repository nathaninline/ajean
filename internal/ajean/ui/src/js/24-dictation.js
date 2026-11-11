// 24-dictation.js : dictée vocale. Le micro (à droite du sélecteur de mode)
// enregistre, AJEAN relaie l'audio vers l'API Whisper réglée (dictation.go) et
// le texte s'insère dans le champ de saisie, à l'endroit du curseur.
// Sans API réglée (pas de clé, adresse Groq par défaut) : la reconnaissance
// vocale intégrée au navigateur (Web Speech API) prend le relais.
let dictRec = null, dictChunks = [], dictStream = null;
let dictSTT = { api: false, lang: '' }, dictSpeech = null;
// Enregistrement via l'API : onde en direct + compteur, arrêt auto à 2 min.
const DICT_MAX_S = 120;
// Bouton cliqué et champ où écrire (composeur, ou plein écran du mode Jean).
let dictBtnEl = null, dictTarget = null;
let dictSrc = null, dictAudio = null, dictRaf = 0, dictTimer = 0, dictT0 = 0;

// Fournisseurs connus (API compatible OpenAI /audio/transcriptions).
// « custom » : adresse saisie à la main.
const STT_PROVIDERS = [
  { id: 'groq', name: 'Groq', url: 'https://api.groq.com/openai/v1', model: 'whisper-large-v3-turbo', key: 'gsk_…' },
  { id: 'openai', name: 'OpenAI', url: 'https://api.openai.com/v1', model: 'gpt-4o-mini-transcribe', key: 'sk-…' },
  { id: 'mistral', name: 'Mistral (Voxtral)', url: 'https://api.mistral.ai/v1', model: 'voxtral-mini-latest', key: '' },
  { id: 'fireworks', name: 'Fireworks', url: 'https://audio-prod.api.fireworks.ai/v1', model: 'whisper-v3', key: 'fw_…' },
  { id: 'local', name: 'Whisper local', url: 'http://127.0.0.1:8000/v1', model: 'whisper-1', key: '' },
  { id: 'custom', name: '', url: '', model: '', key: '' },
];
let sttHasKey = false;

function sttProvider(){
  const id = (document.getElementById('stt-provider') || {}).value || 'groq';
  return STT_PROVIDERS.find(p => p.id === id) || STT_PROVIDERS[0];
}
// Choix dans la liste : adresse et modèle du fournisseur, champ d'adresse
// visible seulement pour « Autre ».
function sttProviderPick(fromLoad){
  const p = sttProvider();
  const url = document.getElementById('stt-url'), model = document.getElementById('stt-model');
  document.getElementById('stt-url-row').style.display = p.id === 'custom' ? '' : 'none';
  if(p.id !== 'custom') url.value = p.url;
  if(!fromLoad){ model.value = p.model; if(p.id === 'custom') url.focus(); else sttLoadModels(); }
  model.placeholder = p.model || 'whisper-1';
  const k = document.getElementById('stt-key');
  k.placeholder = sttHasKey ? tf('stt.key_saved', 'clé enregistrée (vide = inchangée)') : (p.key || tf('stt.key_none', 'facultative'));
}

async function loadSTT(){
  try{
    const r = await jget('/api/stt/settings');
    const sel = document.getElementById('stt-provider');
    if(sel && !sel.options.length){
      sel.innerHTML = STT_PROVIDERS.map(p => '<option value="'+p.id+'">'+escHtml(p.name || tf('stt.custom', 'Autre (adresse à saisir)'))+'</option>').join('');
    }
    const known = STT_PROVIDERS.find(p => p.url && p.url === r.url);
    sel.value = known ? known.id : 'custom';
    document.getElementById('stt-url').value = r.url || '';
    document.getElementById('stt-model').value = r.model || '';
    document.getElementById('stt-lang').value = r.lang || '';
    document.getElementById('stt-key').value = '';
    sttHasKey = !!r.has_key;
    sttProviderPick(true);
    sttLoadModels();
    dictSTT = { api: !!r.has_key || r.url !== r.default_url, lang: r.lang || '' };
  }catch(e){}
}

// Liste des modèles de l'API (adresse + clé saisies, ou celles enregistrées).
// Le menu remplace le champ texte ; « Autre » rend la saisie libre.
let sttModelsSeq = 0;
async function sttLoadModels(){
  const seq = ++sttModelsSeq;
  const sel = document.getElementById('stt-model-sel'), inp = document.getElementById('stt-model');
  const p = sttProvider();
  const url = p.id === 'custom' ? document.getElementById('stt-url').value.trim() : p.url;
  const key = (document.getElementById('stt-key').value || '').trim();
  let r = null;
  if(url) r = await jpost('/api/stt/models', {url, key}).catch(() => null);
  if(seq !== sttModelsSeq) return;
  const list = r && !r.error ? [...(r.audio || []), ...(r.other || [])] : [];
  if(!list.length){ sel.style.display = 'none'; inp.style.display = ''; return; }
  const cur = inp.value.trim();
  const opt = (v, txt) => '<option value="'+escHtml(v)+'">'+escHtml(txt || v)+'</option>';
  let html = '';
  if(r.audio && r.audio.length) html += '<optgroup label="'+escHtml(tf('stt.models_audio', 'Transcription'))+'">'+r.audio.map(v => opt(v)).join('')+'</optgroup>';
  if(r.other && r.other.length) html += '<optgroup label="'+escHtml(tf('stt.models_other', 'Autres modèles'))+'">'+r.other.map(v => opt(v)).join('')+'</optgroup>';
  html += opt('__custom', tf('stt.model_custom', 'Autre (saisir le nom)'));
  sel.innerHTML = html;
  if(cur && !list.includes(cur)){ sel.value = '__custom'; inp.style.display = ''; }
  else { sel.value = cur || list[0]; inp.value = sel.value; inp.style.display = 'none'; }
  sel.style.display = '';
}
function sttModelPick(){
  const sel = document.getElementById('stt-model-sel'), inp = document.getElementById('stt-model');
  if(sel.value === '__custom'){ inp.style.display = ''; inp.value = ''; inp.focus(); }
  else { inp.style.display = 'none'; inp.value = sel.value; }
}

async function saveSTT(){
  const g = id => ((document.getElementById(id) || {}).value || '').trim();
  const p = sttProvider();
  const url = p.id === 'custom' ? g('stt-url') : p.url;
  if(!url){ toast(tf('stt.url_missing', "Adresse de l'API manquante")); return; }
  const r = await jpost('/api/stt/settings', { url, key: g('stt-key'), model: g('stt-model') || p.model, lang: g('stt-lang') }).catch(e => ({ok:false, error:String(e)}));
  if(!r.ok){ toast(r.error || t('chat.error')); return; }
  toast(tf('stt.saved', 'Dictée vocale enregistrée'));
  loadSTT();
}

function dictBtn(state){
  const b = dictBtnEl || document.getElementById('mic-btn');
  if(!b) return;
  // Carré « stop » pendant l'enregistrement : un clic arrête.
  if(!b.querySelector(':scope>.mic-stop')) b.insertAdjacentHTML('afterbegin', '<svg class="mic-stop" viewBox="0 0 24 24" width="12" height="12" fill="currentColor" aria-hidden="true"><rect x="5" y="5" width="14" height="14" rx="3"/></svg>');
  b.classList.toggle('rec', state === 'rec');
  b.classList.toggle('busy', state === 'busy');
  b.title = state === 'rec' ? tf('stt.stop_title', 'Arrêter et transcrire') : tf('stt.mic_title', 'Dicter un message');
}

// Insère le texte dicté au curseur, avec les espaces qu'il faut autour.
function dictInsert(text){
  text = (text || '').trim();
  if(!text) return;
  const ta = dictTarget || document.getElementById('input');
  const a = ta.selectionStart ?? ta.value.length, z = ta.selectionEnd ?? ta.value.length;
  const before = ta.value.slice(0, a), after = ta.value.slice(z);
  const ins = (before && !/\s$/.test(before) ? ' ' : '') + text + (after && !/^\s/.test(after) ? ' ' : '');
  ta.value = before + ins + after;
  const pos = (before + ins).length;
  ta.focus(); ta.setSelectionRange(pos, pos);
  ta.dispatchEvent(new Event('input', {bubbles: true}));
}

// Dictée du navigateur : le texte arrive phrase par phrase, jusqu'au second clic.
function dictBrowser(){
  const SR = window.SpeechRecognition || window.webkitSpeechRecognition;
  if(!SR){
    askAlert(tf('stt.no_browser', "Ce navigateur ne sait pas dicter. Réglez une API de transcription dans Paramètres > Dictée vocale."));
    return;
  }
  const lang = dictSTT.lang || navigator.language || (currentLang() === 'fr' ? 'fr-FR' : 'en-US');
  const rec = new SR();
  rec.lang = lang.length === 2 ? lang + '-' + lang.toUpperCase() : lang;
  rec.continuous = true;
  rec.interimResults = false;
  rec.onresult = e => {
    for(let i = e.resultIndex; i < e.results.length; i++) if(e.results[i].isFinal) dictInsert(e.results[i][0].transcript);
  };
  rec.onerror = e => {
    if(e.error === 'not-allowed' || e.error === 'service-not-allowed') askAlert(tf('stt.denied', "Accès au micro refusé."));
    else if(e.error !== 'no-speech' && e.error !== 'aborted') toast(tf('stt.browser_error', 'Dictée interrompue : ') + e.error);
  };
  rec.onend = () => { dictSpeech = null; dictBtn(''); };
  try{ rec.start(); }catch(e){ askAlert(String(e)); return; }
  dictSpeech = rec;
  dictBtn('rec');
}

// Micro gardé ouvert 30 s après un enregistrement : le suivant démarre sans
// attendre l'ouverture du périphérique (~1-2 s). dictWarm() l'ouvre dès l'appui
// sur le bouton, avant le relâchement du clic.
const DICT_KEEP_MS = 30000;
let dictStreamP = null, dictReleaseT = 0;
function dictGetStream(){
  if(dictStream && dictStream.getAudioTracks().some(t => t.readyState === 'live')) return Promise.resolve(dictStream);
  if(!dictStreamP){
    dictStreamP = navigator.mediaDevices.getUserMedia({audio:{echoCancellation:true, noiseSuppression:true, autoGainControl:true}})
      .then(st => { dictStream = st; dictStreamP = null; return st; }, e => { dictStreamP = null; throw e; });
  }
  return dictStreamP;
}
function dictWarm(){
  if(!dictSTT.api || !navigator.mediaDevices || dictSpeech || (dictRec && dictRec.state === 'recording')) return;
  dictGetStream().then(() => dictRelease()).catch(() => {});
}
function dictRelease(){
  clearTimeout(dictReleaseT);
  dictReleaseT = setTimeout(() => {
    if(dictRec && dictRec.state === 'recording') return;
    dictRelease();
  }, DICT_KEEP_MS);
}

async function toggleDictation(btn, target){
  // Un enregistrement en cours s'arrête depuis n'importe quel micro.
  if(!dictSpeech && !(dictRec && dictRec.state === 'recording')){
    dictBtnEl = btn || document.getElementById('mic-btn');
    dictTarget = target || document.getElementById('input');
  }
  if(dictSpeech){ dictSpeech.stop(); return; }
  if(dictRec && dictRec.state === 'recording'){ dictRec.stop(); return; }
  if(!dictSTT.api){ dictBrowser(); return; }
  if(!navigator.mediaDevices || !window.MediaRecorder){
    askAlert(tf('stt.no_mic', "Le micro n'est pas disponible ici (il faut une page en HTTPS ou localhost)."));
    return;
  }
  dictBtn('rec'); // retour immédiat, le micro s'ouvre derrière
  try{
    dictStream = await dictGetStream();
  }catch(e){
    dictBtn('');
    askAlert(tf('stt.denied', "Accès au micro refusé."));
    return;
  }
  clearTimeout(dictReleaseT);
  const mime = ['audio/webm;codecs=opus', 'audio/webm', 'audio/ogg;codecs=opus', 'audio/mp4'].find(m => MediaRecorder.isTypeSupported(m)) || '';
  dictChunks = [];
  dictRec = new MediaRecorder(dictStream, mime ? {mimeType: mime} : undefined);
  dictRec.ondataavailable = e => { if(e.data && e.data.size) dictChunks.push(e.data); };
  dictRec.onstop = () => dictSend(dictRec.mimeType || mime);
  dictRec.start();
  dictBtn('rec');
  dictWaveStart(dictStream);
}

function dictFmt(s){ return Math.floor(s / 60) + ':' + String(Math.floor(s % 60)).padStart(2, '0'); }
function dictWaveStart(stream){
  const b = dictBtnEl;
  if(!b) return;
  let w = b.querySelector('.mic-rec');
  if(!w){
    w = document.createElement('span'); w.className = 'mic-rec';
    w.innerHTML = '<canvas width="72" height="18"></canvas><span class="mic-time"></span>';
    b.appendChild(w);
  }
  b.classList.add('wave');
  const cv = w.querySelector('canvas'), tm = w.querySelector('.mic-time'), g = cv.getContext('2d');
  const dpr = window.devicePixelRatio || 1;
  cv.width = 72 * dpr; cv.height = 18 * dpr; cv.style.width = '72px'; cv.style.height = '18px';
  dictT0 = Date.now();
  const tick = () => {
    const s = (Date.now() - dictT0) / 1000;
    tm.textContent = dictFmt(s) + ' / ' + dictFmt(DICT_MAX_S);
    if(s >= DICT_MAX_S && dictRec && dictRec.state === 'recording') dictRec.stop();
  };
  tick(); dictTimer = setInterval(tick, 250);
  let an = null;
  try{
    const AC = window.AudioContext || window.webkitAudioContext;
    if(!dictAudio) dictAudio = new AC();
    if(dictAudio.state === 'suspended') dictAudio.resume();
    an = dictAudio.createAnalyser(); an.fftSize = 512;
    // Plancher relevé : le bruit de fond de la pièce tombe sous zéro.
    an.minDecibels = -68; an.maxDecibels = -20; an.smoothingTimeConstant = 0.7;
    dictSrc = dictAudio.createMediaStreamSource(stream); dictSrc.connect(an);

  }catch(e){}
  // Barres fixes : chacune suit une bande de fréquence de la voix, sur place.
  const N = 20, bars = new Array(N).fill(0);
  const fdata = an ? new Uint8Array(an.frequencyBinCount) : null;
  const col = getComputedStyle(b).color;
  const draw = () => {
    dictRaf = requestAnimationFrame(draw);
    if(an) an.getByteFrequencyData(fdata);
    const W = cv.width, H = cv.height, bw = W / N;
    g.clearRect(0, 0, W, H); g.fillStyle = col;
    for(let i = 0; i < N; i++){
      // Centre = cœur de la voix (~300 Hz-1 kHz), bords = aigus (~2,5 kHz) et
      // plus bas (enveloppe). Les très graves (bourdonnement) sont ignorés.
      const k = Math.abs(i - (N - 1) / 2) / ((N - 1) / 2);
      let raw = 0;
      if(fdata){ const lo = 3 + Math.round(k * 22); for(let j = lo; j < lo + 3; j++) raw += fdata[j]; raw /= 3 * 255; }
      const v = Math.min(1, Math.max(0, (raw - 0.25) / 0.6)) * (1 - 0.45 * k);
      bars[i] += (v - bars[i]) * 0.35; // lissage
      const h = Math.max(2 * dpr, bars[i] * H);
      const w = Math.max(1.5 * dpr, bw * 0.34);
      g.fillRect(i * bw + (bw - w) / 2, (H - h) / 2, w, h);
    }
  };
  draw();
}
function dictWaveStop(){
  cancelAnimationFrame(dictRaf); dictRaf = 0;
  clearInterval(dictTimer); dictTimer = 0;
  if(dictSrc){ try{ dictSrc.disconnect(); }catch(e){} dictSrc = null; }
  if(dictBtnEl) dictBtnEl.classList.remove('wave');
}

async function dictSend(mime){
  dictWaveStop();
  if(dictStream){ dictStream.getTracks().forEach(t => t.stop()); dictStream = null; }
  const blob = new Blob(dictChunks, {type: mime || 'audio/webm'});
  dictChunks = [];
  if(blob.size < 1000){ dictBtn(''); return; }
  dictBtn('busy');
  try{
    const b64 = await new Promise((res, rej) => { const fr = new FileReader(); fr.onload = () => res(String(fr.result).split(',')[1] || ''); fr.onerror = rej; fr.readAsDataURL(blob); });
    const r = await jpost('/api/stt', {audio: b64, mime: blob.type});
    if(r.error){ askAlert(r.error); return; }
    dictInsert(r.text);
  }catch(e){
    askAlert(String(e));
  }finally{
    dictBtn('');
  }
}
