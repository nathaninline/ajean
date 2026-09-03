const fs = require('fs');
const path = require('path');
const jsDir = 'F:/IA/ajean-src/internal/ajean/ui/src/js';
const htmlFile = 'F:/IA/ajean-src/internal/ajean/ui/src/index.tmpl.html';
const merged = JSON.parse(fs.readFileSync('F:/IA/ajean-src/i18n-merged-fr.json', 'utf8'));

const usedKeys = new Set();
const files = fs.readdirSync(jsDir).filter(f => f.endsWith('.js') && f !== '00a-i18n-data.js' && f !== '00b-i18n.js');
for (const f of files) {
  const content = fs.readFileSync(path.join(jsDir, f), 'utf8');
  const re = /\bt\(\s*'([^']+)'\s*\)/g;
  let m;
  while ((m = re.exec(content))) usedKeys.add(m[1]);
}
const html = fs.readFileSync(htmlFile, 'utf8');
const reHtml = /data-i18n="([^"]+)"/g;
let hm;
while ((hm = reHtml.exec(html))) usedKeys.add(hm[1]);

const missing = [...usedKeys].filter(k => !(k in merged));
console.log('Keys referenced (JS t() + HTML data-i18n):', usedKeys.size);
console.log('Keys used but MISSING from merged dictionary:', missing.length);
if (missing.length) console.log(missing.join('\n'));

const unused = Object.keys(merged).filter(k => !usedKeys.has(k));
console.log('\nKeys in dictionary but never referenced:', unused.length);
if (unused.length) console.log(unused.join('\n'));
