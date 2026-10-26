package gui

import (
	"net/http"

	"github.com/yetone/magpie/internal/pluginrt"
)

// pluginOwnRoutes serves the routes the active plugins registered
// themselves (pluginrt.Ctx.Handle), apart from the core's own /api,
// mounted once under pluginrt.RoutePrefix (builtinRoutes). One handler for
// them all, which looks each request's route up as it comes, so a plugin
// turned on or off while magpie runs needs no remounting. The runtime
// isn't there before the first Reconcile, and a route of a plugin since
// turned off is gone: both are 404, as an unknown path is.
type pluginOwnRoutes struct{}

func (pluginOwnRoutes) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	h := pluginrt.Running().Routes()
	if h == nil {
		http.NotFound(rw, r)
		return
	}
	h.ServeHTTP(rw, r)
}
