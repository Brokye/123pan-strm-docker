import base64, json, os, re, time
from pathlib import Path
from typing import Any, Dict, List
from urllib.parse import quote

import requests, uvicorn, yaml
from fastapi import FastAPI, File, HTTPException, UploadFile
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import FileResponse, HTMLResponse, JSONResponse, RedirectResponse
from pydantic import BaseModel
from get_file_url import get_file_url

BASE_DIR = Path(__file__).resolve().parent
DEFAULT_DATA_DIR = Path("/data") if Path("/data").exists() else BASE_DIR / "strm_data"
DATA_DIR = Path(os.getenv("DATA_DIR", str(DEFAULT_DATA_DIR))).expanduser()
LIB_DIR = DATA_DIR / "libraries"
CONFIG_FILE = DATA_DIR / "config.json"
SETTINGS_PATH = Path(os.getenv("SETTINGS_PATH", str(DATA_DIR / "settings.yaml"))).expanduser()
DEFAULT_OUTPUT_DIR = os.getenv("STRM_OUTPUT_DIR", "/strm" if Path("/strm").exists() else str(BASE_DIR / "STRM输出"))
INDEX_HTML = BASE_DIR / "index.html"
LIB_DIR.mkdir(parents=True, exist_ok=True)
DATA_DIR.mkdir(parents=True, exist_ok=True)
if not SETTINGS_PATH.exists():
    try: SETTINGS_PATH.write_text((BASE_DIR / "settings.yaml").read_text(encoding="utf-8"), encoding="utf-8")
    except: pass
CACHE_PATH = Path(os.getenv("CACHE_PATH", str(DATA_DIR / "cache.json"))).expanduser()

def ensure_cache_file():
    CACHE_PATH.parent.mkdir(parents=True, exist_ok=True)
    if not CACHE_PATH.exists():
        CACHE_PATH.write_text(json.dumps({"accessToken":"","tokenCreateTime":"","lastDeleteTime":"","accountHash":""},ensure_ascii=False,indent=2),encoding="utf-8")

VIDEO_EXTS = {'.mp4','.mkv','.ts','.m2ts','.avi','.mov','.wmv','.flv','.rmvb','.webm','.mpg','.mpeg','.iso'}
SUBTITLE_EXTS = {'.srt','.ass','.ssa','.vtt','.sub','.sup'}
BAD_CHARS = '<>:"/\\|?*'
CATEGORY_DIRS = {"电影":"电影","剧集":"剧集","动漫":"动漫","纪录片":"纪录片","综艺":"综艺"}

def load_settings_yaml():
    with open(SETTINGS_PATH,"r",encoding="utf-8") as f: return yaml.safe_load(f.read()) or {}
settings = load_settings_yaml()

def safe_name(s:str)->str:
    s=str(s or '').replace('\x00','')
    for c in BAD_CHARS: s=s.replace(c,' ')
    s=re.sub(r'\s+',' ',s).strip().rstrip('.')
    return s or 'unnamed'

def safe_rel_path(p:str)->Path:
    parts=[]
    for x in str(p or '').replace('\\','/').split('/'):
        x=x.strip()
        if not x or x in {'.','..'}: continue
        parts.append(safe_name(x))
    return Path(*parts) if parts else Path('unnamed')

def config()->Dict[str,Any]:
    if CONFIG_FILE.exists():
        try:
            c=json.loads(CONFIG_FILE.read_text(encoding='utf-8'))
            c.setdefault("output_dir",DEFAULT_OUTPUT_DIR)
            c.setdefault("server_base",os.getenv("SERVER_BASE",f"http://127.0.0.1:{settings.get('WEBDAV_PORT',8000)}"))
            c.setdefault("include_subtitles",False)
            c.setdefault("mode","cache")
            c.setdefault("pan_username",settings.get("123PAN_USERNAME",""))
            c.setdefault("pan_password",settings.get("123PAN_PASSWORD",""))
            return c
        except: pass
    return {"output_dir":DEFAULT_OUTPUT_DIR,"server_base":os.getenv("SERVER_BASE",f"http://127.0.0.1:{settings.get('WEBDAV_PORT',8000)}"),"include_subtitles":False,"mode":"cache","pan_username":settings.get("123PAN_USERNAME",""),"pan_password":settings.get("123PAN_PASSWORD","")}

def save_config(c:Dict[str,Any]): CONFIG_FILE.write_text(json.dumps(c,ensure_ascii=False,indent=2),encoding='utf-8')

