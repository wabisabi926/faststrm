package strm

import (
	"path"
	"strings"
)

// mediaContentTypes 已知媒体容器的 Content-Type。
// 用于 HEAD probe 与代理流兜底：115 CDN 对部分原盘/容器返回
// application/octet-stream，播放器拿不到容器类型时会误判。
var mediaContentTypes = map[string]string{
	".iso":  "application/octet-stream",
	".m2ts": "video/mp2t",
	".mts":  "video/mp2t",
	".ts":   "video/mp2t",
	".mkv":  "video/x-matroska",
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".mov":  "video/quicktime",
	".avi":  "video/x-msvideo",
	".wmv":  "video/x-ms-wmv",
	".flv":  "video/x-flv",
	".webm": "video/webm",
	".mpg":  "video/mpeg",
	".mpeg": "video/mpeg",
	".vob":  "video/dvd",
	".rm":   "application/vnd.rn-realmedia",
	".rmvb": "application/vnd.rn-realmedia",
	".3gp":  "video/3gpp",
	".ogv":  "video/ogg",
	".mxf":  "application/mxf",
}

// ContentTypeForName 按文件名扩展名返回媒体 Content-Type；未知返回空串。
func ContentTypeForName(name string) string {
	if name == "" {
		return ""
	}
	ext := strings.ToLower(path.Ext(strings.TrimSpace(name)))
	return mediaContentTypes[ext]
}
