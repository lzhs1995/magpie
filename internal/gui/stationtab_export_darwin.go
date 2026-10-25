//go:build darwin && cgo && !nogui

package gui

import "C"

// The exports live apart from stationtab_darwin.go because a cgo preamble
// that defines functions can't sit beside an //export.

// stationOpenURL takes a link a station's page sends out of its tab: a
// window.open, a link to another site, a mailto:. The window's own opener
// (host.OpenURL) is set when the first tab opens; a test sets its own.
var stationOpenURL func(string)

// stationDownloaded hears where a tab's download was saved, or why it
// failed; a test sets it. "" for err is saved.
var stationDownloaded func(path, err string)

//export mpStationOpenURL
func mpStationOpenURL(u *C.char) {
	s := C.GoString(u) // copied before WebKit's string goes
	if open := stationOpenURL; open != nil {
		// off the main thread: the opener may wait on it (InvokeSync)
		go open(s)
	}
}

//export mpStationDownloaded
func mpStationDownloaded(path, errText *C.char) {
	p, e := C.GoString(path), C.GoString(errText)
	if done := stationDownloaded; done != nil {
		go done(p, e)
	}
}
