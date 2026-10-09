#!/usr/bin/env bash
# ==============================================================================
# FastStrm Kodi (CoreELEC / Linux ARM64) 插件一键构建脚本
# 作用: 交叉编译 Go 单二进制、生成 Kodi 插件文件 (addon.xml, service.py 等) 并打包为 ZIP
# 用法: ./build_kodi_addon.sh [arm64|armv7|amd64|all|<预编译二进制路径>]
#   - 默认 arm64 (CoreELEC 主流平台)
# ==============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${SCRIPT_DIR}"
BUILD_DIR="${ROOT_DIR}/dist/kodi_build"
DIST_DIR="${ROOT_DIR}/dist"
ADDON_ID="service.faststrm"
ADDON_DIR="${BUILD_DIR}/${ADDON_ID}"

# 主程序版本信息 (自动从 cmd/server/main.go 中提取)
APP_VERSION="v1.4.1"
if [ -f "${ROOT_DIR}/cmd/server/main.go" ]; then
    EXTRACTED_VER=$(grep -E '^[[:space:]]*version[[:space:]]*=' "${ROOT_DIR}/cmd/server/main.go" | cut -d '"' -f 2)
    if [ -n "${EXTRACTED_VER}" ]; then
        APP_VERSION="${EXTRACTED_VER}"
    fi
fi

# 基础版本号 (去除开头的 v, 如 v1.4.1 -> 1.4.1)
BASE_KODI_VERSION=$(echo "${APP_VERSION}" | sed 's/^v//' | sed 's/-.*//')

# 插件发布版本号
KODI_VERSION="${KODI_ADDON_VERSION:-${BASE_KODI_VERSION}}"
VERSION="v${KODI_VERSION}"

# 参数解析
ARCH_PARAM="${1:-arm64}"

echo "=================================================="
echo " FastStrm Kodi 插件一键构建工具"
echo " FastStrm 版本: ${VERSION} (Kodi Addon Version: ${KODI_VERSION})"
echo " 构建目标架构: ${ARCH_PARAM}"
echo "=================================================="

rm -rf "${ADDON_DIR}"
mkdir -p "${ADDON_DIR}/bin"
mkdir -p "${ADDON_DIR}/resources"
mkdir -p "${DIST_DIR}"

compile_go() {
    local goos="$1"
    local goarch="$2"
    local goarm="$3"
    local out_name="$4"
    local target_path="${ADDON_DIR}/bin/${out_name}"

    echo " -> 正在交叉编译 ${goos}/${goarch}${goarm:+ (GOARM=${goarm})} 二进制..."
    cd "${ROOT_DIR}"

    local BUILD_DATE
    BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    local LDFLAGS="-s -w -X 'main.version=${VERSION}' -X 'main.BuildDate=${BUILD_DATE}'"

    if [ -n "${goarm}" ]; then
        if ! CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" GOARM="${goarm}" go build -ldflags="${LDFLAGS}" -o "${target_path}" ./cmd/server; then
            echo "编译失败: ${goarch} (GOARM=${goarm})"
            return 1
        fi
    else
        if ! CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" go build -ldflags="${LDFLAGS}" -o "${target_path}" ./cmd/server; then
            echo "编译失败: ${goarch}"
            return 1
        fi
    fi

    chmod +x "${target_path}"
    echo "    编译成功: ${out_name}"
}

# 1. 复制图标资源
echo "[1/4] 复制图标与静态资源..."
if [ -f "${ROOT_DIR}/frontend/public/logo.png" ]; then
    cp "${ROOT_DIR}/frontend/public/logo.png" "${ADDON_DIR}/icon.png"
elif [ -f "${ROOT_DIR}/internal/web/spa/logo.png" ]; then
    cp "${ROOT_DIR}/internal/web/spa/logo.png" "${ADDON_DIR}/icon.png"
fi

# 2. 编译或拷贝二进制文件
if [ -f "${ARCH_PARAM}" ]; then
    echo "[2/4] 使用用户指定的预编译二进制文件: ${ARCH_PARAM}"
    cp "${ARCH_PARAM}" "${ADDON_DIR}/bin/faststrm"
    chmod +x "${ADDON_DIR}/bin/faststrm"
    ZIP_ARCH_TAG="custom"
