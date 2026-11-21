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
task = obj({"id": string, "resourceId": string, "scheduleId": string, "operation": string, "state": {"type": "string", "enum": ["queued", "running", "succeeded", "failed", "interrupted", "cancelled", "skipped"]}, "error": string, "createdAt": string, "updatedAt": string})
task["required"].remove("scheduleId")
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
add('/auth/login', 'post', '密码登录；需要 TOTP 时返回 428，再携带验证码提交', body=obj({"username":string,"password":string,"code":string}, ['username','password']), public=True)
paths['/api/v1/auth/login']['post']['responses']['428'] = {'description':'密码验证通过，仍需验证码；不创建会话、不累计失败次数', 'content':{'application/json':{'schema':obj({'error':string,'code':{'type':'string','enum':['totp_required']}})}}, 'headers':{'Cache-Control':{'schema':{'type':'string','enum':['no-store']}}}}
for path, fields in [('/auth/logout', {}), ('/auth/two-factor', {"action": string,"password":string,"code":string}), ('/auth/password', {"current":string,"password":string,"code":string})]: add(path,'post',path,body=obj(fields))
add('/resources','get','资源列表，敏感字段脱敏',{"type":"array","items":ref('Resource')})
add('/resources','post','创建资源配置',ref('Resource'),ref('ResourceInput'),status='201')
add('/resources/{id}','put','按 revision 更新配置',ref('Resource'),ref('ResourceUpdate'))
add('/resources/{id}','delete','删除待部署或计划资源')
add('/resources/{id}/actions/{action}','post','提交持久化任务；logs 为同步结果',ref('Task'),config,status='202')
add('/tasks/{id}','get','读取单项任务状态；不返回任务参数和凭据',ref('Task'))
add('/tasks','get','最近 200 条任务',{"type":"array","items":ref('Task')})
add('/tasks/{id}/events','get','任务日志与支持 Last-Event-ID 的 SSE')
for path in ['/overview','/catalog','/plugins','/audit','/settings','/system','/openapi.json','/docker/{collection}']: add(path,'get',path)
add('/settings','put','旧设置接口；面板名称和侧栏标题不可修改，访问范围修改使用运行配置事务',body=config)
add('/settings/backups','get','面板配置备份列表')
add('/settings/backup','post','提交加密面板配置备份',ref('Task'),config,status='202')
add('/settings/backup/{file}','get','下载面板配置备份')
paths['/api/v1/settings/backup/{file}']['get']['responses']['200']['content'] = {'application/octet-stream': {'schema': {'type':'string','format':'binary'}}}
add('/settings/certificate','post','上传面板 PEM 证书及私钥',body=obj({"certificate":string,"privateKey":string}))
add('/resources/{id}/download','get','下载加密备份或导出数据库')
paths['/api/v1/resources/{id}/download']['get']['responses']['200']['content'] = {'application/octet-stream': {'schema': {'type':'string','format':'binary'}}}
paths['/api/v1/resources/{id}/download']['get']['parameters'] += [{'name':'artifact','in':'query','schema':string}]
schemas['FileInput']=obj({'scope':{'type':'string','enum':['server']},'recentPaths':{'type':'array','items':string,'maxItems':15,'description':'metadata 操作校验最近访问目录，仅返回当前允许浏览的路径'},'resourceId':string,'action':{'type':'string','enum':['identities','list','stat','read','write','mkdir','note','metadata','favorite','copy','move','rename','permissions','delete','search','size','compress','extract','trash','upload-create','upload-status','upload-finish','upload-cancel']},'path':string,'target':string,'paths':{'type':'array','items':string,'maxItems':1000},'revision':string,'text':{'type':'string','maxLength':10485760},'content':{'oneOf':[string,{'type':'boolean'}],'description':'旧接口为 Base64；服务器 search 操作为布尔内容搜索开关'},'permissionMask':{'type':'integer','minimum':0,'maximum':511},'permissionValue':{'type':'integer','minimum':0,'maximum':511},'revisions':{'type':'object','additionalProperties':string},'mode':string,'owner':string,'group':string,'recursive':{'type':'boolean'},'confirmed':{'type':'boolean'},'query':string,'searchContent':{'type':'boolean','description':'保留字段；内容搜索使用 content=true'},'hidden':{'type':'boolean'},'filter':string,'page':{'type':'integer','minimum':1},'pageSize':{'type':'integer','minimum':1,'maximum':500},'sort':string,'descending':{'type':'boolean'},'conflict':{'type':'string','enum':['skip','overwrite','keep']},'trashAction':{'type':'string','enum':['list','restore','purge','empty']},'id':string,'size':{'type':'integer','format':'int64','maximum':2147483648},'sha256':string,'note':string,'remove':{'type':'boolean'}},['action'])
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
schemas['SiteCreate']=obj({'requestId':{'type':'string','description':'会话内随机幂等创建请求标识'},'name':string,'config':config,'certificate':{'type':'string','writeOnly':True},'privateKey':{'type':'string','writeOnly':True},'database':obj({'instanceId':string,'name':string,'username':string,'password':{'type':'string','writeOnly':True},'encoding':string,'collation':string},['instanceId','name','username','password'])},['name','config'])
schemas['SiteProvision']=obj({'siteId':string,'databaseId':string,'taskId':string})
add('/sites','post','唯一 Nginx 绑定，原子预留站点、可选专用数据库和编排任务；凭据加密',ref('SiteProvision'),ref('SiteCreate'),status='202')
add('/sites/{id}/provision','post','重试原创建任务；复用已完成数据库，不自动删除成功资源',ref('SiteProvision'),config,status='202')
add('/sites/provisions/{requestId}','get','查询幂等创建请求，仅返回站点、任务标识、修订号和数据库完成状态')
schemas['SiteRuntimeUser']=obj({'name':string,'uid':string,'gid':string,'group':string,'source':string,'shared':{'type':'boolean'}})
schemas['SiteRuntimePreview']=obj({'fingerprint':string,'path':string,'entries':{'type':'integer'},'changes':string,'confirmedRequired':{'type':'boolean'}})
add('/sites/runtime-users','get','可用运行用户、实际主组及账号来源；排除面板账号和 UID 0 别名，no-store',obj({'users':{'type':'array','items':ref('SiteRuntimeUser')},'default':string,'notice':string}))
add('/sites/runtime-preview','post','只读检查权限、链接、硬链接及挂载边界；现有站点绑定修订，no-store',ref('SiteRuntimePreview'),obj({'id':string,'revision':{'type':'integer'},'config':config},['config']))
schemas['SiteDeleteItem']=obj({'id':string,'revision':{'type':'integer'},'deleteDirectory':{'type':'boolean','default':False},'deleteDatabase':{'type':'boolean','default':False},'deleteLogs':{'type':'boolean','default':False}},['id','revision'])
add('/sites/delete-preview','post','读取实际关联资源；预览绑定修订和选项，确认等待5秒、有效5分钟',body=obj({'items':{'type':'array','items':ref('SiteDeleteItem'),'minItems':1,'maxItems':100}}))
add('/sites/delete','post','按预览串行删除；可选数据进入30天回收站，旧备份保留',obj({'taskId':string}),obj({'previewId':string,'confirmed':{'type':'boolean'},'maintenanceConfirmed':{'type':'boolean'}},['previewId','confirmed']),status='202')
add('/sites/delete-tasks/{id}','get','读取批量删除逐站结果及失败原因，不返回凭据和原始配置')
add('/sites/{id}/settings','post','提交配置候选；revision 冲突返回409，实际应用成功后更新元数据',obj({'taskId':string}),ref('ResourceUpdate'),status='202')
for endpoint,method in [('/sites','post'),('/sites/{id}/provision','post'),('/sites/{id}/settings','post'),('/resources','post'),('/resources/{id}','put')]:
    paths['/api/v1'+endpoint][method].setdefault('parameters',[]).extend([{'name':'X-OpsView-Password','in':'header','schema':{'type':'string','writeOnly':True},'description':'root 新建或运行配置改变时重新验证当前管理员；不进入任务参数或审计正文'},{'name':'X-OpsView-TOTP','in':'header','schema':{'type':'string','writeOnly':True},'description':'管理员已启用 TOTP 时必须填写'}])
