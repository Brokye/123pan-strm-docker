"""
STRM 同步模块
功能：
1. 根据库文件配置，自动同步 STRM 文件和字幕文件
2. 自动删除多余的 STRM 文件和字幕文件
3. 支持全量同步和增量同步
"""
import json
import logging
from pathlib import Path
from typing import Dict, List, Tuple, Optional, Set
from concurrent.futures import ThreadPoolExecutor, as_completed
import threading

from strm_app import (
    VIDEO_EXTS,
    SUBTITLE_EXTS,
    CATEGORY_DIRS,
    safe_rel_path,
    make_play_url,
    download_subtitle_file,
    load_lib,
    DEFAULT_OUTPUT_DIR,
)

logger = logging.getLogger(__name__)

# 同步状态
SYNC_STATUS = {}
SYNC_LOCK = threading.Lock()


def _cat_root(output_dir: str, category: str) -> Path:
    """计算库文件实际输出根目录（含分类子目录）"""
    root = Path(output_dir).expanduser()
    if category and category in CATEGORY_DIRS:
        root = root / CATEGORY_DIRS[category]
    return root


def get_expected_files(lib: Dict, output_dir: str, include_subtitles: bool = False) -> Tuple[List[Path], List[Path]]:
    """
    根据库文件配置，计算期望生成的 STRM 文件和字幕文件列表

    Returns:
        (strm_files, subtitle_files)
    """
    category = lib.get('category', '')
    out_root = _cat_root(output_dir, category)

    strm_files = []
    subtitle_files = []

    for f in lib.get('files', []):
        path = f.get('path', '')
        if not path:
            continue
        rel = safe_rel_path(path)
        ext = rel.suffix.lower()
        if ext in VIDEO_EXTS:
            strm_files.append(out_root / rel.with_suffix('.strm'))
        elif include_subtitles and ext in SUBTITLE_EXTS:
            subtitle_files.append(out_root / rel)

    return strm_files, subtitle_files


def get_existing_files(scan_root: Path) -> Tuple[List[Path], List[Path]]:
    """
    获取输出目录中现有的 STRM 文件和字幕文件

    Returns:
        (strm_files, subtitle_files)
    """
    if not scan_root.exists():
        return [], []

    strm_files = list(scan_root.rglob('*.strm'))
    subtitle_files = [f for f in scan_root.rglob('*') if f.suffix.lower() in SUBTITLE_EXTS]
    return strm_files, subtitle_files


def _collect_expected(libs: List[Dict], output_dir: str, include_subtitles: bool) -> Tuple[Set[Path], Set[Path], Dict[Path, Dict]]:
    """合并多个库的期望文件，并构建「目标路径 -> 文件信息」映射"""
    strm_map: Dict[Path, Dict] = {}
    sub_map: Dict[Path, Dict] = {}
    for lib in libs:
        category = lib.get('category', '')
        cat_root = _cat_root(output_dir, category)
        for f in lib.get('files', []):
            path = f.get('path', '')
            if not path:
                continue
            rel = safe_rel_path(path)
            ext = rel.suffix.lower()
            if ext in VIDEO_EXTS:
                strm_map.setdefault(cat_root / rel.with_suffix('.strm'), f)
            elif include_subtitles and ext in SUBTITLE_EXTS:
                sub_map.setdefault(cat_root / rel, f)
    return set(strm_map.keys()), set(sub_map.keys()), strm_map, sub_map


