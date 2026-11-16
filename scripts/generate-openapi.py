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
schemas = {"Resource": resource, "Task": task, "Error": obj({"error": string,"code":string,"revision":string},["error"]), "ResourceInput": obj({"kind": string, "name": string, "config": config}), "ResourceUpdate": obj({"name": string, "config": config, "revision": {"type": "integer"}}), "Envelope": obj({"keyId": string, "payload": string, "signature": string})}
paths = {}
def add(path, method, summary, response=config, body=None, public=False, status="200"):
    route = paths.setdefault('/api/v1' + path, {})
    op = {"operationId": method + '_' + path.replace('/', '_').replace('{', '').replace('}', '').replace('-', '_').strip('_'), "summary": summary, "security": [] if public else [{"session": []}], "responses": {status: {"description": "成功", "content": {"application/json": {"schema": response}}}, "400": {"description": "校验失败", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Error"}}}}, "401": {"description": "未登录"}, "409": {"description": "资源冲突"}}}
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
add('/files','post','受管理目录文件操作',body=obj({"resourceId":string,"action":string,"path":string,"content":string,"mode":string},['resourceId','action','path']))
add('/catalog/import','post','验证签名并导入软件配方',body=ref('Envelope'),status='201')
add('/plugins/import','post','导入签名插件清单',body=obj({"name":string,"envelope":ref('Envelope')}),status='201')
add('/repositories/{id}/refresh','post','验证仓库清单并同步插件',body=config)
add('/system/actions/{action}','post','受控系统操作',body=config,status='202')
add('/docker/containers/{id}/{action}','post','容器操作或日志',body=config,status='202')
add('/docker/manage/{collection}/{action}','post','固定摘要拉取镜像及管理网络和卷',ref('Task'),config,status='202')

schemas["CatalogEntry"]=obj({"id":string,"software":string,"version":string,"description":string,"platforms":{"type":"array","items":string},"recommended":{"type":"boolean"},"source":string,"image":string,"sha256":string,"recipeRevision":string,"disabledReason":string,"methods":{"type":"array","items":string},"availability":{"type":"object","additionalProperties":string},"verifiedSystems":{"type":"object","additionalProperties":{"type":"array","items":string}},"binaries":{"type":"array","items":obj({"source":string,"sha256":string,"buildPrefix":string,"system":string})}},["id","software","version","description","platforms"])
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
paths['/api/v1/instances/{id}/session']['get'].setdefault('parameters',[]).append({'name':'siteId','in':'query','schema':string})
add('/instances/{id}/validate','post','校验候选配置，不替换运行配置',body=ref('ConfigurationChange'))
for view in ['traffic','logs']:add('/sites/{id}/'+view,'get','读取网站'+view)
add('/settings/runtime','get','读取 root 管理的面板运行配置')
add('/settings/runtime','put','暂存访问设置；120 秒重新连接确认，否则回退',body=config)
add('/runtime/confirm','post','使用一次性确认凭证提交重新连接结果',body=obj({'token':string}),public=True)
add('/settings/certificate/issue','post','提交域名或公网 IP ACME 申请',ref('Task'),obj({'host':string,'email':string,'acceptTerms':{'type':'boolean'}}),status='202')
add('/auth/username','post','修改用户名，失效旧会话并保留双因素认证',body=obj({'username':string,'password':string,'code':string}))
add('/tasks/{id}/cancel','post','取消软件安装并终止构建进程组',body=config,status='202')
spec = {"openapi":"3.0.3","info":{"title":"OpsView API","version":json.loads((root / "web/package.json").read_text())["version"]},"paths":paths,"components":{"schemas":schemas,"securitySchemes":{"session":{"type":"apiKey","in":"cookie","name":"opsview_session"}}}}
(root/'server/internal/api/openapi.json').write_text(json.dumps(spec,ensure_ascii=False,indent=2)+'\n')
