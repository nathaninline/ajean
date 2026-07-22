// marked: GFM on, no auto-breaks (single newlines stay inline).
marked.setOptions({ gfm: true, breaks: false });
// Pre-process to fix common LLM markdown mistakes before handing to marked:
//  - code fences glued to preceding text on the same line ("foo ```ruby ...")
//  - fences without a closing newline
//  - 3+ consecutive blank lines collapsed to one (LLMs love padding)
function fixMd(s){
  if(!s) return '';
  // Force a newline before a fence that's been glued to preceding text
  // (common LLM mistake: "...crée le fichier et```ruby"). We do NOT split
  // *after* the fence — the part after the fence is the language identifier.
  s = s.replace(/([^\n])(\s*)(```+|~~~+)(?=\w*\s*\n)/g, '$1\n$3');
  // Collapse runs of blank lines that LLMs love to emit.
  s = s.replace(/\n{3,}/g, '\n\n');
  return s;
}
function md(src){ return src ? marked.parse(fixMd(src)) : ''; }
let msgs = [];
let busy = false;
function toggleSide(){ document.getElementById('side').classList.toggle('open'); document.getElementById('backdrop').classList.toggle('open'); document.body.classList.toggle('drawer-open'); }
function saveSys(){ localStorage.setItem('jean.sys', document.getElementById('sysprompt').value); }
