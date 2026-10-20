const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const net = require('node:net');
const http2 = require('node:http2');
const delay = ms => new Promise(resolve=>setTimeout(resolve,ms));
const listen = server => new Promise((resolve,reject)=>{
  server.once('error',reject);
  server.listen(0,'127.0.0.1',()=>resolve(server.address().port));
});
async function run(init = require('../dist/index.js')){
  const sockets=new Set();
  const server=http2.createSecureServer({
    key:fs.readFileSync(path.join(__dirname,'../tests/key.pem')),
    cert:fs.readFileSync(path.join(__dirname,'../tests/cert.pem')),
    minVersion:'TLSv1.2',maxVersion:'TLSv1.2',
  },(req,res)=>{res.writeHead(200,{'content-type':'text/plain'});res.end('loopback-ok');});
  server.on('connection',socket=>{sockets.add(socket);socket.on('close',()=>sockets.delete(socket));});
  const probe=net.createServer();
  const port=await listen(probe);
  await new Promise(resolve=>probe.close(resolve));
  let client;
  try{
    const webPort=await listen(server);
    client=await init({port,autoExit:true});
    for(let n=0;n<10;n++){
      const response=await client(`https://127.0.0.1:${webPort}/`,{
        ja3:'771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53,0-23-65281-10-11-35-16-5-13-18-51-45-43-27-17513,29-23-24,0',
        userAgent:'localhost-smoke',timeout:2,enableConnectionReuse:false,
        insecureSkipVerify:true,
      },'get');
      assert.equal(response.status,200);
      assert.equal(Buffer.isBuffer(response.data)?response.data.toString('utf8'):response.data,'loopback-ok');
      const until=Date.now()+1000;
      while(sockets.size && Date.now()<until)await delay(5);
      assert.equal(sockets.size,0,`request ${n+1}: connection survived response`);
    }
    console.log('PASS: SDK/helper loopback HTTPS, 10 requests, one helper, zero surviving connections');
  }finally{
    await client?.exit();
    for(const socket of sockets)socket.destroy();
    await new Promise(resolve=>server.close(resolve));
  }
}
module.exports = run;
if(require.main === module){
  const deadline=setTimeout(()=>{console.error('Loopback smoke exceeded 30 seconds');process.exit(1);},30000);
  run().catch(error=>{console.error(error);process.exitCode=1;}).finally(()=>clearTimeout(deadline));
}