add('/site-categories','get','网站共享分类',obj({'rows':{'type':'array','items':string}}))
add('/site-categories','post','新增、改名、删除网站分类；删除后归默认分类',body=obj({'old':string,'name':string,'remove':{'type':'boolean'}},[]))
for view in ['traffic','logs','security','process-logs','configuration','directories']:add('/sites/{id}/'+view,'get','读取网站'+view)
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
schemas['SSHStatus']=obj({'available':boolean,'editable':boolean,'reason':string,'unit':string,'settings':ref('SSHSettings'),'revision':string,'pending':obj({'deadline':integer,'ports':{'type':'array','items':integer}},[]),'sessions':string,'fail2ban':string,'keys':config,'matchWarning':string,'service':config},['available'])
schemas['FirewallRule']=obj({'id':string,'revision':string,'kind':{'type':'string','enum':['port','ip','forward','region']},'protocol':string,'ports':string,'source':string,'destination':string,'action':string,'direction':string,'family':{'type':'string','enum':['ipv4','ipv6','dual']},'zone':string,'note':string,'createdAt':string,'editable':boolean,'reason':string,'raw':string,'position':integer,'target':string,'targetPorts':string,'masquerade':boolean,'countries':{'type':'array','items':string},'dataUpdatedAt':string,'runtime':boolean,'permanent':boolean,'listening':boolean},['id','revision','kind','protocol','ports','source','destination','action','direction','family','editable','runtime','permanent'])
schemas['FirewallStatus']=obj({'backend':{'type':'string','enum':['ufw','firewalld','']},'installed':boolean,'enabled':boolean,'compatible':boolean,'reason':string,'warning':string,'modeAvailable':boolean,'modeReason':string,'managementSource':string,'rules':{'type':'array','items':ref('FirewallRule')},'revision':string,'counts':{'type':'object','additionalProperties':integer},'zones':{'type':'array','items':string},'defaultZone':string,'pingBlocked':boolean,'raw':string,'dockerRules':string,'dockerIPv6Rules':string,'protectedPorts':{'type':'array','items':integer},'portReason':string,'pending':obj({'deadline':integer,'mode':boolean,'feature':boolean,'rollbackError':string},[]),'externalRestrictions':string})
schemas['FirewallRuleInput']=obj({key:value for key,value in schemas['FirewallRule']['properties'].items() if key in ['kind','protocol','ports','source','destination','action','direction','family','zone','note','target','targetPorts','masquerade','countries']},['kind','protocol','source','destination','action','direction','family'])
firewall_input=obj({'revision':string,'id':string,'rule':ref('FirewallRuleInput'),'source':string,'confirmedImpact':boolean,'enabled':boolean,'content':{'type':'string','maxLength':10485760},'kind':string,'ids':{'type':'array','items':string},'indices':{'type':'array','items':{'oneOf':[integer,string]}},'token':string,'connected':boolean,'jail':{'type':'string','enum':['opsview-sshd','opsview-nginx-http-auth']},'ip':string,'config':config},[])
for action in ['save','delete','dependencies','import-apply','sync-observe','sync-restore','ping','region-update','protection-install','protection-save','protection-unban']:
    add('/system/firewall/'+action,'post','提交结构化防火墙任务 '+action+'；串行执行，修订冲突返回 409',ref('Task'),firewall_input,status='202')
