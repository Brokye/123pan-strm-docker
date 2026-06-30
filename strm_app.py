import base64
import json
import os
import re
import time
from pathlib import Path
from typing import Any, Dict, List
from urllib.parse import quote

import uvicorn
import yaml
from fastapi import FastAPI, HTTPException
from fastapi.responses import HTMLResponse, JSONResponse, RedirectResponse
from pydantic import BaseModel

from get_file_url import get_file_url

BASE_DIR = Path(__file__).resolve().parent
# Docker/NAS 持久化：默认使用 /data；普通运行时可用本地 strm_data
DEFAULT_DATA_DIR = Path("/data") if Path("/data").exists() else BASE_DIR / "strm_data"
DATA_DIR = Path(os.getenv("DATA_DIR", str(DEFAULT_DATA_DIR))).expanduser()
LIB_DIR = DATA_DIR / "libraries"
CONFIG_FILE = DATA_DIR / "config.json"
SETTINGS_PATH = Path(os.getenv("SETTINGS_PATH", str(DATA_DIR / "settings.yaml"))).expanduser()
DEFAULT_OUTPUT_DIR = os.getenv("STRM_OUTPUT_DIR", "/strm" if Path("/strm").exists() else str(BASE_DIR / "STRM输出"))
LIB_DIR.mkdir(parents=True, exist_ok=True)
DATA_DIR.mkdir(parents=True, exist_ok=True)
# 首次运行复制默认 settings.yaml 到持久化目录
if not SETTINGS_PATH.exists():
    try:
        SETTINGS_PATH.write_text((BASE_DIR / "settings.yaml").read_text(encoding="utf-8"), encoding="utf-8")
    except Exception:
        pass

VIDEO_EXTS = {'.mp4','.mkv','.ts','.m2ts','.avi','.mov','.wmv','.flv','.rmvb','.webm','.mpg','.mpeg','.iso'}
BAD_CHARS = '<>:"/\\|?*'


def load_settings_yaml():
    with open(SETTINGS_PATH, "r", encoding="utf-8") as f:
        return yaml.safe_load(f.read()) or {}

settings = load_settings_yaml()


def safe_name(s: str) -> str:
    s = str(s or '').replace('\x00','')
    for c in BAD_CHARS:
        s = s.replace(c, ' ')
    s = re.sub(r'\s+', ' ', s).strip().rstrip('.')
    return s or 'unnamed'


def safe_rel_path(p: str) -> Path:
    parts = []
    for x in str(p or '').replace('\\','/').split('/'):
        x = x.strip()
        if not x or x in {'.','..'}:
            continue
        parts.append(safe_name(x))
    return Path(*parts) if parts else Path('unnamed')


def config() -> Dict[str, Any]:
    if CONFIG_FILE.exists():
        try:
            c = json.loads(CONFIG_FILE.read_text(encoding='utf-8'))
            c.setdefault("output_dir", DEFAULT_OUTPUT_DIR)
            c.setdefault("server_base", os.getenv("SERVER_BASE", f"http://127.0.0.1:{settings.get('WEBDAV_PORT',8000)}"))
            c.setdefault("include_subtitles", False)
            c.setdefault("pan_username", settings.get("123PAN_USERNAME", ""))
            c.setdefault("pan_password", settings.get("123PAN_PASSWORD", ""))
            return c
        except Exception:
            pass
    return {
        "output_dir": DEFAULT_OUTPUT_DIR,
        "server_base": os.getenv("SERVER_BASE", f"http://127.0.0.1:{settings.get('WEBDAV_PORT',8000)}"),
        "include_subtitles": False,
        "pan_username": settings.get("123PAN_USERNAME", ""),
        "pan_password": settings.get("123PAN_PASSWORD", ""),
    }


def save_config(c: Dict[str, Any]):
    CONFIG_FILE.write_text(json.dumps(c, ensure_ascii=False, indent=2), encoding='utf-8')


