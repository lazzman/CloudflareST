package management

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed web/dist
var webFiles embed.FS

func (service *Service) serveWeb(response http.ResponseWriter, request *http.Request) {
	root, err := fs.Sub(webFiles, "web/dist")
	if err != nil {
		http.Error(response, "前端资源不可用", http.StatusServiceUnavailable)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+request.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	if _, err = fs.Stat(root, name); err != nil {
		name = "index.html"
	}
	body, err := fs.ReadFile(root, name)
	if err != nil {
		http.Error(response, "前端资源尚未构建，请执行 npm run build", http.StatusServiceUnavailable)
		return
	}
	info, err := fs.Stat(root, name)
	if err != nil {
		http.Error(response, "前端资源不可用", http.StatusServiceUnavailable)
		return
	}
	if name == "index.html" {
		response.Header().Set("Cache-Control", "no-cache")
	} else {
		response.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(response, request, name, info.ModTime(), bytes.NewReader(body))
}