def update_settings_account(username:str,password:str):
    path=SETTINGS_PATH; data=load_settings_yaml()
    ou,op=data.get("123PAN_USERNAME",""),data.get("123PAN_PASSWORD","")
    nu,np=username or "",password or ""
    data["123PAN_USERNAME"]=nu; data["123PAN_PASSWORD"]=np
    with open(path,"w",encoding="utf-8") as f: yaml.safe_dump(data,f,allow_unicode=True,sort_keys=False)
    if ou!=nu or op!=np:
        cp=Path(os.getenv("CACHE_PATH",str(DATA_DIR/"cache.json")))
        if cp.exists():
            try: cp.unlink()
            except: pass
    global settings; settings=data

def lib_path(lib_id:str)->Path: return LIB_DIR/f"{safe_name(lib_id)}.json"
def load_lib(lib_id:str)->Dict[str,Any]:
    p=lib_path(lib_id)
    if not p.exists(): raise HTTPException(404,'library not found')
    return json.loads(p.read_text(encoding='utf-8'))

def normalize_library(raw:Any,name:str='',category:str='')->Dict[str,Any]:
    if isinstance(raw,dict) and isinstance(raw.get('files'),list): files,common,meta=raw['files'],raw.get('commonPath',''),{k:v for k,v in raw.items() if k!='files'}
    elif isinstance(raw,list): files,common,meta=raw,'',{}
    else: raise ValueError('unsupported format')
    out=[]
    for i,item in enumerate(files):
        if not isinstance(item,dict): continue
        path=item.get('path') or item.get('Path') or item.get('name') or item.get('FileName') or item.get('filename')
        etag=item.get('etag') or item.get('Etag') or item.get('ETag') or item.get('md5') or item.get('hash')
        size=item.get('size') or item.get('Size') or 0
        if not path or not etag: continue
        try: si=int(size)
        except: si=0
        out.append({"idx":len(out),"path":str(path).replace('\\','/'),"etag":str(etag),"size":si})
    lid=safe_name((name or meta.get('commonPath') or meta.get('name') or f"library_{int(time.time())}").strip('/\\'))
    return {"id":lid,"name":(name or meta.get('commonPath') or lid).strip('/\\'),"commonPath":common,"createdAt":int(time.time()),"meta":meta,"files":out,"category":category}

def list_libraries():
    rows=[]
    for p in sorted(LIB_DIR.glob('*.json'),key=lambda x:x.stat().st_mtime,reverse=True):
        try:
            d=json.loads(p.read_text(encoding='utf-8'))
            total=len(d.get('files',[]))
            video=sum(1 for f in d.get('files',[]) if Path(f.get('path','')).suffix.lower() in VIDEO_EXTS)
            rows.append({"id":d.get('id') or p.stem,"name":d.get('name') or p.stem,"total":total,"video":video,"createdAt":d.get('createdAt'),"category":d.get('category','')})
        except Exception as e:
            print(f"跳过无效库文件 {p.name}: {e}")
    return rows

def is_hex_md5(etag:str)->bool: return bool(re.fullmatch(r"[0-9a-fA-F]{32}",str(etag or "")))

def base62_to_hex_candidates(etag:str)->List[str]:
    etag=str(etag or "").strip()
    if not etag: return []
    if is_hex_md5(etag): return [etag.lower()]
    alphabets=["0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ","0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz","ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789","abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"]
    out=[]
    for ab in alphabets:
        try:
            n=0
            for ch in etag: n=n*62+ab.index(ch)
            h=f"{n:032x}"
            if len(h)<=32: h=h[-32:].zfill(32)
            if h not in out: out.append(h)
        except: continue
    if etag not in out: out.append(etag)
    return out

def get_file_url_with_etag_candidates(name:str,etag:str,size:int,fast_mode:bool=False)->str:
    candidates=base62_to_hex_candidates(etag); last_url=None
    for e in candidates:
        url=get_file_url(name=name,etag=e,size=int(size),fast_mode=fast_mode); last_url=url
        if url and "222.186.21.40:33333/NGGYU.mp4" not in url: return url
    return last_url

def make_play_url(base:str,file_id:int,etag:str,size:int,filename:str)->str:
    return base.rstrip('/')+f"/play/{file_id}/{quote(str(etag),safe='')}/{int(size)}/{quote(filename)}"