def update_settings_account(username: str, password: str):
    """把前端配置的 123 账号密码同步写入 settings.yaml；账号变化时自动清理旧 token。"""
    path = SETTINGS_PATH
    data = load_settings_yaml()
    old_user = data.get("123PAN_USERNAME", "")
    old_pwd = data.get("123PAN_PASSWORD", "")
    new_user = username or ""
    new_pwd = password or ""
    data["123PAN_USERNAME"] = new_user
    data["123PAN_PASSWORD"] = new_pwd
    with open(path, "w", encoding="utf-8") as f:
        yaml.safe_dump(data, f, allow_unicode=True, sort_keys=False)
    if (old_user != new_user) or (old_pwd != new_pwd):
        cache_path = Path(os.getenv("CACHE_PATH", str(DATA_DIR / "cache.json")))
        if cache_path.exists():
            try:
                cache_path.unlink()
                print("检测到123账号或密码变化，已自动删除旧 token 缓存:", cache_path)
            except Exception as e:
                print("删除旧 token 缓存失败:", e)
    global settings
    settings = data


def lib_path(lib_id: str) -> Path:
    return LIB_DIR / f"{safe_name(lib_id)}.json"


def load_lib(lib_id: str) -> Dict[str, Any]:
    p = lib_path(lib_id)
    if not p.exists():
        raise HTTPException(404, 'library not found')
    return json.loads(p.read_text(encoding='utf-8'))


def normalize_library(raw: Any, name: str = '') -> Dict[str, Any]:
    if isinstance(raw, dict) and isinstance(raw.get('files'), list):
        files = raw['files']
        common = raw.get('commonPath','')
        meta = {k:v for k,v in raw.items() if k != 'files'}
    elif isinstance(raw, list):
        files = raw
        common = ''
        meta = {}
    else:
        raise ValueError('不支持的 JSON 格式：需要 {files:[...]} 或数组')

    out_files = []
    for i, item in enumerate(files):
        if not isinstance(item, dict):
            continue
        path = item.get('path') or item.get('Path') or item.get('name') or item.get('FileName') or item.get('filename')
        etag = item.get('etag') or item.get('Etag') or item.get('ETag') or item.get('md5') or item.get('hash')
        size = item.get('size') or item.get('Size') or 0
        if not path or not etag:
            continue
        try:
            size_int = int(size)
        except Exception:
            size_int = 0
        out_files.append({"idx": len(out_files), "path": str(path).replace('\\','/'), "etag": str(etag), "size": size_int})
    lib_id = safe_name((name or meta.get('commonPath') or meta.get('name') or f"library_{int(time.time())}").strip('/\\'))
    return {"id": lib_id, "name": (name or meta.get('commonPath') or lib_id).strip('/\\'), "commonPath": common, "createdAt": int(time.time()), "meta": meta, "files": out_files}


def list_libraries():
    rows = []
    for p in sorted(LIB_DIR.glob('*.json'), key=lambda x: x.stat().st_mtime, reverse=True):
        try:
            d = json.loads(p.read_text(encoding='utf-8'))
            total = len(d.get('files', []))
            video = sum(1 for f in d.get('files', []) if Path(f.get('path','')).suffix.lower() in VIDEO_EXTS)
            rows.append({"id": d.get('id') or p.stem, "name": d.get('name') or p.stem, "total": total, "video": video, "createdAt": d.get('createdAt')})
        except Exception:
            pass
    return rows


def is_hex_md5(etag: str) -> bool:
    return bool(re.fullmatch(r"[0-9a-fA-F]{32}", str(etag or "")))


def base62_to_hex_candidates(etag: str) -> List[str]:
    """把 123 秒传 JSON 中的 base62 etag 尝试还原为 32位 hex MD5。
    不同导出脚本可能使用不同 alphabet，因此生成多个候选，播放时依次尝试。
    """
    etag = str(etag or "").strip()
    if not etag:
        return []
    if is_hex_md5(etag):
        return [etag.lower()]
    alphabets = [
        "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ",
        "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz",
        "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789",
        "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789",
    ]
    out = []
    for alphabet in alphabets:
        try:
            n = 0
            for ch in etag:
                n = n * 62 + alphabet.index(ch)
            h = f"{n:032x}"
            if len(h) <= 32:
                h = h[-32:].zfill(32)
                if h not in out:
                    out.append(h)
        except Exception:
            continue
    # 最后保留原值作为兜底
    if etag not in out:
        out.append(etag)
    return out


