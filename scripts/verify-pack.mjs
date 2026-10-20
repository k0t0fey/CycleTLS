import fs from 'node:fs';
import crypto from 'node:crypto';
const [packMetadata='pack.json']=process.argv.slice(2);
const [{files}]=JSON.parse(fs.readFileSync(packMetadata,'utf8'));
const required=['index','index.exe','index-arm64','index-arm','index-mac','index-mac-arm64','index-freebsd'].map(f=>'dist/'+f);
required.push('dist/index.js','dist/index.d.ts','dist/checksums.txt','LICENSE','FORK.md');
for(const name of required)if(!files.some(f=>f.path===name))throw Error(`Missing packaged file: ${name}`);
for(const line of fs.readFileSync('dist/checksums.txt','utf8').trim().split('\n')){
  const [expected,name]=line.split(/\s+/);
  const actual=crypto.createHash('sha256').update(fs.readFileSync('dist/'+name)).digest('hex');
  if(actual!==expected)throw Error(`Checksum mismatch: ${name}`);
}
for(const name of required.filter(f=>f.startsWith('dist/index')&&!/\.(js|ts|exe)$/.test(f))){
  if(!(files.find(f=>f.path===name).mode&0o111))throw Error(`Executable bit missing: ${name}`);
}
console.log(`Verified ${required.length} required package entries and seven SHA256 checksums`);
