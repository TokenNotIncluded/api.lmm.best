from pathlib import Path
import ast
import json

changed=[]
def edit(path, old, new):
    p=Path(path); text=p.read_text()
    if text.count(old)!=1: raise RuntimeError(f'{path}: expected exactly one source match')
    p.write_text(text.replace(old,new)); changed.append(path)

policy="default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; connect-src 'self'; worker-src 'self' blob:; object-src 'none'; base-uri 'none'; form-action 'self'; frame-src 'none'"
conditional='    set $lmm_extore_callback_csp "";\n    if ($args != "") { set $lmm_extore_callback_csp "'+policy+'"; }\n'
map_block='map $args $lmm_extore_callback_csp {\n    "" "";\n    default "'+policy+'";\n}\n'
edit('packaging/common/lmm-api/edge-policy/nginx/http-map.conf',
     'map $request_uri $lmm_oauth_request_loggable {',
     '# A map keeps conditional headers out of nginx implicit if locations.\n'+map_block+'\nmap $request_uri $lmm_oauth_request_loggable {')
edit('packaging/common/lmm-api/edge-policy/nginx/lmm-api-locations.conf',conditional,'')
path='scripts/render-frontend-proxy-overlay.py'
text=Path(path).read_text()
nodes=[n for n in ast.walk(ast.parse(text)) if isinstance(n,ast.Constant) and isinstance(n.value,str) and n.value.startswith('location = /store/manage {')]
if len(nodes)!=1 or nodes[0].value.count(conditional)!=1: raise RuntimeError('expected one callback overlay literal')
edit(path,ast.get_source_segment(text,nodes[0]),repr(nodes[0].value.replace(conditional,'')))
path='scripts/test-render-frontend-proxy-overlay.py'
edit(path,"ROOT=pathlib.Path(__file__).resolve().parent.parent\n", "ROOT=pathlib.Path(__file__).resolve().parent.parent\nMAP_TEXT=(ROOT/'packaging/common/lmm-api/edge-policy/nginx/http-map.conf').read_text()\nCSP_MAP='map $args $lmm_extore_callback_csp {\\n'+MAP_TEXT.split('map $args $lmm_extore_callback_csp {\\n',1)[1].split('\\n}',1)[0]+'\\n}\\n'\n")
edit(path,"http {{ access_log off; '+dirs+", "http {{ access_log off; '+CSP_MAP+dirs+")
path='docs/development/store-extore-import.md'
edit(path,'部署需同时更新 Go、前端及所使用的边缘配置。', '部署需同时更新 Go、前端及所使用的边缘配置。Nginx 的 `http-map.conf` 和 locations 配置必须一起更新，先通过 `nginx -t` 再重新加载。回调内容策略使用 http 级 `map`，避免在 location 中以 `if` 设置变量导致带查询参数的页面返回 404。')
Path('/tmp/extore-edge-changed.json').write_text(json.dumps(sorted(set(changed))))
print('\n'.join(sorted(set(changed))))
