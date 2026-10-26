package pluginrt

import (
	"net/http"
	"strings"
	"sync"
)

// A plugin's own API. Contribute puts a part in the window and Provide
// gives a service; Handle is the third kind of thing a plugin registers,
// an HTTP route of its own, so a plugin with a page of its own needs no
// room in the core's handler. Its routes are mounted under
// /api/plugins/x/<id>/ and are gone once the plugin is turned off, as
// everything else registered through Ctx is.
//
// The dispatcher looks the route up per request rather than at mount
// time: a plugin is turned on and off while magpie runs (Reconcile), and
// a mux already built can't have a pattern taken out of it again.

// routesMu guards the mounted routes of every plugin.
var routesMu sync.RWMutex

// routeTable is a Runtime's plugin routes, under RoutePrefix: mux is the
// matcher, routed the handler each pattern has now, and mounted the
// patterns the matcher already holds, since a ServeMux takes a pattern
// once and never gives it back. Kept here, beside the code that uses it,
// so the Runtime itself carries one field for it.
type routeTable struct {
	mux     *http.ServeMux
	routed  map[string]http.Handler
	mounted map[string]bool
}

// Handle gives the plugin a route of its own, below its own prefix: the
// pattern is `[METHOD ]path`, where path is relative to the plugin's
// prefix, so "GET stations" of the plugin `stations` is
// `GET /api/plugins/x/stations/stations`. A pattern registered twice, or
// an empty one, is a programming error and panics at Apply, where the
// plugin fails and the others go on.
func (c *Ctx) Handle(pattern string, h http.Handler) {
	method, path := splitPattern(pattern)
	if path == "" {
		panic("pluginrt: " + c.ID + ": a route needs a path")
	}
	full := method + RoutePrefix + c.ID + "/" + path
	routesMu.Lock()
	t := &c.rt.routes
	if t.mux == nil {
		t.mux = http.NewServeMux()
		t.routed = map[string]http.Handler{}
		t.mounted = map[string]bool{}
	}
	if _, seen := t.routed[full]; seen {
		routesMu.Unlock()
		panic("pluginrt: " + c.ID + ": the route " + full + " is registered twice")
	}
	// The mux is only the matcher: it holds a lookup that reads the
	// handler that is there now, so a disposed route stops answering
	// without the pattern having to come back out of the mux — which a
	// ServeMux can't do, and which registering it again after the plugin
	// came back on would panic over.
	t.routed[full] = h
	if !t.mounted[full] {
		t.mounted[full] = true
		t.mux.Handle(full, lookup{t: t, key: full})
	}
	key := full
	routesMu.Unlock()
	c.OnDispose(func() {
		routesMu.Lock()
		delete(t.routed, key)
		routesMu.Unlock()
	})
}

// HandleFunc is Handle of a function.
func (c *Ctx) HandleFunc(pattern string, h func(http.ResponseWriter, *http.Request)) {
	c.Handle(pattern, http.HandlerFunc(h))
}

// RoutePrefix is where a plugin's own routes are, below the core's /api.
const RoutePrefix = "/api/plugins/x/"

// splitPattern takes `[METHOD ]path` apart, keeping the method's trailing
// space so the two join straight back into a mux pattern.
func splitPattern(pattern string) (method, path string) {
	p := strings.TrimSpace(pattern)
	if i := strings.IndexByte(p, ' '); i > 0 {
		m, rest := p[:i], strings.TrimSpace(p[i+1:])
		// only an all-caps word is a method; a path never is one
		if m == strings.ToUpper(m) && !strings.HasPrefix(m, "/") {
			return m + " ", strings.TrimPrefix(rest, "/")
		}
	}
	return "", strings.TrimPrefix(p, "/")
}

// lookup answers with whatever handler is registered for key now, and 404
// once the plugin that registered it is off.
type lookup struct {
	t   *routeTable
	key string
}

func (l lookup) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	routesMu.RLock()
	h := l.t.routed[l.key]
	routesMu.RUnlock()
	if h == nil {
		http.NotFound(rw, r)
		return
	}
	h.ServeHTTP(rw, r)
}

// Routes is the handler for every active plugin's own routes, for the
// window to mount under RoutePrefix, or nil when no plugin has one. It
// stays the same handler as plugins come and go.
func (rt *Runtime) Routes() http.Handler {
	if rt == nil {
		return nil
	}
	routesMu.RLock()
	defer routesMu.RUnlock()
	if rt.routes.mux == nil {
		return nil
	}
	return rt.routes.mux
}
