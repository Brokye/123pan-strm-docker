import base64
import json
import os
import re
import time
from pathlib import Path
from typing import Any, Dict, List
from urllib.parse import quote

import requests
import uvicorn
import yaml
from fastapi import FastAPI, File, HTTPException, UploadFile
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
# cache.json 持久化路径
CACHE_PATH = Path(os.getenv("CACHE_PATH", str(DATA_DIR / "cache.json"))).expanduser()


def ensure_cache_file():
    """确保 /data/cache.json 一定存在。NAS 空目录挂载时也会自动创建。"""
    CACHE_PATH.parent.mkdir(parents=True, exist_ok=True)
    if not CACHE_PATH.exists():
        CACHE_PATH.write_text(
            json.dumps({"accessToken": "", "tokenCreateTime": "", "lastDeleteTime": "", "accountHash": ""}, ensure_ascii=False, indent=2),
            encoding="utf-8"
        )
        print("已自动创建 token 缓存文件:", CACHE_PATH)


VIDEO_EXTS = {'.mp4','.mkv','.ts','.m2ts','.avi','.mov','.wmv','.flv','.rmvb','.webm','.mpg','.mpeg','.iso'}
SUBTITLE_EXTS = {'.srt','.ass','.ssa','.vtt','.sub','.sup'}
BAD_CHARS = '<>:"/\\|?*'