def download_subtitle_file(file_info:Dict,target_path:Path,fast_mode:bool=False)->bool:
    name=Path(file_info['path']).name
    url=get_file_url_with_etag_candidates(name=name,etag=file_info['etag'],size=int(file_info.get('size') or 0),fast_mode=fast_mode)
    if not url or "222.186.21.40:33333/NGGYU.mp4" in url: return False
    try:
        resp=requests.get(url,headers={"Referer":"https://yun.123pan.com/"},timeout=30)
        if resp.status_code==200: target_path.parent.mkdir(parents=True,exist_ok=True); target_path.write_bytes(resp.content); return True
    except: pass
    return False

def generate_strm(lib_id:str,output_dir:str,server_base:str,include_subtitles=False):
    lib=load_lib(lib_id); category=lib.get('category',''); cfg=config(); fast_mode=(cfg.get('mode','cache')=='fast')
    out_root=Path(output_dir).expanduser()
    if category in CATEGORY_DIRS: out_root=out_root/CATEGORY_DIRS[category]
    count=subtitles=skipped=0; examples=[]
    for f in lib.get('files',[]):
        rel=safe_rel_path(f['path']); ext=rel.suffix.lower()
        if ext in VIDEO_EXTS:
            target=out_root/rel.with_suffix('.strm'); target.parent.mkdir(parents=True,exist_ok=True)
            url=make_play_url(server_base,f['idx'],f['etag'],int(f.get('size') or 0),rel.name)
            target.write_text(url+'\n',encoding='utf-8'); count+=1
            if len(examples)<10: examples.append(str(target))
        elif include_subtitles and ext in SUBTITLE_EXTS:
            target=out_root/rel
            if download_subtitle_file(f,target,fast_mode): subtitles+=1
            else: skipped+=1
            if len(examples)<10: examples.append(str(target))
        else: skipped+=1
    return {"count":count,"subtitles":subtitles,"skipped":skipped,"output_dir":str(out_root),"examples":examples}

class SaveReq(BaseModel): name:str=''; content:Any; category:str=''
class ModeReq(BaseModel): mode:str='cache'
class GenReq(BaseModel): lib_id:str; output_dir:str=''; server_base:str=''; include_subtitles:bool=False
class ConfigReq(BaseModel): output_dir:str=''; server_base:str=''; include_subtitles:bool=False; pan_username:str=''; pan_password:str=''
class UpdateLibReq(BaseModel): name:str=''; category:str=''; files:Any=None

app=FastAPI(title='123 sec-chuan -> STRM',docs_url=None,redoc_url=None)
app.add_middleware(CORSMiddleware,allow_origins=["*"],allow_methods=["*"],allow_headers=["*"])

@app.get('/',response_class=HTMLResponse)
def index():
    if INDEX_HTML.exists(): return FileResponse(str(INDEX_HTML))
    return HTMLResponse("<h1>123 STRM API</h1><p>Place index.html in project dir or <a href='/api/libraries'>view API</a></p>")

@app.get('/api/config')
def get_config(): return config()
@app.post('/api/config')
def post_config(req:ConfigReq):
    c=config()
    if req.output_dir: c['output_dir']=req.output_dir
    if req.server_base: c['server_base']=req.server_base
    if req.pan_username: c['pan_username']=req.pan_username
    if req.pan_password: c['pan_password']=req.pan_password
    c['include_subtitles']=req.include_subtitles
    save_config(c); update_settings_account(c.get('pan_username',''),c.get('pan_password','')); ensure_cache_file()
    return {"ok":True}

@app.get('/api/libraries')
def api_libraries(): return {"items":list_libraries()}

@app.get('/api/libraries/{lib_id}')
def api_get_library(lib_id:str): return load_lib(lib_id)

@app.post('/api/libraries')
def api_save_library(req:SaveReq):
    try:
        lib=normalize_library(req.content,req.name,req.category); p=lib_path(lib['id'])
        if p.exists(): lib['id']=safe_name(f"{lib['id']}_{int(time.time())}"); p=lib_path(lib['id'])
        p.write_text(json.dumps(lib,ensure_ascii=False,indent=2),encoding='utf-8')
        return {"ok":True,"id":lib['id'],"files":len(lib['files'])}
    except Exception as e: return JSONResponse({"ok":False,"error":str(e)},status_code=400)