for action in ['export','import-preview','sync-preview','protection-status','confirm','rollback','rollback-feature']:
    add('/system/firewall/'+action,'post','防火墙 '+action+'；读取和确认返回 no-store；导入预览绑定修订与15分钟令牌',config,firewall_input)
    paths['/api/v1/system/firewall/'+action]['post']['responses']['200']['headers']={'Cache-Control':{'schema':{'type':'string','enum':['no-store']}}}
add('/schedules','get','读取计划任务、服务器时区与真实下次执行时间；手动执行使用 resources/{id}/actions/run')
add('/system/ssh/users','get','本机普通账号和 root 的脱敏状态，不返回密码哈希')
add('/system/security/ssh-user','post','新鲜管理员密码/TOTP 验证后同步操作本机账号；密码不进入队列、日志和审计',config,obj({'operation':{'type':'string','enum':['create','password','lock','unlock','delete']},'username':string,'revision':string,'password':{'type':'string','format':'password'},'currentPassword':{'type':'string','format':'password'},'code':string,'confirmedImpact':boolean},['operation','username','currentPassword','confirmedImpact']))
add('/system/security/ssh-service-confirm','post','验证备用控制台后确认保留 SSH 停用；超时自动恢复',config,obj({'consoleConfirmed':{'type':'boolean','enum':[True]}}))
add('/system/ssh','get','读取实际 OpenSSH 配置、会话、公钥指纹及防爆破诊断',ref('SSHStatus'))
add('/system/ssh/logs','get','结构化 SSH journal 采样，最多 2000 行，不作为完整登录总量')
paths['/api/v1/system/ssh/logs']['get']['parameters']=[{'name':'period','in':'query','schema':{'type':'string','enum':['24h','7d','30d']}}]
add('/system/firewall','get','自动读取 UFW/firewalld 系统规则、四类计数、修订、真实管理端口及待确认事务',ref('FirewallStatus'))
for path in ['/system/ssh','/system/ssh/logs','/system/firewall']:
    paths['/api/v1'+path]['get']['responses']['200']['headers']={'Cache-Control':{'schema':{'type':'string','enum':['no-store']}}}
