from Pan123 import Pan123
import base64
import requests
import yaml
import os
import json
import time
import hashlib
import threading
from pathlib import Path

# 与 strm_app.py 保持一致的路径解析
_BASE_DIR = Path(__file__).resolve().parent
_DEFAULT_DATA_DIR = Path("/data") if Path("/data").exists() else _BASE_DIR / "strm_data"
_DATA_DIR = Path(os.getenv("DATA_DIR", str(_DEFAULT_DATA_DIR))).expanduser()
SETTINGS_PATH = os.getenv("SETTINGS_PATH", str(_DATA_DIR / "settings.yaml"))
CACHE_PATH = os.getenv("CACHE_PATH", str(_DATA_DIR / "cache.json"))
# 缓存文件夹固定名（与 WebDAV 挂载版一致）
CACHE_DIR_NAME = "__缓存目录_无视即可_24h自动清理__123Pan-Unlimited-WebDAV"
def ensure_cache_file():
    try:
        os.makedirs(os.path.dirname(CACHE_PATH), exist_ok=True) if os.path.dirname(CACHE_PATH) else None
    except Exception:
        pass
    if not os.path.exists(CACHE_PATH):
        with open(CACHE_PATH, "w", encoding="utf-8") as f:
            json.dump(
                {
                    "accessToken": "",
                    "tokenCreateTime": "",
                    "lastDeleteTime": "",
                    "accountHash": "",
                    "cacheFolderId": "",
                    "directUrlCache": {},
                },
                f,
                indent=4,
                ensure_ascii=False)


def account_hash(username, password):
    raw = f"{username or ''}\n{password or ''}"
    return hashlib.sha256(raw.encode('utf-8')).hexdigest()


def reset_cache_for_account_change(cache_data, current_hash):
    if cache_data.get("accountHash") and cache_data.get("accountHash") != current_hash:
        print("检测到123账号或密码已变化，自动清理旧token缓存")
        cache_data["accessToken"] = ""
        cache_data["tokenCreateTime"] = ""
        cache_data["lastDeleteTime"] = ""
        cache_data["cacheFolderId"] = ""
    cache_data["accountHash"] = current_hash
    return cache_data