elif [ "${ARCH_PARAM}" = "arm64" ] || [ "${ARCH_PARAM}" = "aarch64" ]; then
    echo "[2/4] 开始编译 ARM64 (64位 Linux ARM / CoreELEC 主流) 二进制..."
    compile_go "linux" "arm64" "" "faststrm_arm64"
    ZIP_ARCH_TAG="arm64"
elif [ "${ARCH_PARAM}" = "armv7" ] || [ "${ARCH_PARAM}" = "arm7" ] || [ "${ARCH_PARAM}" = "arm32" ]; then
    echo "[2/4] 开始编译 ARMv7 (32位 Linux ARM) 二进制..."
    compile_go "linux" "arm" "7" "faststrm_armv7"
    ZIP_ARCH_TAG="armv7"
elif [ "${ARCH_PARAM}" = "amd64" ] || [ "${ARCH_PARAM}" = "x86_64" ]; then
    echo "[2/4] 开始编译 AMD64 (64位 Linux x86) 二进制..."
    compile_go "linux" "amd64" "" "faststrm_amd64"
    ZIP_ARCH_TAG="amd64"
elif [ "${ARCH_PARAM}" = "all" ] || [ "${ARCH_PARAM}" = "universal" ]; then
    echo "[2/4] 开始编译双架构 (ARM64 + AMD64) 通用二进制..."
    compile_go "linux" "arm64" "" "faststrm_arm64"
    compile_go "linux" "amd64" "" "faststrm_amd64"
    ZIP_ARCH_TAG="universal"
else
    echo "未知架构参数: ${ARCH_PARAM}"
    exit 1
fi

# 3. 生成 Kodi 插件描述文件与相关脚本
echo "[3/4] 生成 Kodi 插件清单与交互/守护脚本..."

# 生成 addon.xml
cat <<EOF > "${ADDON_DIR}/addon.xml"
<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<addon id="${ADDON_ID}" name="FastStrm Service" version="${KODI_VERSION}" provider-name="wabisabi926">
  <requires>
    <import addon="xbmc.python" version="3.0.0"/>
  </requires>
  <extension point="xbmc.service" library="service.py" start="startup"/>
  <extension point="xbmc.python.pluginsource" library="default.py">
    <provides>executable</provides>
  </extension>
  <extension point="xbmc.addon.metadata">
    <summary lang="zh_CN">FastStrm 让 115 网盘和你的播放器真正「同步」</summary>
    <summary lang="en_GB">FastStrm syncs your 115 cloud drive with your player</summary>
    <description lang="zh_CN">在 CoreELEC / Linux (ARM64 / x86_64) 后台静默运行 FastStrm，让 115 网盘和你的播放器真正「同步」：播放时直连 115 CDN 流式传输，不下载任何东西、4K 秒开、不占硬盘；10 秒感知网盘增删改，新上传自动生成 .strm，删除自动清理；扫码登录零配置，Cookie 过期还能一键刷新；无需外置 NAS 或电脑。</description>
    <description lang="en_GB">Run FastStrm daemon in CoreELEC / Linux to keep your 115 cloud drive and player in sync: stream directly from the 115 CDN without downloading, auto-generate .strm files within seconds, QR-code login with zero config, no external NAS or PC required.</description>
    <platform>linux</platform>
    <license>MIT</license>
    <assets>
      <icon>icon.png</icon>
    </assets>
  </extension>
</addon>
EOF

# 生成 settings.xml
cat <<EOF > "${ADDON_DIR}/resources/settings.xml"
<?xml version="1.0" encoding="utf-8" standalone="yes"?>
<settings>
    <category label="30000">
        <setting id="server_port" type="number" label="30001" default="8090" />
        <setting id="data_dir" type="folder" label="30002" default="" />
        <setting id="strm_dir" type="folder" label="30003" default="/storage/videos/strm" />
        <setting id="notify_startup" type="bool" label="30004" default="true" />
    </category>
</settings>
EOF

