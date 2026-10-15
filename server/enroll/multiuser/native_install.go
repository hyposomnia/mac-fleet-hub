package multiuser

import (
	_ "embed"
	"net/http"
	"strings"
)

//go:embed native_install.sh
var nativeInstallScript string

func (server *Server) handleNativeInstall(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", "GET")
		reject(writer, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	if !strings.HasPrefix(server.options.Origin, "https://") {
		reject(writer, http.StatusServiceUnavailable, "安装入口需要 HTTPS 服务地址")
		return
	}
	origin := "'" + strings.ReplaceAll(server.options.Origin, "'", "'\"'\"'") + "'"
	writer.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	writer.Write([]byte(strings.Replace(nativeInstallScript, "__FLEET_INSTALL_ORIGIN__", origin, 1)))
}