def _sync(
    expected_strm: Set[Path],
    expected_subs: Set[Path],
    strm_map: Dict[Path, Dict],
    sub_map: Dict[Path, Dict],
    output_dir: str,
    server_base: str,
    include_subtitles: bool,
    dry_run: bool,
    label: str = '',
    scan_root: Path = None,
) -> Dict:
    """核心同步：对比期望与现有，删除多余、生成缺失"""
    out_root_dir = Path(output_dir).expanduser()
    if scan_root is None:
        scan_root = out_root_dir
    existing_strm, existing_subs = get_existing_files(scan_root)
    existing_strm_set = set(existing_strm)
    existing_subs_set = set(existing_subs)

    to_delete_strm = existing_strm_set - expected_strm
    to_delete_subs = existing_subs_set - expected_subs
    to_create_strm = expected_strm - existing_strm_set
    to_create_subs = expected_subs - existing_subs_set

    result = {
        'lib_id': label,
        'expected_strm': len(expected_strm),
        'expected_subs': len(expected_subs),
        'existing_strm': len(existing_strm),
        'existing_subs': len(existing_subs),
        'to_delete_strm': len(to_delete_strm),
        'to_delete_subs': len(to_delete_subs),
        'to_create_strm': len(to_create_strm),
        'to_create_subs': len(to_create_subs),
        'deleted_strm': [],
        'deleted_subs': [],
        'created_strm': [],
        'created_subs': [],
        'errors': [],
    }

    if dry_run:
        result['dry_run'] = True
        logger.info(f"[干跑] {label}: 期望 {len(expected_strm)} 个 STRM, 现有 {len(existing_strm)} 个")
        logger.info(f"[干跑] 需删除: {len(to_delete_strm)} 个 STRM, {len(to_delete_subs)} 个字幕")
        logger.info(f"[干跑] 需生成: {len(to_create_strm)} 个 STRM, {len(to_create_subs)} 个字幕")
        return result

    rel_of = lambda p: str(p.relative_to(out_root_dir))

    # 删除多余的 STRM 文件
    for f in to_delete_strm:
        try:
            f.unlink()
            result['deleted_strm'].append(rel_of(f))
            logger.info(f"删除 STRM: {f}")
        except Exception as e:
            result['errors'].append(f"删除 STRM 失败 {f}: {e}")
            logger.error(f"删除 STRM 失败 {f}: {e}")

    # 删除多余的字幕文件
    for f in to_delete_subs:
        try:
            f.unlink()
            result['deleted_subs'].append(rel_of(f))
            logger.info(f"删除字幕: {f}")
        except Exception as e:
            result['errors'].append(f"删除字幕失败 {f}: {e}")
            logger.error(f"删除字幕失败 {f}: {e}")

    # 清理空目录（仅当目录为空时）
    def cleanup_empty_dirs(base_dir: Path, stop_at: Path = None):
        if base_dir == stop_at or not base_dir.exists():
            return
        try:
            for subdir in list(base_dir.iterdir()):
                if subdir.is_dir():
                    cleanup_empty_dirs(subdir, stop_at)
            if not any(base_dir.iterdir()):
                base_dir.rmdir()
                logger.info(f"清理空目录: {base_dir}")
        except Exception:
            pass

    cleanup_empty_dirs(scan_root, stop_at=out_root_dir if scan_root != out_root_dir else None)

    # 生成缺失的 STRM 文件
    def create_strm(strm_path: Path):
        file_info = strm_map.get(strm_path)
        if file_info is None:
            return False
        url = make_play_url(server_base, file_info['idx'], file_info['etag'],
                            int(file_info.get('size') or 0), Path(file_info['path']).name)
        strm_path.parent.mkdir(parents=True, exist_ok=True)
        strm_path.write_text(url + '\n', encoding='utf-8')
        result['created_strm'].append(rel_of(strm_path))
        logger.info(f"生成 STRM: {strm_path}")
        return True

    with ThreadPoolExecutor(max_workers=8) as executor:
        futures = {executor.submit(create_strm, f): f for f in to_create_strm}
        for future in as_completed(futures):
            strm_path = futures[future]
            try:
                future.result()
            except Exception as e:
                result['errors'].append(f"生成 STRM 失败 {strm_path}: {e}")
                logger.error(f"生成 STRM 失败 {strm_path}: {e}")

    # 下载缺失的字幕文件
    if include_subtitles:
        cfg = {}
        try:
            from strm_app import config
            cfg = config()
        except Exception:
            pass

        # 未登录 123 网盘时跳过字幕下载，避免逐个尝试登录导致卡顿
        try:
            from strm_app import CACHE_PATH
            import time as _time
            _cached = {}
            try:
                _cached = json.loads(CACHE_PATH.read_text(encoding='utf-8'))
            except Exception:
                pass
            logged_in = (_cached.get('accessToken') and _cached.get('tokenCreateTime')
                         and _time.time() - int(_cached.get('tokenCreateTime') or 0) < 25 * 24 * 60 * 60)
        except Exception:
            logged_in = False

        if not logged_in and to_create_subs:
            result['subtitle_skipped'] = '未登录 123 网盘，跳过字幕下载'
            logger.warning("未登录 123 网盘，跳过字幕下载")
            return result

        def download_sub(sub_path: Path):
            file_info = sub_map.get(sub_path)
            if file_info is None:
                return False
            try:
                fast_mode = cfg.get('mode', 'cache') == 'fast'
                if download_subtitle_file(file_info, sub_path, fast_mode):
                    result['created_subs'].append(rel_of(sub_path))
                    logger.info(f"下载字幕: {sub_path}")
                    return True
            except Exception as e:
                logger.error(f"下载字幕失败 {sub_path}: {e}")
            return False

        with ThreadPoolExecutor(max_workers=8) as executor:
            futures = {executor.submit(download_sub, f): f for f in to_create_subs}
            for future in as_completed(futures):
                sub_path = futures[future]
                try:
                    future.result()
                except Exception as e:
                    result['errors'].append(f"下载字幕失败 {sub_path}: {e}")
                    logger.error(f"下载字幕失败 {sub_path}: {e}")

    return result


def sync_all_libraries(
    output_dir: str = None,
    server_base: str = None,
    include_subtitles: bool = False,
    dry_run: bool = False,
) -> Dict:
    """
    同步所有库的 STRM 文件：合并全部库的期望后整体对比，避免同分类多库互相误删。

    Returns:
        总体同步结果
    """
    from strm_app import config, list_libraries

    cfg = config()
    if not output_dir:
        output_dir = cfg.get('output_dir') or DEFAULT_OUTPUT_DIR
    if not server_base:
        server_base = cfg.get('server_base') or 'http://127.0.0.1:8000'

    out_root_dir = Path(output_dir).expanduser()

    libraries = list_libraries()
    all_libs = [load_lib(info['id']) for info in libraries if info.get('id')]

    expected_strm, expected_subs, strm_map, sub_map = _collect_expected(all_libs, output_dir, include_subtitles)

    result = _sync(expected_strm, expected_subs, strm_map, sub_map, output_dir, server_base,
                   include_subtitles, dry_run, label='all',
                   scan_root=out_root_dir)

    total_result = {
        'libraries': result,
        'total_expected_strm': result['expected_strm'],
        'total_existing_strm': result['existing_strm'],
        'total_to_delete_strm': result['to_delete_strm'],
        'total_to_create_strm': result['to_create_strm'],
        'total_deleted_strm': len(result['deleted_strm']),
        'total_created_strm': len(result['created_strm']),
        'total_deleted_subs': len(result['deleted_subs']),
        'total_created_subs': len(result['created_subs']),
        'total_errors': len(result['errors']),
    }
    return total_result
