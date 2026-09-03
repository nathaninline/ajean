const fs = require('fs');
const dataFile = 'F:/IA/ajean-src/internal/ajean/ui/src/js/00a-i18n-data.js';
const merged = JSON.parse(fs.readFileSync('F:/IA/ajean-src/i18n-merged-fr.json', 'utf8'));

const lines = fs.readFileSync(dataFile, 'utf8').split('\n');
// Find the closing "}," of the fr: block (first one after "fr: {").
let frStart = lines.findIndex(l => l.trim() === 'fr: {');
if (frStart < 0) throw new Error('fr: block not found');
let frEnd = -1;
for (let i = frStart + 1; i < lines.length; i++) {
  if (lines[i].trim() === '},') { frEnd = i; break; }
}
if (frEnd < 0) throw new Error('closing of fr: block not found');

const newLines = Object.entries(merged).map(([k, v]) => '  ' + JSON.stringify(k) + ': ' + JSON.stringify(v) + ',');
const out = lines.slice(0, frEnd).concat(newLines).concat(lines.slice(frEnd));
fs.writeFileSync(dataFile, out.join('\n'), 'utf8');
console.log('Injected', newLines.length, 'keys into fr: block (after line', frEnd, ')');
