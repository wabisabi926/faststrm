import os
import sys
import urllib.request
import urllib.error
import json

OWNER = "wabisabi926"
REPO = "faststrm"
CHANGES_DIR = r"c:\Users\liwl\Downloads\AI\faststrm-go\.changes"

# Token: 必须通过环境变量 GITHUB_TOKEN 传入，避免明文写进仓库（触发 GitHub Push Protection）
#   export GITHUB_TOKEN=ghp_xxx   或   $env:GITHUB_TOKEN="ghp_xxx"
TOKEN = os.environ.get("GITHUB_TOKEN", "").strip()
if not TOKEN:
    print("ERROR: 请先设置环境变量 GITHUB_TOKEN", file=sys.stderr)
    print("  export GITHUB_TOKEN=ghp_xxx   或   $env:GITHUB_TOKEN=\"ghp_xxx\"", file=sys.stderr)
    sys.exit(1)

API = "https://api.github.com"
HEADERS = {
    "Accept": "application/vnd.github+json",
    "Authorization": f"Bearer {TOKEN}",
    "X-GitHub-Api-Version": "2022-11-28",
    "User-Agent": "faststrm-release-sync",
}


def gh(method, path, data=None):
    url = API + path
    body = None
    headers = dict(HEADERS)
    if data is not None:
        body = json.dumps(data).encode("utf-8")
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(url, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read().decode("utf-8")
            return resp.status, json.loads(raw) if raw else None
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        try:
            info = json.loads(raw)
        except Exception:
            info = {"raw": raw}
        return e.code, info


def load_notes(tag):
    """Return markdown notes string for a given tag (e.g. 'v1.0.5'), or None."""
    # Try exact .changes match first
    path = os.path.join(CHANGES_DIR, f"{tag}.md")
    if os.path.isfile(path):
        with open(path, "r", encoding="utf-8") as f:
            return f.read().strip()
    # Fallback: v0.9.1 doesn't have .changes file
    if tag == "v0.9.1":
        return (
            "# v0.9.1 - 2026-08-19 · 首个 Go 重构版本\n\n"
            "## ✨ 新增\n\n"
            "- 完成 Go 语言重构，彻底移除 Node.js 和 Nginx 依赖，单二进制即可部署\n"
            "- 实现 115 扫码登录，手机扫一扫即可获取 Cookie，过期再扫一下就好\n"
            "- 实现生活事件监控，网盘增删改自动同步到本地\n"
            "- 实现 STRM 文件自动生成与清理对账\n"
            "- 实现 Emby 主动刷库联动 + 删除同步\n"
            "- 实现 Telegram / Webhook 消息通知\n"
            "- 实现 302 直连播放模式，不占用服务器带宽\n\n"
            "## ⚡ 优化\n\n"
            "- UI 统一为简洁风格\n"
            "- 前端升级为 Vite + React 技术栈\n"
            "- 后端升级为 Go 单二进制架构，部署更简单\n"
        )
    return None


def main():
    # 1. List all releases (paginated)
    releases = []
    page = 1
    while True:
        status, data = gh("GET", f"/repos/{OWNER}/{REPO}/releases?per_page=100&page={page}")
        if status != 200:
            print(f"[FAIL] list releases: HTTP {status} {data}")
            sys.exit(1)
        if not isinstance(data, list) or not data:
            break
        releases.extend(data)
        if len(data) < 100:
            break
        page += 1

    print(f"[INFO] Found {len(releases)} releases on GitHub")

    # 2. For each release, update body
    ok, skip, fail = 0, 0, 0
    for rel in releases:
        tag = rel.get("tag_name", "")
        rel_id = rel.get("id")
        name = rel.get("name", "")
        old_body = rel.get("body") or ""
        notes = load_notes(tag)
        if notes is None:
            print(f"[SKIP] {tag} (id={rel_id}) — no matching .changes file")
            skip += 1
            continue
        # Only touch if actually different (trim comparison to avoid whitespace ping-pong)
        if old_body.strip() == notes.strip():
            print(f"[OK-] {tag} (id={rel_id}) — already in sync")
            ok += 1
            continue
        status, resp = gh(
            "PATCH",
            f"/repos/{OWNER}/{REPO}/releases/{rel_id}",
            {"body": notes},
        )
        if status in (200, 201):
            print(f"[OK]  {tag} (id={rel_id}) — Release Notes updated")
            ok += 1
        else:
            print(f"[FAIL] {tag} (id={rel_id}) — HTTP {status}: {resp}")
            fail += 1

    print(f"\nDone. ok={ok}  skipped={skip}  failed={fail}")
    if fail:
        sys.exit(2)


if __name__ == "__main__":
    main()