# 生成中文语言包
mkdir -p "${ADDON_DIR}/resources/language/resource.language.zh_cn"
cat <<'EOF_STRINGS' > "${ADDON_DIR}/resources/language/resource.language.zh_cn/strings.po"
# FastStrm Kodi Addon Language File
msgid ""
msgstr ""
"Content-Type: text/plain; charset=UTF-8
"
"Language: zh_CN
"

msgctxt "#30000"
msgid "服务配置"
msgstr "服务配置"

msgctxt "#30001"
msgid "Web / API 服务端口 (默认 8090)"
msgstr "Web / API 服务端口 (默认 8090)"

msgctxt "#30002"
msgid "数据配置存储目录 (留空使用插件默认目录)"
msgstr "数据配置存储目录 (留空使用插件默认目录)"

msgctxt "#30003"
msgid "STRM 文件输出目录 (建议 /storage/videos/strm)"
msgstr "STRM 文件输出目录 (建议 /storage/videos/strm)"

msgctxt "#30004"
msgid "开机启动时显示 Web 访问地址提示"
msgstr "开机启动时显示 Web 访问地址提示"
EOF_STRINGS

# 生成 service.py (后台服务守护进程)
cat <<'EOF_SERVICE' > "${ADDON_DIR}/service.py"
# -*- coding: utf-8 -*-
import os
import stat
import subprocess
import platform
import socket
import time
import xbmc
import xbmcgui
import xbmcaddon
import xbmcvfs

ADDON_ID = "service.faststrm"

def get_local_ip():
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        s.connect(("119.29.29.29", 80))
        ip = s.getsockname()[0]
    except Exception:
        ip = "127.0.0.1"
    finally:
        s.close()
    return ip