def get_file_url_with_etag_candidates(name: str, etag: str, size: int) -> str:
    candidates = base62_to_hex_candidates(etag)
    last_url = None
    for e in candidates:
        print(f"尝试 ETag: {etag} -> {e}")
        url = get_file_url(name=name, etag=e, size=int(size))
        last_url = url
        # get_file_url 失败时会返回一个固定兜底 mp4，识别并继续尝试下一个候选
        if url and "222.186.21.40:33333/NGGYU.mp4" not in url:
            return url
    return last_url


def make_play_url(base: str, file_id: int, etag: str, size: int, filename: str) -> str:
    # 新 STRM 格式：/play/{id}/{etag}/{size}/{原文件名}
    # 最后一段保留原文件后缀，方便播放器/日志识别。
    return base.rstrip('/') + f"/play/{file_id}/{quote(str(etag), safe='')}/{int(size)}/{quote(filename)}"


def generate_strm(lib_id: str, output_dir: str, server_base: str, include_subtitles=False):
    lib = load_lib(lib_id)
    out_root = Path(output_dir).expanduser()
    count = 0
    skipped = 0
    examples = []
    for f in lib.get('files', []):
        rel = safe_rel_path(f['path'])
        ext = rel.suffix.lower()
        if ext not in VIDEO_EXTS:
            skipped += 1
            continue
        target = out_root / rel.with_suffix(rel.suffix + '.strm')
        # 如果原文件名是 .mkv，输出 movie.mkv.strm，便于保留原扩展信息
        target.parent.mkdir(parents=True, exist_ok=True)
        original_name = rel.name
        url = make_play_url(server_base, f['idx'], f['etag'], int(f.get('size') or 0), original_name)
        target.write_text(url + '\n', encoding='utf-8')
        count += 1
        if len(examples) < 10:
            examples.append(str(target))
    return {"count": count, "skipped": skipped, "output_dir": str(out_root), "examples": examples}


class SaveReq(BaseModel):
    name: str = ''
    content: Any

class GenReq(BaseModel):
    lib_id: str
    output_dir: str = ''
    server_base: str = ''
    include_subtitles: bool = False

class ConfigReq(BaseModel):
    output_dir: str = ''
    server_base: str = ''
    include_subtitles: bool = False
    pan_username: str = ''
    pan_password: str = ''

app = FastAPI(title='123 秒传 JSON -> STRM', docs_url=None, redoc_url=None)