def get_file_url(name, etag, size, fast_mode=False) -> str:
    # 直链缓存：同一文件(etag+size) 30 分钟内直接返回，避免反复打 123 API
    # 解决 Emby HTTPStrm 10 秒超时问题：第二次播放/预解析秒回
    _cache_key = f"{etag}|{size}"
    try:
        with open(CACHE_PATH, "r", encoding="utf-8") as f:
            _cache = json.load(f)
        _url_map = _cache.get("directUrlCache") or {}
        _hit = _url_map.get(_cache_key)
        if _hit and time.time() - _hit.get("ts", 0) < 30 * 60:
            print(f"[直链缓存] 命中 {name} ({int(time.time())-int(_hit.get('ts',0))}s 前)")
            return _hit.get("url")
    except Exception:
        pass
    # 读取配置文件
    with open(SETTINGS_PATH, "r", encoding="utf-8") as f:
        settings_data = yaml.safe_load(f.read())
    current_hash = account_hash(settings_data.get("123PAN_USERNAME"), settings_data.get("123PAN_PASSWORD"))
    # 实例化
    driver = Pan123()
    # 登录账号并保存Token（假设有效期24h）
    with open(CACHE_PATH, "r", encoding="utf-8") as f:
        cache_data = json.load(f)
    cache_data = reset_cache_for_account_change(cache_data, current_hash)
    if cache_data.get("tokenCreateTime") \
        and time.time() - cache_data.get("tokenCreateTime") < 25 * 24 * 60 * 60 \
        and cache_data.get("accessToken"): # accessToken 30天有效, 这里设置为25天, 省事
        driver.setAccessToken(cache_data.get("accessToken"))
    else:
        driver.doLogin(
            username=settings_data.get("123PAN_USERNAME"),
            password=settings_data.get("123PAN_PASSWORD")
        )
        if driver.getAccessToken() is None:
            print("登录失败, 请检查用户名或密码能否正常登录")
            return None
        cache_data["accessToken"] = driver.getAccessToken()
        cache_data["tokenCreateTime"] = int(time.time())
        cache_data["accountHash"] = current_hash
        with open(CACHE_PATH, "w", encoding="utf-8") as f:
            json.dump(cache_data, f, indent=4, ensure_ascii=False)
    # 创建/复用缓存文件夹（避免每次播放都调 Mkdir，触发 123 网盘限流 100011）
    cache_folder_id = str(cache_data.get("cacheFolderId") or "")
    if cache_folder_id:
        cacheFolderInfo = {"FileId": int(cache_folder_id), "FileName": CACHE_DIR_NAME}
        cacheFolderId = int(cache_folder_id)
    else:
        # 先查根目录是否已有同名缓存文件夹（单层，非递归）
        cacheFolderId = None
        try:
            listed = driver.listFilesSingle(0)
            for it in (listed.get("items") or []):
                if it.get("FileName") == CACHE_DIR_NAME:
                    cacheFolderId = int(it.get("FileId"))
                    cacheFolderInfo = {"FileId": cacheFolderId, "FileName": CACHE_DIR_NAME}
                    cache_data["cacheFolderId"] = cacheFolderId
                    with open(CACHE_PATH, "w", encoding="utf-8") as f:
                        json.dump(cache_data, f, indent=4, ensure_ascii=False)
                    break
        except Exception as e:
            print(f"查找缓存文件夹异常(忽略): {e}")
        if not cacheFolderId:
            action_result = driver.createFolder(0, CACHE_DIR_NAME, True)
            if action_result.get("isFinish"):
                cacheFolderInfo = action_result.get("message").get("Info")
                cacheFolderId = cacheFolderInfo.get("FileId")
                cache_data["cacheFolderId"] = cacheFolderId
                with open(CACHE_PATH, "w", encoding="utf-8") as f:
                    json.dump(cache_data, f, indent=4, ensure_ascii=False)
            else:
                print(action_result.get("message"))
                return None
    # 上传文件
    action_result = driver.uploadFile(
                            etag=etag,
                            fileName=name,
                            parentFileId=cacheFolderId,
                            size=size,
                            raw_data=True
                        )
    if action_result.get("isFinish"):
        file_data = action_result.get("message").get("Info")
        # print(action_result.get("message").get("Info"))
    else:
        print(action_result.get("message"))
        return None
    # 获取下载地址
    action_result = driver.downloadFile(
        etag=file_data.get("Etag"),
        fileId=file_data.get("FileId"),
        S3KeyFlag=file_data.get("S3KeyFlag"),
        type=file_data.get("Type"),
        fileName=file_data.get("FileName"),
        size=file_data.get("Size")
    )
    if action_result.get("isFinish"):
        download_link = action_result.get("message")
        # print(download_link)
    else:
        print(action_result.get("message"))
        return None
    # 删除文件夹
    # 如果缓存里没有上次删除时间, 则把当前时间设置为上次删除时间
    if not cache_data.get("lastDeleteTime"):
        cache_data["lastDeleteTime"] = int(time.time())
        with open(CACHE_PATH, "w", encoding="utf-8") as f:
            json.dump(cache_data, f, indent=4, ensure_ascii=False)
    # 现在缓存里一定有时间，判断间隔是否24小时，如果大于24小时则删除
    if time.time() - cache_data.get("lastDeleteTime") > 24 * 60 * 60:
        # 删除文件夹（纯缓存清理，失败绝不阻断播放——直链此时已拿到）
        try:
            action_result = driver.deleteFile([cacheFolderInfo], True)
            if action_result.get("isFinish"):
                print(f"彻底删除文件夹 {cacheFolderInfo.get('FileName')} 成功")
                cache_data["cacheFolderId"] = ""
            else:
                print(f"清理缓存文件夹失败(忽略，不影响播放): {action_result.get('message')}")
        except Exception as e:
            print(f"清理缓存文件夹异常(忽略，不影响播放): {e}")
        # 无论成败都更新 lastDeleteTime：避免每次播放都重试删除，
        # 导致 123 网盘连续限流(100011 请勿频繁操作)滚雪球
        cache_data["lastDeleteTime"] = int(time.time())
        with open(CACHE_PATH, "w", encoding="utf-8") as f:
            json.dump(cache_data, f, indent=4, ensure_ascii=False) 
    # 退出登录
    # driver.doLogout()
    # 获取跳转后的链接
    real_url = download_link.split("params=")[-1].split("&")[0]
    real_url = base64.b64decode(real_url).decode("utf-8")
    # 判断该链接是不是最终链接（加超时，防止无限挂起拖垮 /play/ 响应）
    headers = {"Referer": "https://yun.123pan.com/"}
    response = requests.get(real_url, headers=headers, allow_redirects=False, timeout=8)
    if response.status_code == 302:
        # 如果是 302 重定向，从 'Location' 头获取最终 URL
        final_url = response.headers.get("location")
    elif response.status_code < 300:
        # 如果是成功状态码 (如 200 OK)，解析 JSON
        try:
            data = response.json()
            final_url = data.get("data").get("redirect_url")
        except requests.exceptions.JSONDecodeError:
            print("Status was 2xx, but failed to decode JSON response.")
            return None
    else:
        # 其他非成功状态码
        print(f"Request failed with status code: {response.status_code}")
        return None
    
    # 如果播放过程中自动重新登录刷新了 token，写回 cache.json
    try:
        if driver.getAccessToken() and driver.getAccessToken() != cache_data.get("accessToken"):
            cache_data["accessToken"] = driver.getAccessToken()
            cache_data["tokenCreateTime"] = int(time.time())
            cache_data["accountHash"] = current_hash
            with open(CACHE_PATH, "w", encoding="utf-8") as f:
                json.dump(cache_data, f, indent=4, ensure_ascii=False)
            print("已写回刷新后的123 token缓存")
    except Exception as e:
        print("写回刷新token失败:", e)

    # 入库模式：获取直链后1分钟异步删除该文件
    if fast_mode:
        def _delayed_delete(_driver, _file_data, _name):
            print(f"入库模式：将于60秒后删除临时文件 {_name}")
            time.sleep(60)
            try:
                result = _driver.deleteFile([_file_data], True)
                if result.get("isFinish"):
                    print(f"入库模式：已删除临时文件 {_name}")
                else:
                    print(f"入库模式：删除临时文件失败 {_name}: {result.get('message')}")
            except Exception as e:
                print(f"入库模式：删除临时文件异常 {_name}: {e}")
        threading.Thread(target=_delayed_delete, args=(driver, file_data, name), daemon=True).start()

    print(f"获取到 {name} 的真实 URL: {final_url}")

    # 写入直链缓存（30 分钟），下次播放/Emby 重试直接命中
    if final_url:
        try:
            _url_map = cache_data.setdefault("directUrlCache", {})
            _url_map[_cache_key] = {"url": final_url, "ts": int(time.time())}
            # 限制缓存条数，防止无限膨胀
            if len(_url_map) > 200:
                for _k in sorted(_url_map, key=lambda k: _url_map[k].get("ts", 0))[:len(_url_map) - 200]:
                    _url_map.pop(_k, None)
            with open(CACHE_PATH, "w", encoding="utf-8") as f:
                json.dump(cache_data, f, indent=4, ensure_ascii=False)
        except Exception as e:
            print(f"写直链缓存失败(忽略): {e}")

    return final_url



if __name__ == "__main__":
    name = "提灯映桃花 晚安时间到·楚河（CV：袁铭喆）微博@种草小呆萌.mp4"
    etag = "df5f8f335a1043be16e3e6e8f83c3072"
    size = 552721
    get_file_url(name=name, etag=etag, size=size)