class FastStrmService(xbmc.Monitor):
    def __init__(self):
        super().__init__()
        self.addon = xbmcaddon.Addon(ADDON_ID)
        self.addon_dir = xbmcvfs.translatePath(self.addon.getAddonInfo("path"))
        self.profile_dir = xbmcvfs.translatePath(self.addon.getAddonInfo("profile"))
        self.bin_path = self.detect_binary()
        self.process = None
        self.log_file = None

    def detect_binary(self):
        bin_dir = os.path.join(self.addon_dir, "bin")

        single_bin = os.path.join(bin_dir, "faststrm")
        if os.path.exists(single_bin):
            return single_bin

        machine = platform.machine().lower()
        xbmc.log(f"[FastStrm] 系统 CPU 架构检测为: {machine}", xbmc.LOGINFO)

        target_name = "faststrm_arm64"
        if "aarch64" in machine or "arm64" in machine:
            target_name = "faststrm_arm64"
        elif "x86_64" in machine or "amd64" in machine:
            target_name = "faststrm_amd64"
        elif "arm" in machine or "armv7" in machine or "armv6" in machine:
            target_name = "faststrm_armv7"

        target_bin = os.path.join(bin_dir, target_name)
        if os.path.exists(target_bin):
            return target_bin

        if os.path.exists(bin_dir):
            files = [f for f in os.listdir(bin_dir) if not f.startswith(".")]
            if files:
                return os.path.join(bin_dir, files[0])

        return os.path.join(bin_dir, "faststrm_arm64")

    def get_paths_and_port(self):
        custom_data_dir = self.addon.getSetting("data_dir").strip()
        if custom_data_dir:
            base_dir = xbmcvfs.translatePath(custom_data_dir)
        else:
            base_dir = self.profile_dir

        config_dir = os.path.join(base_dir, "config")
        data_dir = os.path.join(base_dir, "data")

        port_str = self.addon.getSetting("server_port").strip()
        try:
            port = int(port_str)
        except Exception:
            port = 8090

        custom_strm_dir = self.addon.getSetting("strm_dir").strip()
        strm_dir = xbmcvfs.translatePath(custom_strm_dir) if custom_strm_dir else "/storage/videos/strm"

        return config_dir, data_dir, port, strm_dir

    def onSettingsChanged(self):
        xbmc.log("[FastStrm] 检测到设置更新，正在重启后台服务...", xbmc.LOGINFO)
        self.stop_process()
        self.start_process()

    def start_process(self):
        config_dir, data_dir, port, strm_dir = self.get_paths_and_port()

        for p in [config_dir, data_dir, strm_dir]:
            try:
                os.makedirs(p, exist_ok=True)
            except Exception as e:
                xbmc.log(f"[FastStrm] 创建目录 {p} 失败: {str(e)}", xbmc.LOGWARNING)

        if not self.bin_path or not os.path.exists(self.bin_path):
            xbmc.log(f"[FastStrm] 错误: 未找到二进制执行文件: {self.bin_path}", xbmc.LOGERROR)
            xbmcgui.Dialog().notification("FastStrm 启动错误", "未找到适配该架构的二进制文件", xbmcgui.NOTIFICATION_ERROR, 5000)
            return

        try:
            st = os.stat(self.bin_path)
            os.chmod(self.bin_path, st.st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)
        except Exception as e:
            xbmc.log(f"[FastStrm] 设置执行权限警告: {str(e)}", xbmc.LOGWARNING)

        env = os.environ.copy()
        env["CONFIG_DIR"] = config_dir
        env["DATA_DIR"] = data_dir
        env["SERVER_PORT"] = str(port)
        env["SERVER_HOST"] = "0.0.0.0"
        env["APP_ENV"] = "prod"

        log_path = os.path.join(data_dir, "faststrm.log")
        try:
            self.log_file = open(log_path, "a", encoding="utf-8", buffering=1)
        except Exception:
            self.log_file = None

        cmd = [self.bin_path, "--no-tray", "--config", config_dir]
        xbmc.log(f"[FastStrm] 正在启动守护进程: {' '.join(cmd)}, port={port}", xbmc.LOGINFO)

        try:
            out_dest = self.log_file if self.log_file else subprocess.DEVNULL
            self.process = subprocess.Popen(
                cmd,
                cwd=data_dir,
                env=env,
                stdout=out_dest,
                stderr=out_dest
            )
            xbmc.log(f"[FastStrm] 守护进程启动成功 (PID: {self.process.pid})", xbmc.LOGINFO)

            if self.addon.getSettingBool("notify_startup"):
                ip = get_local_ip()
                icon = os.path.join(self.addon_dir, "icon.png")
                xbmcgui.Dialog().notification(
                    "FastStrm 服务已就绪",
                    f"浏览器访问: http://{ip}:{port}",
                    icon if os.path.exists(icon) else xbmcgui.NOTIFICATION_INFO,
                    6000
                )
        except Exception as e:
            xbmc.log(f"[FastStrm] 守护进程启动异常: {str(e)}", xbmc.LOGERROR)

    def stop_process(self):
        if self.process:
            xbmc.log(f"[FastStrm] 正在停止进程 (PID: {self.process.pid})...", xbmc.LOGINFO)
            try:
                self.process.terminate()
                self.process.wait(timeout=5)
            except Exception:
                try:
                    self.process.kill()
                except Exception:
                    pass
            self.process = None

        if self.log_file and not self.log_file.closed:
            try:
                self.log_file.close()
            except Exception:
                pass
            self.log_file = None

    def run(self):
        self.start_process()
        while not self.abortRequested():
            if self.waitForAbort(2):
                break
            if self.process and self.process.poll() is not None:
                exit_code = self.process.returncode
                xbmc.log(f"[FastStrm] 进程异常退出 (exit code: {exit_code})，3秒后尝试自动重启...", xbmc.LOGWARNING)
                time.sleep(3)
                if not self.abortRequested():
                    self.start_process()
        self.stop_process()

if __name__ == "__main__":
    service = FastStrmService()
    service.run()
EOF_SERVICE

# 生成 default.py (用户点击插件菜单时的交互界面)
cat <<'EOF_DEFAULT' > "${ADDON_DIR}/default.py"
# -*- coding: utf-8 -*-
import os
import socket
import xbmc
import xbmcgui
import xbmcaddon
import xbmcvfs

ADDON_ID = "service.faststrm"

def get_local_ip():
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        s.connect(("119.29.29.29", 80))
        ip = s.getsockname()[0]
    except Exception:
        ip = "127.0.0.1"
    finally:
        s.close()
    return ip

def check_port_listening(port):
    sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    sock.settimeout(1)
    result = sock.connect_ex(("127.0.0.1", port))
    sock.close()
    return result == 0