HTML = r'''
<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>123秒传JSON转STRM</title>
<style>body{margin:0;background:#0f172a;color:#e5e7eb;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","Microsoft YaHei",sans-serif}.wrap{max-width:1200px;margin:0 auto;padding:24px}.title{font-size:28px;font-weight:800}.sub{color:#94a3b8;margin-top:6px}.grid{display:grid;grid-template-columns:420px 1fr;gap:18px;margin-top:20px}.card{background:#1f2937;border:1px solid #334155;border-radius:16px;padding:18px;box-shadow:0 12px 30px rgba(0,0,0,.25)}label{display:block;margin:12px 0 6px;color:#cbd5e1;font-size:13px}input,textarea,select{width:100%;box-sizing:border-box;background:#020617;color:#e5e7eb;border:1px solid #475569;border-radius:10px;padding:10px 12px}textarea{height:220px;font-family:Consolas,monospace;font-size:12px}.btn{border:0;border-radius:10px;padding:11px 14px;font-weight:700;cursor:pointer;margin-top:12px}.primary{background:#38bdf8;color:#001018}.ok{background:#22c55e;color:#001}.ghost{background:#475569;color:#fff}.danger{background:#ef4444;color:#fff}.row{display:flex;gap:10px}.row .btn{flex:1}.list{display:grid;gap:10px}.item{background:#0b1220;border:1px solid #334155;border-radius:12px;padding:12px}.muted{color:#94a3b8;font-size:12px}.log{white-space:pre-wrap;background:#020617;border:1px solid #334155;border-radius:12px;padding:12px;height:260px;overflow:auto;font-family:Consolas,monospace;font-size:12px}.pill{display:inline-block;padding:2px 8px;background:#0e7490;border-radius:99px;font-size:12px;margin-left:6px}@media(max-width:900px){.grid{grid-template-columns:1fr}}</style></head><body><div class="wrap"><div class="title">123 秒传 JSON → STRM</div><div class="sub">NAS Docker 一体版 · 持久化保存秒传库 · 直接生成 STRM · 播放时自动秒传并 302 到真实直链</div><div class="grid"><div class="card"><h3>1. 保存秒传 JSON</h3><label>库名称</label><input id="name" placeholder="例如：来自：BT磁力链下载"><label>JSON 内容</label><textarea id="content" placeholder='粘贴 {"files":[{"etag":"...","size":"...","path":"电影/xxx.mkv"}]}'></textarea><div class="row"><button class="btn ghost" onclick="pickFile()">选择JSON文件</button><button class="btn primary" onclick="saveLib()">保存到库</button></div><input id="file" type="file" accept=".json,application/json" style="display:none"><h3>2. 123账号设置</h3><label>123账号</label><input id="pan_username" placeholder="手机号/邮箱"><label>123密码</label><input id="pan_password" type="password" placeholder="123云盘密码"><h3>3. STRM 设置</h3><label>输出目录</label><input id="output_dir"><label>服务地址</label><input id="server_base"><button class="btn ok" onclick="saveCfg()">保存设置</button></div><div class="card"><h3>已保存的秒传库</h3><div id="libs" class="list"></div><h3>日志</h3><div id="log" class="log"></div></div></div></div><script>
function log(s){let el=document.getElementById('log');el.textContent+=s+'\n';el.scrollTop=el.scrollHeight}async function api(u,opt){let r=await fetch(u,opt);let j=await r.json();if(!r.ok||j.ok===false)throw new Error(j.error||j.detail||r.status);return j}async function loadCfg(){let c=await api('/api/config');output_dir.value=c.output_dir;server_base.value=c.server_base;pan_username.value=c.pan_username||'';pan_password.value=c.pan_password||''}async function saveCfg(){await api('/api/config',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({output_dir:output_dir.value,server_base:server_base.value,pan_username:pan_username.value,pan_password:pan_password.value})});log('设置已保存，123账号已同步到 settings.yaml')}function pickFile(){file.click()}file.onchange=async()=>{let f=file.files[0];if(!f)return;name.value=name.value||f.name.replace(/\.json$/,'');content.value=await f.text()};async function saveLib(){try{let raw=JSON.parse(content.value);let j=await api('/api/libraries',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:name.value,content:raw})});log('保存成功：'+j.id+'，文件数 '+j.files);content.value='';await loadLibs()}catch(e){alert(e.message)}}async function loadLibs(){let j=await api('/api/libraries');libs.innerHTML=j.items.map(x=>`<div class="item"><b>${x.name}</b><span class="pill">视频 ${x.video}</span><span class="pill">总 ${x.total}</span><div class="muted">ID: ${x.id}</div><div class="row"><button class="btn primary" onclick="gen('${x.id}')">生成STRM</button><button class="btn danger" onclick="delLib('${x.id}')">删除</button></div></div>`).join('')||'<div class="muted">暂无</div>'}async function gen(id){try{let j=await api('/api/generate',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({lib_id:id,output_dir:output_dir.value,server_base:server_base.value})});log('生成完成：'+j.count+' 个 STRM，跳过非视频 '+j.skipped+' 个\n输出：'+j.output_dir+'\n示例：\n'+j.examples.join('\n'))}catch(e){alert(e.message)}}async function delLib(id){if(!confirm('删除库 '+id+' ?'))return;await api('/api/libraries/'+encodeURIComponent(id),{method:'DELETE'});await loadLibs()}loadCfg();loadLibs();</script></body></html>
'''