CATEGORY_DIRS = {
    "电影": "电影",
    "剧集": "剧集",
    "动漫": "动漫",
    "纪录片": "纪录片",
    "综艺": "综艺",
}


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
            c.setdefault("mode", "cache")
            c.setdefault("pan_username", settings.get("123PAN_USERNAME", ""))
            c.setdefault("pan_password", settings.get("123PAN_PASSWORD", ""))
            return c
        except Exception:
            pass
    return {
        "output_dir": DEFAULT_OUTPUT_DIR,
        "server_base": os.getenv("SERVER_BASE", f"http://127.0.0.1:{settings.get('WEBDAV_PORT',8000)}"),
        "include_subtitles": False,
        "mode": "cache",
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


def normalize_library(raw: Any, name: str = '', category: str = '') -> Dict[str, Any]:
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
    return {"id": lib_id, "name": (name or meta.get('commonPath') or lib_id).strip('/\\'), "commonPath": common, "createdAt": int(time.time()), "meta": meta, "files": out_files, "category": category}


def list_libraries():
    rows = []
    for p in sorted(LIB_DIR.glob('*.json'), key=lambda x: x.stat().st_mtime, reverse=True):
        try:
            d = json.loads(p.read_text(encoding='utf-8'))
            total = len(d.get('files', []))
            video = sum(1 for f in d.get('files', []) if Path(f.get('path','')).suffix.lower() in VIDEO_EXTS)
            rows.append({"id": d.get('id') or p.stem, "name": d.get('name') or p.stem, "total": total, "video": video, "createdAt": d.get('createdAt'), "category": d.get('category', '')})
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


def get_file_url_with_etag_candidates(name: str, etag: str, size: int, fast_mode: bool = False) -> str:
    candidates = base62_to_hex_candidates(etag)
    last_url = None
    for e in candidates:
        print(f"尝试 ETag: {etag} -> {e}")
        url = get_file_url(name=name, etag=e, size=int(size), fast_mode=fast_mode)
        last_url = url
        # get_file_url 失败时会返回一个固定兜底 mp4，识别并继续尝试下一个候选
        if url and "222.186.21.40:33333/NGGYU.mp4" not in url:
            return url
    return last_url


def make_play_url(base: str, file_id: int, etag: str, size: int, filename: str) -> str:
    return base.rstrip('/') + f"/play/{file_id}/{quote(str(etag), safe='')}/{int(size)}/{quote(filename)}"


def download_subtitle_file(file_info: Dict, target_path: Path, fast_mode: bool = False) -> bool:
    """通过秒传获取字幕文件直链并下载到本地。"""
    name = Path(file_info['path']).name
    url = get_file_url_with_etag_candidates(
        name=name,
        etag=file_info['etag'],
        size=int(file_info.get('size') or 0),
        fast_mode=fast_mode,
    )
    if not url or "222.186.21.40:33333/NGGYU.mp4" in url:
        print(f"字幕下载失败(获取URL失败): {name}")
        return False
    try:
        headers = {"Referer": "https://yun.123pan.com/"}
        resp = requests.get(url, headers=headers, timeout=30)
        if resp.status_code == 200:
            target_path.parent.mkdir(parents=True, exist_ok=True)
            target_path.write_bytes(resp.content)
            print(f"字幕下载成功: {target_path}")
            return True
    except Exception as e:
        print(f"字幕下载异常: {name}, {e}")
    return False


def generate_strm(lib_id: str, output_dir: str, server_base: str, include_subtitles=False):
    lib = load_lib(lib_id)
    category = lib.get('category', '')
    cfg = config()
    fast_mode = (cfg.get('mode', 'cache') == 'fast')

    # 根据分类拼接子目录
    out_root = Path(output_dir).expanduser()
    if category in CATEGORY_DIRS:
        out_root = out_root / CATEGORY_DIRS[category]

    count = 0
    subtitles = 0
    skipped = 0
    examples = []
    for f in lib.get('files', []):
        rel = safe_rel_path(f['path'])
        ext = rel.suffix.lower()

        if ext in VIDEO_EXTS:
            # 视频：生成 STRM
            target = out_root / rel.with_suffix(rel.suffix + '.strm')
            target.parent.mkdir(parents=True, exist_ok=True)
            original_name = rel.name
            url = make_play_url(server_base, f['idx'], f['etag'], int(f.get('size') or 0), original_name)
            target.write_text(url + '\n', encoding='utf-8')
            count += 1
            if len(examples) < 10:
                examples.append(str(target))

        elif include_subtitles and ext in SUBTITLE_EXTS:
            # 字幕：直接下载实际文件
            target = out_root / rel
            if download_subtitle_file(f, target, fast_mode):
                subtitles += 1
                if len(examples) < 10:
                    examples.append(str(target))
            else:
                skipped += 1
        else:
            skipped += 1

    return {"count": count, "subtitles": subtitles, "skipped": skipped, "output_dir": str(out_root), "examples": examples}


class SaveReq(BaseModel):
    name: str = ''
    content: Any
    category: str = ''

class ModeReq(BaseModel):
    mode: str = 'cache'

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



HTML = r'''<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>123秒传JSON转STRM</title>
<style>body{margin:0;background:#0f172a;color:#e5e7eb;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI","Microsoft YaHei",sans-serif}.wrap{max-width:1200px;margin:0 auto;padding:24px}.title{font-size:28px;font-weight:800}.sub{color:#94a3b8;margin-top:6px}.modebar{display:flex;align-items:center;gap:14px;background:#1e293b;border:2px solid #334155;border-radius:16px;padding:16px 20px;margin-top:18px;transition:all .4s}.modebar.fast{background:#2d1518;border-color:#dc2626;box-shadow:0 0 20px rgba(220,38,38,.15)}.modebar .mode-icon{font-size:28px;flex-shrink:0;transition:all .4s}.modebar .mode-text{flex:1}.modebar .mode-title{font-size:16px;font-weight:700;color:#e5e7eb;transition:color .4s}.modebar.fast .mode-title{color:#fca5a5}.modebar .mode-desc{font-size:12px;color:#94a3b8;margin-top:4px;transition:color .4s}.modebar.fast .mode-desc{color:#f87171}.modebar .mode-badge{display:inline-block;padding:3px 10px;border-radius:99px;font-size:11px;font-weight:700;background:#0e7490;color:#fff;transition:all .4s}.modebar.fast .mode-badge{background:#dc2626;animation:pulse 2s infinite}@keyframes pulse{0%,100%{opacity:1}50%{opacity:.6}}.grid{display:grid;grid-template-columns:420px 1fr;gap:18px;margin-top:18px}.card{background:#1f2937;border:1px solid #334155;border-radius:16px;padding:18px;box-shadow:0 12px 30px rgba(0,0,0,.25)}label{display:block;margin:12px 0 6px;color:#cbd5e1;font-size:13px}input,textarea,select{width:100%;box-sizing:border-box;background:#020617;color:#e5e7eb;border:1px solid #475569;border-radius:10px;padding:10px 12px}textarea{height:220px;font-family:Consolas,monospace;font-size:12px}.btn{border:0;border-radius:10px;padding:11px 14px;font-weight:700;cursor:pointer;margin-top:12px}.primary{background:#38bdf8;color:#001018}.ok{background:#22c55e;color:#001}.ghost{background:#475569;color:#fff}.danger{background:#ef4444;color:#fff}.warn{background:#f59e0b;color:#001}.row{display:flex;gap:10px}.row .btn{flex:1}.list{display:grid;gap:10px}.item{background:#0b1220;border:1px solid #334155;border-radius:12px;padding:12px}.muted{color:#94a3b8;font-size:12px}.log{white-space:pre-wrap;background:#020617;border:1px solid #334155;border-radius:12px;padding:12px;height:260px;overflow:auto;font-family:Consolas,monospace;font-size:12px}.pill{display:inline-block;padding:2px 8px;background:#0e7490;border-radius:99px;font-size:12px;margin-left:6px}.pill-fast{background:#dc2626}.cat-head{display:flex;align-items:center;gap:10px;padding:10px 14px;background:#1e293b;border:1px solid #334155;border-radius:10px;margin-top:10px;cursor:pointer;user-select:none}.cat-head:hover{background:#273449}.cat-arrow{font-size:12px;transition:transform .2s;color:#94a3b8}.cat-arrow.open{transform:rotate(90deg)}.cat-count{color:#94a3b8;font-size:12px;margin-left:auto}.toggle{display:flex;align-items:center;gap:8px;cursor:pointer;flex-shrink:0}.toggle input{display:none}.toggle .track{width:44px;height:24px;background:#475569;border-radius:99px;position:relative;transition:background .3s;flex-shrink:0}.toggle input:checked+.track{background:#dc2626}.toggle .track::after{content:'';position:absolute;top:3px;left:3px;width:18px;height:18px;background:#fff;border-radius:50%;transition:transform .3s}.toggle input:checked+.track::after{transform:translateX(20px)}@media(max-width:900px){.grid{grid-template-columns:1fr}}</style></head><body><div class="wrap"><div class="title">123 秒传 JSON → STRM</div><div class="sub">NAS Docker 一体版 · 持久化保存秒传库 · 直接生成 STRM · 播放时自动秒传并 302 到真实直链</div><div class="modebar" id="modebar"><span class="mode-icon" id="modeIcon">🛡️</span><div class="mode-text"><div class="mode-title" id="modeTitle">缓存模式</div><div class="mode-desc" id="modeDesc">文件保存 24 小时后自动清理，适合反复播放</div></div><span class="mode-badge" id="modeBadge">安全</span><label class="toggle"><input id="fastMode" type="checkbox"><span class="track"></span></label></div><div class="grid"><div class="card"><h3>1. 保存秒传 JSON</h3><label>库名称</label><input id="name" placeholder="例如：来自：BT磁力链下载"><label>分类(可选)</label><select id="category"><option value="">未分类</option><option value="电影">电影</option><option value="剧集">剧集</option><option value="动漫">动漫</option><option value="纪录片">纪录片</option><option value="综艺">综艺</option></select><label>JSON 内容</label><textarea id="content" placeholder='粘贴 {"files":[{"etag":"...","size":"...","path":"电影/xxx.mkv"}]}'></textarea><div class="row"><button class="btn ghost" onclick="document.getElementById('file').click()">选择JSON文件</button><button class="btn primary" onclick="saveLib()">保存到库</button></div><input id="file" type="file" accept=".json,application/json" style="position:absolute;left:-9999px"><h3>2. 基础设置</h3><label>123账号</label><input id="pan_username" placeholder="手机号/邮箱"><label>123密码</label><input id="pan_password" type="password" placeholder="123云盘密码"><label>STRM 输出目录</label><input id="output_dir"><label>服务地址</label><input id="server_base"><button class="btn ok" onclick="saveCfg()">保存基础设置</button></div><div class="card"><h3>已保存的秒传库</h3><div class="row"><button class="btn ghost" onclick="exportBackup()">导出备份</button><button class="btn warn" onclick="document.getElementById('restoreFile').click()">导入恢复</button></div><input id="restoreFile" type="file" accept=".json" style="position:absolute;left:-9999px"><div id="libs" class="list"></div><h3>日志</h3><div id="log" class="log"></div></div></div></div><script>
function log(s){var el=document.getElementById('log');el.textContent+=s+'\n';el.scrollTop=el.scrollHeight}async function api(u,opt){var r=await fetch(u,opt);var j=await r.json();if(!r.ok||j.ok===false)throw new Error(j.error||j.detail||r.status);return j}
function _applyModeUI(fast){var bar=document.getElementById('modebar');var icon=document.getElementById('modeIcon');var title=document.getElementById('modeTitle');var desc=document.getElementById('modeDesc');var badge=document.getElementById('modeBadge');if(fast){bar.classList.add('fast');icon.textContent='⚠️';title.textContent='入库模式';desc.textContent='获取直链后 1 分钟即删除，播放完即清理';badge.textContent='即时清理';badge.style.background='#dc2626'}else{bar.classList.remove('fast');icon.textContent='🛡️';title.textContent='缓存模式';desc.textContent='文件保存 24 小时后自动清理，适合反复播放';badge.textContent='安全';badge.style.background='#0e7490'}}
async function loadMode(){var m=await api('/api/mode');var fast=(m.mode==='fast');document.getElementById('fastMode').checked=fast;_applyModeUI(fast)}
document.getElementById('fastMode').onchange=async function(){var f=this.checked;_applyModeUI(f);var j=await api('/api/mode',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({mode:f?'fast':'cache'})});log('已切换为：'+j.label)}
async function loadCfg(){var c=await api('/api/config');document.getElementById('output_dir').value=c.output_dir;document.getElementById('server_base').value=c.server_base;document.getElementById('pan_username').value=c.pan_username||'';document.getElementById('pan_password').value=c.pan_password||''}
async function saveCfg(){var o=document.getElementById('output_dir').value;var s=document.getElementById('server_base').value;var u=document.getElementById('pan_username').value;var p=document.getElementById('pan_password').value;await api('/api/config',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({output_dir:o,server_base:s,pan_username:u,pan_password:p})});log('√ 设置已保存')}
document.getElementById('file').onchange=async function(){var f=this.files[0];if(!f)return;document.getElementById('name').value=document.getElementById('name').value||f.name.replace(/\.json$/,'');document.getElementById('content').value=await f.text()}
async function saveLib(){try{var raw=JSON.parse(document.getElementById('content').value);var cat=document.getElementById('category').value;var j=await api('/api/libraries',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({name:document.getElementById('name').value,content:raw,category:cat})});log('√ 保存成功：'+j.id+'，文件数 '+j.files);document.getElementById('content').value='';await loadLibs()}catch(e){alert(e.message)}}
function _itemHtml(x){return'<div class="item"><b>'+x.name+'</b> <span class="pill">视频 '+x.video+'</span> <span class="pill">总 '+x.total+'</span><div class="muted">ID: '+x.id+'</div><div class="row"><button class="btn primary" onclick="gen(\''+x.id+'\')">生成STRM</button><button class="btn danger" onclick="delLib(\''+x.id+'\')">删除</button></div></div>'}var _catColors={电影:'#a855f7',剧集:'#3b82f6',动漫:'#ec4899',纪录片:'#22c55e',综艺:'#f59e0b','':'#6b7280'};var _catOrder=['电影','剧集','动漫','纪录片','综艺',''];var _catNames={'电影':'电影','剧集':'剧集','动漫':'动漫','纪录片':'纪录片','综艺':'综艺','':'未分类'};async function loadLibs(){var j=await api('/api/libraries');var el=document.getElementById('libs');var items=j.items||[];var groups={};_catOrder.forEach(function(c){groups[c]=[]});items.forEach(function(x){var cat=x.category||'';if(!groups[cat])groups[cat]=[];groups[cat].push(x)});var h='';_catOrder.forEach(function(cat){var g=groups[cat];if(!g||g.length===0)return;h+='<div class="cat-head" onclick="var a=this.querySelector(\'.cat-arrow\');var b=this.nextElementSibling;a.classList.toggle(\'open\');b.style.display=b.style.display===\'none\'?\'block\':\'none\'"><span class="cat-arrow open">▶</span><span class="pill" style="background:'+(_catColors[cat]||'#6b7280')+'">'+(_catNames[cat]||cat||'未分类')+'</span><span class="cat-count">('+g.length+')</span></div><div class="cat-body" style="display:block">'+g.map(_itemHtml).join('')+'</div>'});el.innerHTML=h||'<div class="muted">暂无</div>'}
async function gen(id){try{var j=await api('/api/generate',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({lib_id:id,output_dir:document.getElementById('output_dir').value,server_base:document.getElementById('server_base').value,include_subtitles:true})});log('√ 生成完成：'+j.count+' 个 STRM'+(j.subtitles?', '+j.subtitles+' 个字幕':'')+'，跳过 '+j.skipped+' 个\n输出：'+j.output_dir+'\n示例：\n'+j.examples.join('\n'))}catch(e){alert(e.message)}}
async function delLib(id){if(!confirm('删除库 '+id+' ?'))return;await api('/api/libraries/'+encodeURIComponent(id),{method:'DELETE'});await loadLibs()}
async function exportBackup(){var resp=await fetch('/api/backup');var blob=await resp.blob();var a=document.createElement('a');a.href=URL.createObjectURL(blob);a.download='123pan-strm-backup-'+Date.now()+'.json';a.click();log('√ 备份已下载')}
document.getElementById('restoreFile').onchange=async function(){var f=this.files[0];if(!f)return;var formData=new FormData();formData.append('file',f);var resp=await fetch('/api/restore/upload',{method:'POST',body:formData});var j=await resp.json();if(!j.ok){alert(j.error);return}log('√ 恢复完成：成功 '+j.restored+' 个，跳过 '+j.skipped+' 个');await loadLibs()}
loadMode();loadCfg();loadLibs();
</script></body></html>'''

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
    # 只在用户保存基础设置后检查/创建 token 缓存文件
    ensure_cache_file()
    return {"ok": True}

@app.get('/api/libraries')
def api_libraries():
    return {"items": list_libraries()}

@app.post('/api/libraries')
def api_save_library(req: SaveReq):
    try:
        lib = normalize_library(req.content, req.name, req.category)
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
    cfg = config()
    fast_mode = (cfg.get('mode', 'cache') == 'fast')
    url = get_file_url_with_etag_candidates(name=filename, etag=etag, size=int(size), fast_mode=fast_mode)
    if not url:
        raise HTTPException(500, 'failed to get url')
    return RedirectResponse(url=url, status_code=302)


@app.get('/play/{lib_id}/{idx}')
def play_legacy(lib_id: str, idx: int):
    lib = load_lib(lib_id)
    files = lib.get('files', [])
    if idx < 0 or idx >= len(files):
        raise HTTPException(404, 'file not found')
    f = files[idx]
    name = Path(f['path']).name
    cfg = config()
    fast_mode = (cfg.get('mode', 'cache') == 'fast')
    url = get_file_url_with_etag_candidates(name=name, etag=f['etag'], size=int(f.get('size') or 0), fast_mode=fast_mode)
    if not url:
        raise HTTPException(500, 'failed to get url')
    return RedirectResponse(url=url, status_code=302)


@app.get('/api/mode')
def api_get_mode():
    cfg = config()
    return {"mode": cfg.get('mode', 'cache')}


@app.post('/api/mode')
def api_set_mode(req: ModeReq):
    new_mode = req.mode
    if new_mode not in ('cache', 'fast'):
        return JSONResponse({"ok": False, "error": "mode must be cache or fast"}, status_code=400)
    cfg = config()
    cfg['mode'] = new_mode
    save_config(cfg)
    label = "入库模式(1分钟清理)" if new_mode == 'fast' else "缓存模式(24小时清理)"
    return {"ok": True, "mode": new_mode, "label": label}


@app.get('/api/backup')
def api_backup():
    """导出所有秒传库为备份 JSON 文件（不含账号信息）。"""
    libraries = []
    for p in sorted(LIB_DIR.glob('*.json')):
        try:
            d = json.loads(p.read_text(encoding='utf-8'))
            libraries.append(d)
        except Exception:
            pass
    backup_data = {
        "version": 1,
        "exportedAt": int(time.time()),
        "app": "123pan-strm-docker",
        "libraries": libraries,
    }
    return JSONResponse(
        content=backup_data,
        headers={"Content-Disposition": f'attachment; filename="123pan-strm-backup-{int(time.time())}.json"'}
    )


@app.post('/api/restore/upload')
async def api_restore_upload(file: UploadFile = File(...)):
    """上传备份 JSON 文件恢复秒传库。跳过已存在的同名库。"""
    try:
        content = await file.read()
        data = json.loads(content)
    except Exception as e:
        return JSONResponse({"ok": False, "error": f"备份文件解析失败: {e}"}, status_code=400)
    if data.get('app') != '123pan-strm-docker':
        return JSONResponse({"ok": False, "error": "不支持的备份文件格式"}, status_code=400)
    libraries = data.get('libraries', [])
    restored = 0
    skipped = 0
    for lib in libraries:
        lib.setdefault('category', '')
        lib.setdefault('mode', 'cache')
        lib_id = lib.get('id') or lib.get('name', '')
        if not lib_id:
            continue
        p = lib_path(lib_id)
        if p.exists():
            skipped += 1
            continue
        p.write_text(json.dumps(lib, ensure_ascii=False, indent=2), encoding='utf-8')
        restored += 1
    return {"ok": True, "restored": restored, "skipped": skipped}


if __name__ == '__main__':
    port = int(os.getenv("PORT", settings.get('WEBDAV_PORT', 8000)))
    host = os.getenv("HOST", settings.get('WEBDAV_HOST', '0.0.0.0'))
    print(f"STRM工具启动：http://127.0.0.1:{port}/")
    uvicorn.run(
        app,
        host=host,
        port=port,
        log_level='warning',
        access_log=False,
        loop='asyncio',
        reload=False,
    )