def main():
    addon = xbmcaddon.Addon(ADDON_ID)
    port_str = addon.getSetting("server_port").strip()
    try:
        port = int(port_str)
    except Exception:
        port = 8090

    ip = get_local_ip()
    web_url = f"http://{ip}:{port}"
    is_running = check_port_listening(port)
    status_text = "🟢 正在运行中" if is_running else "🔴 未运行或启动中"

    options = [
        f"🌐 Web 管理地址: {web_url}",
        "⚙️ 打开插件设置",
        "📄 查看运行日志 (faststrm.log)",
        "🔄 重启后台服务"
    ]

    dialog = xbmcgui.Dialog()
    choice = dialog.select(f"FastStrm 服务管理 [{status_text}]", options)

    profile_dir = xbmcvfs.translatePath(addon.getAddonInfo("profile"))
    custom_data_dir = addon.getSetting("data_dir").strip()
    data_dir = xbmcvfs.translatePath(custom_data_dir) if custom_data_dir else os.path.join(profile_dir, "data")
    log_path = os.path.join(data_dir, "faststrm.log")

    if choice == 0:
        dialog.ok(
            "FastStrm 管理控制台",
            f"请在局域网内任意手机或电脑浏览器中打开：\n\n{web_url}\n\n"
            f"默认管理账号: admin\n默认管理密码: admin\n\n"
            f"登录后即可扫码绑定 115 账号并配置同步规则。"
        )
    elif choice == 1:
        addon.openSettings()
    elif choice == 2:
        if os.path.exists(log_path):
            try:
                with open(log_path, "r", encoding="utf-8", errors="ignore") as f:
                    lines = f.readlines()
                    last_lines = "".join(lines[-40:])
                dialog.textviewer("FastStrm 运行日志 (最新40行)", last_lines if last_lines else "日志文件为空")
            except Exception as e:
                dialog.notification("读取日志失败", str(e), xbmcgui.NOTIFICATION_ERROR)
        else:
            dialog.notification("提示", "日志文件尚未生成", xbmcgui.NOTIFICATION_INFO)
    elif choice == 3:
        addon.setSetting("notify_startup", addon.getSetting("notify_startup"))
        dialog.notification("FastStrm", "已发送服务重启指令", xbmcgui.NOTIFICATION_INFO)

if __name__ == "__main__":
    main()
EOF_DEFAULT

chmod +x "${ADDON_DIR}/service.py"
chmod +x "${ADDON_DIR}/default.py"

# 4. 打包为 ZIP
echo "[4/4] 正在打包 Kodi 插件 ZIP 安装包..."
ZIP_NAME="${ADDON_ID}-${KODI_VERSION}-${ZIP_ARCH_TAG}.zip"
ZIP_PATH="${DIST_DIR}/${ZIP_NAME}"

cd "${BUILD_DIR}"
rm -f "${ZIP_PATH}"
zip -r -q "${ZIP_PATH}" "${ADDON_ID}"

# 5. 生成本地 Kodi 插件库结构与索引 (可选供 Web 服务器直接托管)
echo "[5/5] 生成 Kodi 插件库结构 (dist/zips/) 与索引..."
REPO_STAGING="${DIST_DIR}/zips/${ADDON_ID}"
mkdir -p "${REPO_STAGING}"
cp -f "${ZIP_PATH}" "${REPO_STAGING}/${ADDON_ID}-${KODI_VERSION}.zip"
cp -f "${ADDON_DIR}/addon.xml" "${REPO_STAGING}/addon.xml"
if [ -f "${ADDON_DIR}/icon.png" ]; then
    cp -f "${ADDON_DIR}/icon.png" "${REPO_STAGING}/icon.png"
fi

# 执行 build_repo.py 生成 addons.xml 与 md5
if [ -f "${ROOT_DIR}/scripts/build_repo.py" ]; then
    python3 "${ROOT_DIR}/scripts/build_repo.py" "${DIST_DIR}"
fi

echo "=================================================="
echo " 全部构建完成！"
echo " 单插件 ZIP: ${ZIP_PATH}"
echo " 插件库目录: ${DIST_DIR}/zips/ (包含 addons.xml 及 md5)"
echo "=================================================="