@app.put('/api/libraries/{lib_id}')
def api_update_library(lib_id:str,req:UpdateLibReq):
    lib=load_lib(lib_id); old_path=lib_path(lib_id)
    if req.name:
        new_id=safe_name(req.name.strip('/\\')); lib['name']=req.name.strip('/\\')
        if new_id!=lib_id:
            lib['id']=new_id; new_path=lib_path(new_id)
            if new_path.exists() and str(new_path)!=str(old_path): lib['id']=safe_name(f"{new_id}_{int(time.time())}"); new_path=lib_path(lib['id'])
            old_path.rename(new_path); old_path=new_path
    if req.category: lib['category']=req.category
    if req.files is not None:
        for i,f in enumerate(req.files): f['idx']=i
        lib['files']=req.files
    old_path.write_text(json.dumps(lib,ensure_ascii=False,indent=2),encoding='utf-8')
    return {"ok":True,"id":lib['id'],"files":len(lib['files'])}

@app.delete('/api/libraries/{lib_id}')
def api_delete_library(lib_id:str):
    p=lib_path(lib_id)
    if p.exists(): p.unlink()
    return {"ok":True}

@app.post('/api/generate')
def api_generate(req:GenReq):
    c=config(); out=req.output_dir or c.get('output_dir') or DEFAULT_OUTPUT_DIR
    base=req.server_base or c.get('server_base') or os.getenv("SERVER_BASE",f"http://127.0.0.1:{settings.get('WEBDAV_PORT',8000)}")
    c['output_dir']=out; c['server_base']=base; save_config(c)
    try: return generate_strm(req.lib_id,out,base,req.include_subtitles)
    except Exception as e: return JSONResponse({"ok":False,"error":str(e)},status_code=400)

@app.get('/play/{file_id}/{etag}/{size}/{filename:path}')
def play_direct(file_id:int,etag:str,size:int,filename:str):
    cfg=config(); fast_mode=(cfg.get('mode','cache')=='fast')
    url=get_file_url_with_etag_candidates(name=filename,etag=etag,size=int(size),fast_mode=fast_mode)
    if not url: raise HTTPException(500,'failed')
    return RedirectResponse(url=url,status_code=302)

@app.get('/play/{lib_id}/{idx}')
def play_legacy(lib_id:str,idx:int):
    lib=load_lib(lib_id); files=lib.get('files',[])
    if idx<0 or idx>=len(files): raise HTTPException(404,'not found')
    f=files[idx]; name=Path(f['path']).name
    cfg=config(); fast_mode=(cfg.get('mode','cache')=='fast')
    url=get_file_url_with_etag_candidates(name=name,etag=f['etag'],size=int(f.get('size') or 0),fast_mode=fast_mode)
    if not url: raise HTTPException(500,'failed')
    return RedirectResponse(url=url,status_code=302)

@app.get('/api/mode')
def api_get_mode(): return {"mode":config().get('mode','cache')}

@app.post('/api/mode')
def api_set_mode(req:ModeReq):
    if req.mode not in ('cache','fast'): return JSONResponse({"ok":False,"error":"invalid mode"},status_code=400)
    c=config(); c['mode']=req.mode; save_config(c)
    return {"ok":True,"mode":req.mode,"label":"入库模式(1分钟清理)" if req.mode=='fast' else "缓存模式(24小时清理)"}

@app.get('/api/backup')
def api_backup():
    libs=[]
    for p in sorted(LIB_DIR.glob('*.json')):
        try: libs.append(json.loads(p.read_text(encoding='utf-8')))
        except: pass
    return JSONResponse({"version":1,"exportedAt":int(time.time()),"app":"123pan-strm-docker","libraries":libs},headers={"Content-Disposition":f'attachment; filename="backup-{int(time.time())}.json"'})

@app.post('/api/restore/upload')
async def api_restore(file:UploadFile=File(...)):
    try:
        content=await file.read(); data=json.loads(content)
    except: return JSONResponse({"ok":False,"error":"parse error"},status_code=400)
    if data.get('app')!='123pan-strm-docker': return JSONResponse({"ok":False,"error":"invalid format"},status_code=400)
    restored=skipped=0
    for lib in data.get('libraries',[]):
        lib.setdefault('category',''); lib.setdefault('mode','cache')
        lid=lib.get('id') or lib.get('name','')
        if not lid: continue
        p=lib_path(lid)
        if p.exists(): skipped+=1; continue
        p.write_text(json.dumps(lib,ensure_ascii=False,indent=2),encoding='utf-8'); restored+=1
    return {"ok":True,"restored":restored,"skipped":skipped}

if __name__=='__main__':
    port=int(os.getenv("PORT",settings.get('WEBDAV_PORT',8000)))
    host=os.getenv("HOST",settings.get('WEBDAV_HOST','0.0.0.0'))
    print(f"STRM API: http://127.0.0.1:{port}/")
    uvicorn.run(app,host=host,port=port,log_level='warning',access_log=False,loop='asyncio',reload=False)
