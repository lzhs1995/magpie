//go:build !nogui && !darwin

package gui

// Only macOS groups windows into tabs. Elsewhere a station's page opens in
// the browser, as it did before: openStationTabIn stays nil, so the route
// answers {"tabbed": false} and the page opens the link itself.

func (h *host) openStationTab(string, string) error { return ErrNoStationTabs }
