#!/usr/bin/env python3
import os
import sys
import zipfile
import hashlib
import xml.etree.ElementTree as ET
from pathlib import Path

def extract_addon_xml_from_zip(zip_path):
    """从 zip 文件中提取 addon.xml 的文本内容"""
    try:
        with zipfile.ZipFile(zip_path, 'r') as zf:
            for file_info in zf.infolist():
                if file_info.filename.endswith('addon.xml'):
                    with zf.open(file_info) as f:
                        return f.read().decode('utf-8')
    except Exception as e:
        print(f"[ERROR] 读取 {zip_path} 中的 addon.xml 失败: {e}")
    return None

def get_addon_xml_content(addon_item: Path):
    """从目录或其中的 zip 获取 addon.xml 内容"""
    if not addon_item.is_dir():
        return None
    
    # 优先从该插件目录下的 zip 包中解析 addon.xml
    zip_files = list(addon_item.glob("*.zip"))
    if zip_files:
        zip_files.sort(key=lambda p: p.stat().st_mtime, reverse=True)
        xml_content = extract_addon_xml_from_zip(zip_files[0])
        if xml_content:
            return xml_content

    # 若无 zip 包，尝试直接读取 addon.xml
    xml_path = addon_item / "addon.xml"
    if xml_path.exists():
        return xml_path.read_text(encoding='utf-8')
    
    return None

def clean_xml_header(content):
    lines = [line for line in content.splitlines() if not line.strip().startswith('<?xml')]
    return "\n".join(lines).strip()

def process_dir(target_dir):
    zips_root = Path(target_dir) / "zips"
    if not zips_root.exists():
        print(f"[WARN] 目录 {zips_root} 不存在，跳过处理")
        return

    all_addon_xmls = []

    # 扫描 zips 目录下的各个插件目录 (如 zips/plugin.video.juku, zips/service.litepan)
    for item in sorted(zips_root.iterdir()):
        if item.is_dir():
            print(f"[DEBUG] 扫描到目录: {item.name}")
            xml_content = get_addon_xml_content(item)
            if xml_content:
                cleaned = clean_xml_header(xml_content)
                all_addon_xmls.append(cleaned)
                print(f"[DEBUG] 成功提取 {item.name} 的 addon.xml (长度: {len(cleaned)})")
            else:
                print(f"[WARN] 无法提取 {item.name} 的 addon.xml")

    # 生成 zips 根目录统一的 addons.xml 与 md5
    if all_addon_xmls:
        unique_xmls = list(dict.fromkeys(all_addon_xmls))
        content = '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>\n<addons>\n'
        content += "\n".join(unique_xmls) + "\n</addons>\n"

        out_xml = zips_root / "addons.xml"
        out_xml.write_text(content, encoding='utf-8')

        md5_str = hashlib.md5(content.encode('utf-8')).hexdigest()
        out_md5 = zips_root / "addons.xml.md5"
        out_md5.write_text(md5_str, encoding='utf-8')
        print(f"[SUCCESS] 生成插件库统一索引: {out_xml} (共 {len(unique_xmls)} 个插件, MD5: {md5_str})")
    else:
        print(f"[WARN] 未在 {zips_root} 下找到任何有效的插件目录")

if __name__ == "__main__":
    target = sys.argv[1] if len(sys.argv) > 1 else "."
    print(f"--- 开始构建 Kodi 插件库索引 ({target}) ---")
    process_dir(target)
    print("--- 插件库索引构建完成 ---")