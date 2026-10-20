import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import {spawnSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';

const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..');
const targets=[
  ['index','linux','amd64'], ['index.exe','windows','amd64'],
  ['index-arm64','linux','arm64'], ['index-arm','linux','arm'],
  ['index-mac','darwin','amd64'], ['index-mac-arm64','darwin','arm64'],
  ['index-freebsd','freebsd','amd64'],
];
const go=process.env.GO_BINARY||'go';
const env={...process.env,GOTOOLCHAIN:'local',CGO_ENABLED:'0'};
delete env.GOOS;delete env.GOARCH;delete env.GOAMD64;delete env.GOARM;
function run(args,extraEnv={}){
  const r=spawnSync(go,args,{cwd:path.join(root,'src'),env:{...env,...extraEnv},encoding:'utf8',windowsHide:true});
  if(r.error || r.status!==0)throw r.error||Error(r.stderr||`Go exited ${r.status}`);
  return r.stdout;
}
const version=run(['version']).trim();
if(!version.startsWith('go version go1.24.13 '))throw Error(`Go version is not pinned: ${version}`);
const selected=process.argv.slice(2);
const builds=selected.length?targets.filter(([name])=>selected.includes(name)):targets;
if(selected.some(name=>!targets.some(t=>t[0]===name)))throw Error('Unknown target');
fs.mkdirSync(path.join(root,'dist'),{recursive:true});
for(const [filename,GOOS,GOARCH] of builds){
  const output=path.join(root,'dist',filename);
  run(['build','-trimpath','-buildvcs=false','-o',output,'./'],{GOOS,GOARCH,GOAMD64:'v1',GOARM:'7'});
  if(GOOS!=='windows')fs.chmodSync(output,0o755);
  console.log(JSON.stringify({filename,GOOS,GOARCH,sha256:crypto.createHash('sha256').update(fs.readFileSync(output)).digest('hex')}));
}
if(targets.every(([filename])=>fs.existsSync(path.join(root,'dist',filename)))){
  const sums=targets.map(([filename])=>`${crypto.createHash('sha256').update(fs.readFileSync(path.join(root,'dist',filename))).digest('hex')}  ${filename}`).join('\n')+'\n';
  fs.writeFileSync(path.join(root,'dist/checksums.txt'),sums);
}
