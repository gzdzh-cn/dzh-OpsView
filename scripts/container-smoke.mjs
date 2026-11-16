import { execFileSync } from 'node:child_process'
import { mkdtempSync, writeFileSync, rmSync } from 'node:fs'
import path from 'node:path'
import crypto from 'node:crypto'
const root = path.resolve(import.meta.dirname, '..')
const dir = mkdtempSync(path.join(root, '.opsview', 'smoke-'))
const suffix = crypto.randomBytes(5).toString('hex'), created = []
const catalog = JSON.parse(execFileSync('cat', [path.join(root, 'packages/catalog/versions.json')], { encoding:'utf8' }))
const image = software => catalog.find(x=>x.software===software && x.image).image
const docker = (...args) => execFileSync('docker', args, { encoding:'utf8', stdio:['pipe','pipe','pipe'], timeout:180000 })
function start(name, args) { const id = docker('run','--rm','-d','--name',`opsview-smoke-${suffix}-${name}`,'--label','opsview.test=true',...args).trim(); created.push(id); return id }
async function wait(probe, description) { let last; for(let i=0;i<90;i++){ try { return await probe() } catch(e) { last=e; await new Promise(r=>setTimeout(r,1000)) } }; throw new Error(`${description}: ${last}`) }
function port(id, internal) { const inspect=JSON.parse(docker('inspect',id))[0];return inspect.NetworkSettings.Ports[`${internal}/tcp`][0].HostPort }
try {
  writeFileSync(path.join(dir,'index.html'),'<h1>OpsView integration</h1>')
  writeFileSync(path.join(dir,'nginx.conf'),'events {}\nhttp { server { listen 80; root /usr/share/nginx/html; location / { try_files $uri /index.html; } } }\n')
  const nginx=start('nginx',['-p','127.0.0.1::80','-v',`${dir}/nginx.conf:/etc/nginx/nginx.conf:ro`,'-v',`${dir}/index.html:/usr/share/nginx/html/index.html:ro`,image('nginx')])
  const address=`http://127.0.0.1:${port(nginx,80)}`
  await wait(async()=>{const body=await(await fetch(address)).text();if(!body.includes('OpsView integration'))throw new Error(body)},'Nginx startup')
  writeFileSync(path.join(dir,'nginx.conf'),'events {}\nhttp { invalid_directive; }')
  let rejected=false;try{docker('exec',nginx,'nginx','-t')}catch{rejected=true};if(!rejected)throw new Error('Nginx accepted invalid configuration')
  if(!(await(await fetch(address)).text()).includes('OpsView integration'))throw new Error('old site stopped after invalid configuration')
  console.log('PASS Nginx serves a site and keeps old workers after invalid configuration')
  const password=crypto.randomBytes(20).toString('hex')
  const mysql=start('mysql',['-e',`MYSQL_ROOT_PASSWORD=${password}`,image('mysql')])
  const mysqlSQL=(sql,db)=>docker('exec','-e',`MYSQL_PWD=${password}`,mysql,'mysql','-uroot','--batch','--skip-column-names',...(db?[db]:[]),'-e',sql)
  await wait(()=>mysqlSQL('SELECT 1'),'MySQL startup')
  mysqlSQL("CREATE DATABASE opsview_source; CREATE DATABASE opsview_restore; CREATE USER 'opsview_app'@'127.0.0.1' IDENTIFIED BY 'test-only-password-123!'; GRANT ALL ON opsview_source.* TO 'opsview_app'@'127.0.0.1'; CREATE TABLE opsview_source.items(id INT PRIMARY KEY, name VARCHAR(50)); INSERT INTO opsview_source.items VALUES (1,'opsview');")
  docker('exec','-e','MYSQL_PWD=test-only-password-123!',mysql,'mysql','--protocol=TCP','-h127.0.0.1','-uopsview_app','opsview_source','-e','SELECT * FROM items')
  const dump=docker('exec','-e',`MYSQL_PWD=${password}`,mysql,'mysqldump','-uroot','--single-transaction','--set-gtid-purged=OFF','opsview_source')
  execFileSync('docker',['exec','-i','-e',`MYSQL_PWD=${password}`,mysql,'mysql','-uroot','opsview_restore'],{input:dump,stdio:['pipe','pipe','pipe'],timeout:30000})
  if(mysqlSQL('SELECT name FROM items','opsview_restore').trim()!=='opsview')throw new Error('MySQL restore mismatch')
  console.log('PASS MySQL dedicated account, logical dump and restore to a new database')
  const pg=start('postgres',['-e',`POSTGRES_PASSWORD=${password}`,image('postgresql')])
  await wait(()=>docker('exec',pg,'pg_isready','-U','postgres'),'PostgreSQL startup')
  const pgSQL=(sql,db='postgres')=>docker('exec',pg,'psql','-U','postgres','-v','ON_ERROR_STOP=1','-t','-A','-d',db,'-c',sql)
  pgSQL("CREATE ROLE opsview_app LOGIN PASSWORD 'test-only-password-123!'");pgSQL('CREATE DATABASE opsview_source OWNER opsview_app');pgSQL('CREATE DATABASE opsview_restore OWNER opsview_app')
  pgSQL("CREATE TABLE items(id INT PRIMARY KEY,name TEXT); INSERT INTO items VALUES(1,'opsview')",'opsview_source')
  const pgDump=execFileSync('docker',['exec',pg,'pg_dump','-U','postgres','-Fc','opsview_source'],{timeout:30000})
  execFileSync('docker',['exec','-i',pg,'pg_restore','-U','postgres','--no-owner','--no-acl','--role=opsview_app','--exit-on-error','-d','opsview_restore'],{input:pgDump,timeout:30000,stdio:['pipe','pipe','pipe']})
  if(pgSQL('SELECT name FROM items','opsview_restore').trim()!=='opsview')throw new Error('PostgreSQL restore mismatch')
  const roles=docker('exec',pg,'pg_dumpall','-U','postgres','--roles-only');if(!roles.includes('opsview_app'))throw new Error('PostgreSQL roles missing from export')
  console.log('PASS PostgreSQL custom dump, role export and restore to a new database')
} finally { for(const id of created.reverse())try{docker('rm','-f',id)}catch{};rmSync(dir,{recursive:true,force:true}) }