security_bodies={
 'ssh-service':obj({'operation':{'type':'string','enum':['start','stop','restart']},'confirmedImpact':boolean}),
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

# rc.17: grouped settings, durable schedules, authenticated archives and notifications.
boolean={"type":"boolean"}; integer={"type":"integer"}; strings={"type":"array","items":string}
auth_fields={"password":string,"code":string}
schemas['SettingPatch']=obj({'patch':config,'revision':integer,**auth_fields,'basicPassword':string},['patch','revision'])
schemas['SettingGroup']=obj({'config':config,'revision':integer})
schemas['ScheduleRun']=obj({'id':string,'state':task['properties']['state'],'error':string,'createdAt':string,'updatedAt':string,'result':config,'truncated':boolean},['id','state','createdAt','updatedAt'])
schemas['ScheduleConfig']=obj({'operation':{'type':'string','enum':['backup','renew','shell','python','http','directory','panel','rotate','cleanup','site-state','time-sync']},'cron':{'type':'string','description':'五段 Cron，最短一分钟'},'timezone':string,'enabled':boolean,'category':string,'targetId':string,'targetIds':strings,'user':string,'interpreter':string,'workingDirectory':string,'script':{'type':'string','maxLength':1048576},'args':strings,'environment':{'type':'object','additionalProperties':string},'timeoutSeconds':integer,'retentionDays':integer,'path':string,'storageDirectory':string,'storageId':string,'excludes':string,'retention':integer,'method':{'type':'string','enum':['GET','POST']},'url':string,'headers':{'type':'object','additionalProperties':string},'body':string,'allowPrivate':boolean,'expectedStatus':integer},['operation','cron'])
add('/public/preferences','get','公开登录外观；不含内部安全配置',public=True)
add('/settings/groups/{group}','get','按分组读取设置与同一事务修订',ref('SettingGroup'))
add('/settings/groups/{group}','patch','局部更新；安全配置重新验证；访问变更返回 120 秒确认令牌',body=ref('SettingPatch'))
add('/settings/confirm','post','重新连接后确认访问设置',body=obj({'token':string}))
add('/settings/regions','get','本地登录地区数据状态与当前来源匹配')
add('/settings/regions','post','原子更新 IPv4/IPv6 地区数据；失败保留当前数据',ref('Task'),config,status='202')
add('/schedule-categories','get','计划分类列表',strings)
add('/schedule-categories','post','创建、重命名或删除分类；删除时归入默认分类',body=obj({'old':string,'name':string,'remove':boolean},[]))
add('/schedules/capabilities','get','实际执行用户、Python 解释器与任务类型')
add('/schedules/preview','post','按保存时区预览未来五次执行时间',body=obj({'cron':string,'timezone':string}))
add('/schedules/import','post','预览或原子导入；计划默认暂停，root 不继承授权',body=config)
add('/schedules/{id}','get','显式读取单个计划完整配置，私密变量遮盖')
add('/schedules/{id}/runs','get','独立执行历史、实际退出码与截断状态',{'type':'array','items':ref('ScheduleRun')})
add('/schedules/{id}/export','get','导出暂停计划；不导出私密变量、请求头和 root 授权')
add('/schedules/{id}/stop','post','停止执行及整个 systemd 控制组',body=config)
add('/schedules/{id}/clear-history','post','清理指定日期前的终态历史',body=obj({'before':string}))
for path in ['/resources','/resources/{id}']:
 method='post' if path=='/resources' else 'put'
 paths['/api/v1'+path][method].setdefault('parameters',[]).extend([{'name':k,'in':'header','required':False,'schema':string,'description':'root 脚本新增、启用或内容变更需重新验证'} for k in ['X-OpsView-Password','X-OpsView-TOTP']])
for path in ['/scripts','/scripts/{id}']:
 add(path,'get','脚本库列表或按修订显式读取正文；不执行脚本')
 add(path,'post' if path=='/scripts' else 'put','保存加密脚本与独立修订',ref('Resource'),config)
add('/scripts/{id}','delete','删除模板；不修改已有计划')
paths['/api/v1/scripts/{id}']['get']['parameters'] += [{'name':'revision','in':'query','schema':integer},{'name':'history','in':'query','schema':{'type':'string','enum':['1']}}]
add('/auth/sessions','get','已登录会话及当前标识')
add('/auth/sessions','post','重新验证后撤销单个或全部会话',body=config)
add('/auth/tokens/{kind}','get','API 或临时凭据元数据；不返回令牌')
add('/auth/tokens/{kind}','post','重新验证后创建或撤销；创建令牌仅返回一次',body=config)
add('/auth/temporary','post','一次兑换十分钟只读授权，令牌不进 URL',body=obj({'token':string}),public=True)
add('/auth/client-certificate','post','重新验证后生成客户端证书，私钥仅此次返回',body=obj(auth_fields))
add('/auth/passkeys/list','get','Passkey 凭据及当前 RP/安全上下文可用性')
for step in ['register-begin','register-finish','delete','login-begin','login-finish']:
 add('/auth/passkeys/'+step,'post','WebAuthn 一次挑战、RP ID、Origin 与用户验证；登录保留 TOTP',body=config,public=step.startswith('login'))
for kind in ['rules','channels','records']:
 add('/notifications/'+kind,'get','告警规则、脱敏通知渠道或真实发送历史',{'type':'array','items':config})
 if kind!='records':
  add('/notifications/'+kind,'post','创建告警配置',ref('Resource'),config,status='201')
  add('/notifications/'+kind+'/{id}','put','按修订更新告警配置',ref('Resource'),config)
  add('/notifications/'+kind+'/{id}','delete','删除无依赖的告警配置')
add('/notifications/channels/{id}/test','post','真实发送测试并记录结果；不记录秘密',body=config)
add('/backups/archives','get','本机受信任业务归档和存储元数据')
add('/backups/archives/{action}','post','排队校验、恢复预览、删除或恢复目录；同归档互斥',ref('Task'),config,status='202')
add('/backups/tasks/{id}','get','归档任务状态、实际错误与结果')
add('/backups/download','get','经认证流式下载本地或已登记 S3 的归档')
add('/backups/upload','put','流式上传 .ovb/.ovp；2 GiB；验证原密钥与历史，拒绝同名覆盖')
paths['/api/v1/backups/upload']['put']['requestBody']={'required':True,'content':{'application/octet-stream':{'schema':{'type':'string','format':'binary'}}}}
for path in ['/backups/download','/backups/upload']:
 method='get' if path.endswith('download') else 'put'
 paths['/api/v1'+path][method].setdefault('parameters',[]).extend([{'name':'file','in':'query','required':True,'schema':string},{'name':'backupId','in':'query','schema':string}])
paths['/api/v1/backups/download']['get']['responses']['200']['content']={'application/octet-stream':{'schema':{'type':'string','format':'binary'}}}

# Website settings use structured policies; general site fields remain compatible.
schemas['SiteBinding']=obj({'host':string,'port':{'type':'integer','minimum':1,'maximum':65535},'directory':string},['host'])
schemas['SiteRedirect']=obj({'path':string,'target':string,'code':{'type':'integer','enum':[301,302,307,308]},'exact':{'type':'boolean'}})
schemas['SiteAccess']=obj({'path':string,'username':string,'password':{'type':'string','description':'新密码提交明文，经服务端哈希后入队；读取返回遮盖值，原样提交保留密码'}})
site_settings=obj({'domains':{'type':'string','description':'空白分隔；域名、泛域名、IP、自定义 HTTP 端口；HTTPS 在证书设置管理'},'subdirectoryBindings':{'type':'array','items':ref('SiteBinding'),'maxItems':32},'accessRules':{'type':'object','additionalProperties':ref('SiteAccess')},'denyPaths':string,'denyExtensions':string,'allowAddresses':string,'denyAddresses':string,'limitEnabled':{'type':'boolean'},'maxConnections':{'type':'integer'},'perIPConnections':{'type':'integer'},'rateKB':{'type':'integer'},'redirects':{'type':'array','items':ref('SiteRedirect')},'hotlinkEnabled':{'type':'boolean'},'hotlinkStatus':{'type':'integer','enum':[403,404]},'redirect404Target':string,'hotlinkDomains':string,'hotlinkExtensions':string,'hotlinkAllowEmpty':{'type':'boolean'},'gzipEnabled':{'type':'boolean'},'gzipLevel':{'type':'integer','minimum':1,'maximum':9},'cacheEnabled':{'type':'boolean'},'cacheExtensions':string,'cacheSeconds':{'type':'integer'},'responseHeaders':{'type':'object','additionalProperties':string},'corsEnabled':{'type':'boolean'},'corsOrigin':string,'corsMethods':string,'corsHeaders':string,'corsCredentials':{'type':'boolean'},'clientAuthEnabled':{'type':'boolean'},'clientCA':{'type':'string','maxLength':65536,'description':'仅客户端公共 CA 证书，不提交私钥'},'accessLogDisabled':{'type':'boolean'},'phpIsolation':{'type':'boolean'}},[])
site_settings['properties'].update({'user':{'type':'string','description':'isolated、www-data、经运行用户能力接口验证的已有账号或 root；禁止其他 UID 0 别名'},'runtimeIsolation':{'type':'boolean','description':'新托管项目强制启用；旧项目经预览确认后整改，禁止普通保存关闭'},'releasePort':{'type':'boolean','default':False,'description':'仅放行本机 TCP；不启用防火墙，不修改云安全组或监听地址'},'permissionPreview':{'type':'string','description':'只读权限预检指纹，变化后必须重新确认'},'runtimeUser':{'type':'string','readOnly':True},'runtimeGroup':{'type':'string','readOnly':True},'runtimeUID':{'type':'string','readOnly':True},'runtimeGID':{'type':'string','readOnly':True},'runtimeIsolationApplied':{'type':'boolean','readOnly':True}})
site_settings['additionalProperties']=True
schemas['SiteSettings']=site_settings
schemas['SiteCreate']['properties']['config']=ref('SiteSettings')
paths['/api/v1/sites/{id}/settings']['post']['requestBody']['content']['application/json']['schema']=obj({'name':string,'config':ref('SiteSettings'),'revision':{'type':'integer'}})
paths['/api/v1/sites/{id}/logs']['get'].setdefault('parameters',[]).append({'name':'kind','in':'query','schema':{'type':'string','enum':['access','error'],'default':'access'}})
paths['/api/v1/sites/{id}/configuration']['get']['responses']['200']['content']['application/json']['schema']=obj({'current':string,'managed':string,'drift':{'type':'boolean'},'history':{'type':'array','items':obj({'revision':string,'content':string})}})
paths['/api/v1/sites/{id}/directories']['get']['responses']['200']['content']['application/json']['schema']=obj({'directories':{'type':'array','items':string}})
spec = {"openapi":"3.0.3","info":{"title":"OpsView API","version":json.loads((root / "web/package.json").read_text())["version"]},"paths":paths,"components":{"schemas":schemas,"securitySchemes":{"session":{"type":"apiKey","in":"cookie","name":"opsview_session","description":"HTTPS 会话，Secure Cookie"},"httpSession":{"type":"apiKey","in":"cookie","name":"opsview_session_http","description":"HTTP 会话，与 HTTPS Cookie 分离以兼容关闭 SSL"}}}}
(root/'server/internal/api/openapi.json').write_text(json.dumps(spec,ensure_ascii=False,indent=2)+'\n')
