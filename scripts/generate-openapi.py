#!/usr/bin/env python3
"""Keep the public contract and generated frontend types in source control."""
import json
from pathlib import Path
root = Path(__file__).resolve().parents[1]
def obj(fields, required=None):
    return {"type": "object", "properties": fields, "required": required or list(fields)}
string = {"type": "string"}
config = {"type": "object", "additionalProperties": True}
resource = obj({"id": string, "kind": string, "name": string, "status": string, "config": config, "revision": {"type": "integer"}, "createdAt": string, "updatedAt": string})
task = obj({"id": string, "resourceId": string, "operation": string, "state": {"type": "string", "enum": ["queued", "running", "succeeded", "failed", "interrupted", "cancelled"]}, "error": string, "createdAt": string, "updatedAt": string})
schemas = {"Resource": resource, "Task": task, "Error": obj({"error": string,"code":string,"revision":string,"resourceId":string},["error"]), "ResourceInput": obj({"kind": string, "name": string, "config": config}, ["kind","config"]), "ResourceUpdate": obj({"name": string, "config": config, "revision": {"type": "integer"}}), "Envelope": obj({"keyId": string, "payload": string, "signature": string})}
paths = {}
def add(path, method, summary, response=config, body=None, public=False, status="200"):
    route = paths.setdefault('/api/v1' + path, {})
    op = {"operationId": method + '_' + path.replace('/', '_').replace('{', '').replace('}', '').replace('-', '_').strip('_'), "summary": summary, "security": [] if public else [{"session": []}, {"httpSession": []}], "responses": {status: {"description": "成功", "content": {"application/json": {"schema": response}}}, "400": {"description": "校验失败", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Error"}}}}, "401": {"description": "未登录"}, "409": {"description": "资源冲突"}}}
    params = []
    for part in path.split('/'):
        if part.startswith('{'): params.append({"name": part[1:-1], "in": "path", "required": True, "schema": string})
    if method != 'get' and not public: params.append({"name": "X-CSRF-Token", "in": "header", "required": True, "schema": string})
    if path == '/resources' and method == 'get': params.append({"name": "kind", "in": "query", "schema": string})
    if path.endswith('/events'): params += [{"name": "after", "in": "query", "schema": {"type": "integer"}}, {"name": "stream", "in": "query", "schema": {"type": "boolean"}}, {"name": "Last-Event-ID", "in": "header", "schema": string}]; op['responses'][status]['content']['text/event-stream'] = {"schema": string}
    if params: op['parameters'] = params
    if body is not None: op['requestBody'] = {"required": True, "content": {"application/json": {"schema": body}}}
    route[method] = op
def ref(name): return {"$ref": "#/components/schemas/" + name}
add('/health', 'get', '存活检查', public=True)
add('/auth', 'get', '初始化及会话状态', public=True)
add('/auth/setup', 'post', '创建唯一管理员', body=obj({"token":string,"username":string,"password":string}), public=True, status='201')
add('/auth/login', 'post', '密码及 TOTP 登录', body=obj({"username":string,"password":string,"code":string}), public=True)
for path, fields in [('/auth/logout', {}), ('/auth/two-factor', {"action": string,"password":string,"code":string}), ('/auth/password', {"current":string,"password":string,"code":string})]: add(path,'post',path,body=obj(fields))
add('/resources','get','资源列表，敏感字段脱敏',{"type":"array","items":ref('Resource')})
add('/resources','post','创建资源配置',ref('Resource'),ref('ResourceInput'),status='201')
add('/resources/{id}','put','按 revision 更新配置',ref('Resource'),ref('ResourceUpdate'))
add('/resources/{id}','delete','删除待部署或计划资源')
add('/resources/{id}/actions/{action}','post','提交持久化任务；logs 为同步结果',ref('Task'),config,status='202')
add('/tasks','get','最近 200 条任务',{"type":"array","items":ref('Task')})
add('/tasks/{id}/events','get','任务日志与支持 Last-Event-ID 的 SSE')
for path in ['/overview','/catalog','/plugins','/audit','/settings','/system','/openapi.json','/docker/{collection}']: add(path,'get',path)
add('/settings','put','面板名称与 IP 访问范围',body=config)
add('/settings/backups','get','面板配置备份列表')
add('/settings/backup','post','提交加密面板配置备份',ref('Task'),config,status='202')
add('/settings/backup/{file}','get','下载面板配置备份')
paths['/api/v1/settings/backup/{file}']['get']['responses']['200']['content'] = {'application/octet-stream': {'schema': {'type':'string','format':'binary'}}}
add('/settings/certificate','post','上传面板 PEM 证书及私钥',body=obj({"certificate":string,"privateKey":string}))
add('/resources/{id}/download','get','下载加密备份或导出数据库')
paths['/api/v1/resources/{id}/download']['get']['responses']['200']['content'] = {'application/octet-stream': {'schema': {'type':'string','format':'binary'}}}
paths['/api/v1/resources/{id}/download']['get']['parameters'] += [{'name':'artifact','in':'query','schema':string}]
schemas['FileInput']=obj({'scope':{'type':'string','enum':['server']},'recentPaths':{'type':'array','items':string,'maxItems':15,'description':'metadata 操作校验最近访问目录，仅返回当前允许浏览的路径'},'resourceId':string,'action':{'type':'string','enum':['list','stat','read','write','mkdir','note','metadata','favorite','copy','move','rename','permissions','delete','search','size','compress','extract','trash','upload-create','upload-status','upload-finish','upload-cancel']},'path':string,'target':string,'paths':{'type':'array','items':string,'maxItems':1000},'revision':string,'text':{'type':'string','maxLength':10485760},'content':{'oneOf':[string,{'type':'boolean'}],'description':'旧接口为 Base64；服务器 search 操作为布尔内容搜索开关'},'mode':string,'owner':string,'group':string,'recursive':{'type':'boolean'},'confirmed':{'type':'boolean'},'query':string,'searchContent':{'type':'boolean','description':'保留字段；内容搜索使用 content=true'},'hidden':{'type':'boolean'},'filter':string,'page':{'type':'integer','minimum':1},'pageSize':{'type':'integer','minimum':1,'maximum':500},'sort':string,'descending':{'type':'boolean'},'conflict':{'type':'string','enum':['skip','overwrite','keep']},'trashAction':{'type':'string','enum':['list','restore','purge','empty']},'id':string,'size':{'type':'integer','format':'int64','maximum':2147483648},'sha256':string,'note':string,'remove':{'type':'boolean'}},['action'])
schemas['FileMetadata']=obj({'favorites':{'type':'array','items':string},'recentPaths':{'type':'array','items':string},'shortcuts':{'type':'array','items':obj({'name':string,'path':string})},'uploadLimit':{'type':'integer','format':'int64'}})
add('/files','post','服务器绝对路径或旧资源相对路径操作；metadata 返回 FileMetadata，列表和路径元信息过滤禁止访问项；批量耗时操作返回 taskId，写入冲突返回 409',body=ref('FileInput'))
paths['/api/v1/files']['post']['responses']['202']={'description':'耗时文件操作已排队','content':{'application/json':{'schema':ref('Task')}}}
add('/files/tasks/{id}','get','读取文件任务状态及加密持久化结果')
add('/files/transfer','get','经认证和执行器流式下载，支持 Range')
paths['/api/v1/files/transfer']['get']['parameters']=[{'name':'path','in':'query','required':True,'schema':string},{'name':'Range','in':'header','schema':string}]
paths['/api/v1/files/transfer']['get']['responses']['200']['content']={'application/octet-stream':{'schema':{'type':'string','format':'binary'}}}
paths['/api/v1/files/transfer']['get']['responses']['206']={'description':'文件范围响应','content':{'application/octet-stream':{'schema':{'type':'string','format':'binary'}}}}
add('/files/transfer','put','上传已创建会话的分块，单块最大 8 MiB')
paths['/api/v1/files/transfer']['put']['parameters'] += [{'name':'id','in':'query','required':True,'schema':string},{'name':'offset','in':'query','required':True,'schema':{'type':'integer','format':'int64'}}]
paths['/api/v1/files/transfer']['put']['requestBody']={'required':True,'content':{'application/octet-stream':{'schema':{'type':'string','format':'binary'}}}}
schemas['DockerStatus']=obj({'status':{'type':'string','enum':['ready','not_installed','stopped','incompatible','error']},'reason':string,'engineVersion':string,'apiVersion':string,'minAPIVersion':string,'maxAPIVersion':string,'composeAvailable':{'type':'boolean'},'composeVersion':string},['status','reason','composeAvailable'])
add('/docker/status','get','无版本探测及 API 1.41–1.47 协商，成功缓存 30 秒',ref('DockerStatus'))
add('/catalog/import','post','验证签名并导入软件配方',body=ref('Envelope'),status='201')
add('/plugins/import','post','导入签名插件清单',body=obj({"name":string,"envelope":ref('Envelope')}),status='201')
add('/repositories/{id}/refresh','post','验证仓库清单并同步插件',body=config)
add('/system/actions/{action}','post','受控系统操作',body=config,status='202')
add('/docker/containers/{id}/{action}','post','容器操作或日志',body=config,status='202')
add('/docker/manage/{collection}/{action}','post','固定摘要拉取镜像及管理网络和卷',ref('Task'),config,status='202')

schemas["CatalogEntry"]=obj({"id":string,"software":string,"version":string,"description":string,"platforms":{"type":"array","items":string},"recommended":{"type":"boolean"},"source":string,"image":string,"sha256":string,"recipeRevision":string,"disabledReason":string,"methods":{"type":"array","items":string},"availability":{"type":"object","additionalProperties":string},"verifiedSystems":{"type":"object","additionalProperties":{"type":"array","items":string}},"binaries":{"type":"array","items":obj({"source":string,"sha256":string,"buildPrefix":string,"system":string,"runtimePackages":{"type":"array","items":string}})}},["id","software","version","description","platforms"])
paths['/api/v1/catalog']['get']['responses']['200']['content']['application/json']['schema']={"type":"array","items":ref("CatalogEntry")}
software_field=obj({"key":string,"label":string,"section":string,"group":string,"default":string,"pattern":string,"apply":{"type":"string","enum":["reload","restart","dynamic"]},"options":{"type":"array","items":string}},["key","label","group","default","pattern","apply"])
schemas["SoftwareField"]=software_field
schemas["ConfigurationDocument"]=obj({"file":string,"revision":string,"content":string,"draft":string,"values":{"type":"object","additionalProperties":string},"fields":{"type":"array","items":ref("SoftwareField")},"history":{"type":"array","items":obj({"revision":string,"createdAt":string})}})
schemas["ConfigurationChange"]=obj({"file":string,"siteId":string,"baseRevision":string,"content":string,"values":{"type":"object","additionalProperties":string},"apply":{"type":"string","enum":["draft","reload","restart","dynamic"]},"confirmPersistenceChange":{"type":"boolean"}},["file","baseRevision","apply"])
for view in ['settings','metrics','logs','extensions','binlogs','session','memory-budget']: add('/instances/{id}/'+view,'get','读取实例'+view)
for name in ['budgetMB','values']: paths['/api/v1/instances/{id}/memory-budget']['get'].setdefault('parameters',[]).append({'name':name,'in':'query','schema':string})
add('/instances/{id}/configuration','get','读取配置、字段、加密历史及当前修订',ref('ConfigurationDocument'))
for name in ['file','revision','kind','search','siteId']:
 for view in ['configuration','logs']:paths['/api/v1/instances/{id}/'+view]['get'].setdefault('parameters',[]).append({'name':name,'in':'query','schema':string})
for name in ['since','until','level','archive','limit']:
 paths['/api/v1/instances/{id}/logs']['get']['parameters'].append({'name':name,'in':'query','schema':string})
schemas['SoftwareLogs']=obj({'content':string,'notice':string,'revision':string,'size':{'type':'integer'},'count':{'type':'integer'},'archive':string,'canRotate':{'type':'boolean'},'rotationNotice':string,'windowTruncated':{'type':'boolean'},'limited':{'type':'boolean'},'unknownTimeRecords':{'type':'integer'},'timezone':string,'windowBytes':{'type':'integer'},'archives':{'type':'array','items':obj({'name':string,'size':{'type':'integer'},'createdAt':string})}},['content'])
paths['/api/v1/instances/{id}/logs']['get']['responses']['200']['content']['application/json']['schema']=ref('SoftwareLogs')
paths['/api/v1/instances/{id}/session']['get'].setdefault('parameters',[]).append({'name':'siteId','in':'query','schema':string})
schemas['PHPSession']=obj({'handler':{'type':'string','enum':['files','redis','memcached']},'revision':string,'expiredFiles':{'type':'integer'},'directory':string,'instanceId':string,'namespace':string,'isolated':{'type':'boolean','description':'当前目录或前缀是否为该范围独立值，旧配置不会自动替换'},'memcached':obj({'port':{'type':'integer','minimum':1,'maximum':65535},'saslEnabled':{'type':'boolean'},'saslUsername':string,'saslPassword':{'type':'string','description':'已有密码仅返回遮盖占位符，保存时原样提交可保留凭据'}})})
schemas['PHPSessionSave']=obj({'operation':{'type':'string','enum':['save']},'siteId':string,'handler':{'type':'string','enum':['files','redis','memcached']},'baseRevision':string,'confirmed':{'type':'boolean','enum':[True]},'instanceId':string,'memcachedPort':{'type':'integer','minimum':1,'maximum':65535,'description':'仅连接本机 127.0.0.1，需已安装对应服务'},'saslEnabled':{'type':'boolean'},'saslUsername':string,'saslPassword':{'type':'string','writeOnly':True}},['operation','handler','baseRevision','confirmed'])
schemas['PHPSessionClean']=obj({'operation':{'type':'string','enum':['clean']},'siteId':string,'confirmed':{'type':'boolean','enum':[True]}},['operation','confirmed'])
paths['/api/v1/instances/{id}/session']['get']['responses']['200']['content']['application/json']['schema']=ref('PHPSession')
add('/resources/{id}/actions/session','post','验证并修改所选 PHP 范围的 Session，或清理过期文件',ref('Task'),{'oneOf':[ref('PHPSessionSave'),ref('PHPSessionClean')]},status='202')
add('/instances/{id}/validate','post','校验候选配置，不替换运行配置',body=ref('ConfigurationChange'))
for view in ['traffic','logs','security','process-logs']:add('/sites/{id}/'+view,'get','读取网站'+view)
add('/settings/runtime','get','读取 root 管理的面板运行配置')
add('/settings/runtime','put','暂存访问设置；120 秒重新连接确认，否则回退',body=config)
add('/runtime/confirm','post','使用一次性确认凭证提交重新连接结果',body=obj({'token':string}),public=True)
add('/settings/certificate/issue','post','提交域名或公网 IP ACME 申请',ref('Task'),obj({'host':string,'email':string,'acceptTerms':{'type':'boolean'}}),status='202')
add('/auth/username','post','修改用户名，失效旧会话并保留双因素认证',body=obj({'username':string,'password':string,'code':string}))
add('/tasks/{id}/cancel','post','取消软件安装或文件任务；变更阶段保留可恢复副本',body=config,status='202')
# Dedicated SQL/Redis APIs: sensitive requests bypass the navigation cache.
integer = {"type":"integer"}
boolean = {"type":"boolean"}
schemas['DatabaseList'] = obj({'rows':{'type':'array','items':ref('Resource')}})
schemas['DatabaseCredentialRequest'] = obj({'password':{'type':'string','writeOnly':True},'code':{'type':'string','writeOnly':True}},[])
schemas['DatabaseCredential'] = obj({'password':{'type':'string','description':'仅当前弹窗保留 30 秒；禁止缓存'},'displaySeconds':integer})
schemas['DatabaseAction'] = obj({'name':string,'username':string,'source':string,'password':{'type':'string','writeOnly':True,'minLength':12,'maxLength':128},'level':{'type':'string','enum':['readonly','readwrite','admin']},'schema':string,'table':string,'operation':{'type':'string','enum':['check','optimize','repair','analyze','vacuum']},'confirmed':boolean,'maintenanceConfirmed':boolean,'confirmDataDeletion':boolean,'sharedConfirmed':boolean,'allowRemote':boolean,'databaseName':string,'targetInstanceId':string,'revision':integer,'note':{'type':'string','maxLength':1000}},[])
add('/databases','get','按引擎/实例读取数据库或回收站；仅返回遮盖密码、可用操作及备份元数据',ref('DatabaseList'))
paths['/api/v1/databases']['get']['parameters']=[{'name':key,'in':'query','schema':schema} for key,schema in [('engine',{'type':'string','enum':['mysql','postgresql']}),('instanceId',string),('recycled',boolean)]]
for op in ['capabilities','inventory','permissions','tables','structure']:
    add('/databases/{id}/'+op,'get','读取数据库 '+op+'；由执行器校验实际实例和对象')
    if op=='structure':
        paths['/api/v1/databases/{id}/'+op]['get']['parameters'] += [{'name':key,'in':'query','required':True,'schema':string} for key in ['schema','table']]
for op in ['adopt','password','grant','access','maintain','recycle','restore','purge']:
    add('/databases/{id}/'+op,'post','提交数据库 '+op+' 任务；实例串行；回收必须先备份并确认维护窗口',ref('Task'),ref('DatabaseAction'),status='202')
add('/databases/{id}/note','post','仅更新备注；revision 冲突返回 409',body=ref('DatabaseAction'))
add('/databases/{id}/credential','post','验证当前管理员密码和 TOTP；授权限当前会话 5 分钟；响应 no-store',ref('DatabaseCredential'),ref('DatabaseCredentialRequest'))
paths['/api/v1/databases/{id}/credential']['post']['responses']['200']['headers']={'Cache-Control':{'schema':{'type':'string','enum':['no-store']}}}
schemas['RedisInput']=obj({'db':{'type':'integer','minimum':0,'maximum':65535},'cursor':string,'pattern':{'type':'string','maxLength':1024},'key':{'type':'string','format':'byte'},'type':{'type':'string','enum':['string','hash','list','set','zset','stream']},'operation':{'type':'string','enum':['set','remove-member','delete','rename','ttl']},'revision':string,'value':{'type':'string','format':'byte','description':'解码后单次编辑最多 1 MiB'},'field':{'type':'string','format':'byte'},'target':{'type':'string','format':'byte'},'index':string,'score':string,'ttl':{'type':'integer','minimum':-1,'maximum':2147483647},'confirmed':boolean},[])
schemas['RedisKey']=obj({'key':{'type':'string','format':'byte'},'name':string,'binary':boolean,'type':string,'ttlMillis':integer,'length':integer,'revision':string,'cursor':string,'editable':boolean,'complete':boolean,'notice':string,'content':{}},['key','name','binary','type','ttlMillis','length'])
schemas['RedisScan']=obj({'cursor':string,'rows':{'type':'array','items':ref('RedisKey')}})
for op in ['databases','scan','read','write','download']:
    response = ref('RedisScan') if op=='scan' else ref('RedisKey') if op=='read' else config
    add('/redis/{id}/'+op,'post','结构化 Redis '+op+'；无任意命令；WATCH 修订冲突返回 409；不缓存值',response,ref('RedisInput'))
paths['/api/v1/redis/{id}/download']['post']['responses']['200']['content']={'application/octet-stream':{'schema':{'type':'string','format':'binary','description':'Redis DUMP 序列化，最大 2 GiB'}}}
schemas['SSHSettings']=obj({'ports':{'type':'array','minItems':1,'maxItems':8,'items':{'type':'integer','minimum':1,'maximum':65535}},'password':boolean,'publicKey':boolean,'rootLogin':{'type':'string','enum':['yes','no','prohibit-password']},'maxAuthTries':{'type':'integer','minimum':1,'maximum':20},'loginGraceTime':{'type':'integer','minimum':10,'maximum':600},'clientAliveInterval':{'type':'integer','minimum':0,'maximum':3600},'clientAliveCountMax':{'type':'integer','minimum':1,'maximum':10}})
schemas['SSHStatus']=obj({'available':boolean,'editable':boolean,'reason':string,'unit':string,'settings':ref('SSHSettings'),'revision':string,'pending':obj({'deadline':integer,'ports':{'type':'array','items':integer}},[]),'sessions':string,'fail2ban':string,'keys':config,'matchWarning':string},['available'])
schemas['FirewallRule']=obj({'id':string,'revision':string,'kind':{'type':'string','enum':['port','ip','forward','region']},'protocol':string,'ports':string,'source':string,'destination':string,'action':string,'direction':string,'family':{'type':'string','enum':['ipv4','ipv6','dual']},'zone':string,'note':string,'createdAt':string,'editable':boolean,'reason':string,'raw':string,'position':integer,'target':string,'targetPorts':string,'masquerade':boolean,'countries':{'type':'array','items':string},'dataUpdatedAt':string,'runtime':boolean,'permanent':boolean,'listening':boolean},['id','revision','kind','protocol','ports','source','destination','action','direction','family','editable','runtime','permanent'])
schemas['FirewallStatus']=obj({'backend':{'type':'string','enum':['ufw','firewalld','']},'installed':boolean,'enabled':boolean,'compatible':boolean,'reason':string,'rules':{'type':'array','items':ref('FirewallRule')},'revision':string,'counts':{'type':'object','additionalProperties':integer},'zones':{'type':'array','items':string},'defaultZone':string,'pingBlocked':boolean,'raw':string,'dockerRules':string,'dockerIPv6Rules':string,'protectedPorts':{'type':'array','items':integer},'portReason':string,'pending':obj({'deadline':integer,'mode':boolean,'feature':boolean,'rollbackError':string},[]),'externalRestrictions':string})
schemas['FirewallRuleInput']=obj({key:value for key,value in schemas['FirewallRule']['properties'].items() if key in ['kind','protocol','ports','source','destination','action','direction','family','zone','note','target','targetPorts','masquerade','countries']},['kind','protocol','source','destination','action','direction','family'])
firewall_input=obj({'revision':string,'id':string,'rule':ref('FirewallRuleInput'),'source':string,'confirmedImpact':boolean,'enabled':boolean,'content':{'type':'string','maxLength':10485760},'kind':string,'ids':{'type':'array','items':string},'indices':{'type':'array','items':{'oneOf':[integer,string]}},'token':string,'connected':boolean,'jail':{'type':'string','enum':['opsview-sshd','opsview-nginx-http-auth']},'ip':string,'config':config},[])
for action in ['save','delete','dependencies','import-apply','sync-observe','sync-restore','ping','region-update','protection-install','protection-save','protection-unban']:
    add('/system/firewall/'+action,'post','提交结构化防火墙任务 '+action+'；串行执行，修订冲突返回 409',ref('Task'),firewall_input,status='202')
for action in ['export','import-preview','sync-preview','protection-status','confirm','rollback','rollback-feature']:
    add('/system/firewall/'+action,'post','防火墙 '+action+'；读取和确认返回 no-store；导入预览绑定修订与15分钟令牌',config,firewall_input)
    paths['/api/v1/system/firewall/'+action]['post']['responses']['200']['headers']={'Cache-Control':{'schema':{'type':'string','enum':['no-store']}}}
add('/system/ssh','get','读取实际 OpenSSH 配置、会话、公钥指纹及防爆破诊断',ref('SSHStatus'))
add('/system/ssh/logs','get','最近 24 小时最多 200 行 SSH journal 记录，不作为完整统计')
add('/system/firewall','get','自动读取 UFW/firewalld 系统规则、四类计数、修订、真实管理端口及待确认事务',ref('FirewallStatus'))
for path in ['/system/ssh','/system/ssh/logs','/system/firewall']:
    paths['/api/v1'+path]['get']['responses']['200']['headers']={'Cache-Control':{'schema':{'type':'string','enum':['no-store']}}}
security_bodies={
 'ssh-apply':obj({'revision':string,'settings':ref('SSHSettings'),'source':string,'confirmedImpact':{'type':'boolean','enum':[True]}}),
 'ssh-key-add':obj({'revision':string,'publicKey':string,'confirmedImpact':{'type':'boolean','enum':[True]}}),
 'ssh-key-delete':obj({'revision':string,'fingerprint':string,'confirmedImpact':{'type':'boolean','enum':[True]}}),
 'firewall-mode':obj({'enabled':boolean,'source':string,'confirmedImpact':{'type':'boolean','enum':[True]}},['enabled','confirmedImpact']),
 'firewall-reload':config,
}
for action,body in security_bodies.items():add('/system/security/'+action,'post','提交受控安全任务 '+action+'；SSH 和防火墙启停须在 120 秒内确认',ref('Task'),body,status='202')
for prefix in ['ssh','firewall']:
    add('/system/security/'+prefix+'-confirm','post','确认已经通过新设置重新登录；必须在截止时间之前',body=obj({'connected':{'type':'boolean','enum':[True]}}))
    add('/system/security/'+prefix+'-rollback','post','恢复本次变更前的设置和本次增加的规则',body=config)
spec = {"openapi":"3.0.3","info":{"title":"OpsView API","version":json.loads((root / "web/package.json").read_text())["version"]},"paths":paths,"components":{"schemas":schemas,"securitySchemes":{"session":{"type":"apiKey","in":"cookie","name":"opsview_session","description":"HTTPS 会话，Secure Cookie"},"httpSession":{"type":"apiKey","in":"cookie","name":"opsview_session_http","description":"HTTP 会话，与 HTTPS Cookie 分离以兼容关闭 SSL"}}}}
(root/'server/internal/api/openapi.json').write_text(json.dumps(spec,ensure_ascii=False,indent=2)+'\n')
