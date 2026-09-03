const fs = require('fs');
const path = require('path');
const dir = 'F:/IA/ajean-src';
const files = fs.readdirSync(dir).filter(f => /^i18n-extract-.*\.json$/.test(f));
const merged = {};
const conflicts = [];
for (const f of files) {
  const data = JSON.parse(fs.readFileSync(path.join(dir, f), 'utf8'));
  for (const [k, v] of Object.entries(data)) {
    if (Object.prototype.hasOwnProperty.call(merged, k) && merged[k] !== v) {
      conflicts.push({key: k, from: merged[k], to: v, file: f});
    }
    merged[k] = v;
  }
}
console.log('Files merged:', files.length);
console.log('Total unique keys:', Object.keys(merged).length);
console.log('Conflicts (same key, different value):', conflicts.length);
if (conflicts.length) console.log(JSON.stringify(conflicts, null, 2));
fs.writeFileSync(path.join(dir, 'i18n-merged-fr.json'), JSON.stringify(merged, null, 2), 'utf8');
console.log('Written to i18n-merged-fr.json');