@app.get('/', response_class=HTMLResponse)
def index():
    return HTML

@app.get('/api/config')
def get_config():
    c = config()
    return c

@app.post('/api/config')
def post_config(req: ConfigReq):
    c = config()
    if req.output_dir:
        c['output_dir'] = req.output_dir
    if req.server_base:
        c['server_base'] = req.server_base
    if req.pan_username:
        c['pan_username'] = req.pan_username
    if req.pan_password:
        c['pan_password'] = req.pan_password
    c['include_subtitles'] = req.include_subtitles
    save_config(c)
    update_settings_account(c.get('pan_username',''), c.get('pan_password',''))
    return {"ok": True}

@app.get('/api/libraries')
def api_libraries():
    return {"items": list_libraries()}

@app.post('/api/libraries')
def api_save_library(req: SaveReq):
    try:
        lib = normalize_library(req.content, req.name)
        p = lib_path(lib['id'])
        # 避免同名覆盖时不可控，加时间后缀
        if p.exists():
            lib['id'] = safe_name(f"{lib['id']}_{int(time.time())}")
            p = lib_path(lib['id'])
        p.write_text(json.dumps(lib, ensure_ascii=False, indent=2), encoding='utf-8')
        return {"ok": True, "id": lib['id'], "files": len(lib['files'])}
    except Exception as e:
        return JSONResponse({"ok": False, "error": str(e)}, status_code=400)

@app.delete('/api/libraries/{lib_id}')
def api_delete_library(lib_id: str):
    p = lib_path(lib_id)
    if p.exists():
        p.unlink()
    return {"ok": True}

@app.post('/api/generate')
def api_generate(req: GenReq):
    c = config()
    out = req.output_dir or c.get('output_dir') or DEFAULT_OUTPUT_DIR
    base = req.server_base or c.get('server_base') or os.getenv("SERVER_BASE", f"http://127.0.0.1:{settings.get('WEBDAV_PORT',8000)}")
    c['output_dir'] = out
    c['server_base'] = base
    save_config(c)
    try:
        return generate_strm(req.lib_id, out, base, req.include_subtitles)
    except Exception as e:
        return JSONResponse({"ok": False, "error": str(e)}, status_code=400)

@app.get('/play/{file_id}/{etag}/{size}/{filename:path}')
def play_direct(file_id: int, etag: str, size: int, filename: str):
    # file_id 在新格式中主要作为占位/日志标识；真正秒传依赖 etag + size + filename。
    url = get_file_url_with_etag_candidates(name=filename, etag=etag, size=int(size))
    if not url:
        raise HTTPException(500, 'failed to get url')
    return RedirectResponse(url=url, status_code=302)


@app.get('/play/{lib_id}/{idx}')
def play_legacy(lib_id: str, idx: int):
    # 兼容旧版 STRM：/play/{lib_id}/{idx}
    lib = load_lib(lib_id)
    files = lib.get('files', [])
    if idx < 0 or idx >= len(files):
        raise HTTPException(404, 'file not found')
    f = files[idx]
    name = Path(f['path']).name
    url = get_file_url_with_etag_candidates(name=name, etag=f['etag'], size=int(f.get('size') or 0))
    if not url:
        raise HTTPException(500, 'failed to get url')
    return RedirectResponse(url=url, status_code=302)

if __name__ == '__main__':
    port = int(os.getenv("PORT", settings.get('WEBDAV_PORT', 8000)))
    host = os.getenv("HOST", settings.get('WEBDAV_HOST', '0.0.0.0'))
    print(f"STRM工具启动：http://127.0.0.1:{port}/")
    uvicorn.run(app, host=host, port=port, log_level='info')
