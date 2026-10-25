//go:build !nogui

package gui

// A station's own page as a tab, rather than a jump to the browser. Each
// station is a magpie of its own on its own port, so its page must be
// loaded top level: its sign-in cookie is set for that host:port, and a
// frame inside this window (wails://) would be another site, whose Lax
// cookie a browser does not send. A window of its own keeps the cookie
// first party and leaves the station's page exactly as it serves it.
//
// The stations share one window of tabs, apart from the main window. The
// main window's content runs under its title bar (MacTitleBarHiddenInset),
// so the system's tab row would lie over its header; a station window has
// the ordinary title bar, and the tab row goes under it, above the page.

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>
#include <stdlib.h>

// joinTabs puts win in the tab group of the station window in front, after
// its last tab, and selects it, as a browser's new tab goes into the window
// in use. With none in front (none open, or all minimized), win starts a
// window of its own. orderedWindows is front to back, and of a group only
// the selected tab is visible, so the first visible one with ident is the
// front group's.
static void joinTabs(void *win, const char *ident) {
	NSWindow *t = (NSWindow *)win;
	if (t == nil) return;
	NSString *id = [NSString stringWithUTF8String:ident];
	NSWindow *front = nil;
	for (NSWindow *w in NSApp.orderedWindows) {
		if (w != t && w.isVisible && !w.isMiniaturized &&
		    [w.tabbingIdentifier isEqualToString:id]) {
			front = w;
			break;
		}
	}
	t.tabbingIdentifier = id;
	t.tabbingMode = NSWindowTabbingModePreferred;
	if (front != nil) {
		NSWindow *last = front.tabbedWindows.lastObject ?: front;
		[last addTabbedWindow:t ordered:NSWindowAbove];
	}
	[t makeKeyAndOrderFront:nil];
}

// selectTab brings an open tab to the front without making another.
static void selectTab(void *tabWin) {
	NSWindow *t = (NSWindow *)tabWin;
	if (t == nil) return;
	[t makeKeyAndOrderFront:nil];
}
*/
import "C"

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// stationTabGroup is the identifier every station tab shares, so they are
// tabs of one window.
const stationTabGroup = "ai.usemagpie.stations"

var (
	// stationOpenMu takes one Open at a time, so two clicks on one station
	// make one tab. Nothing an event runs takes it: it is held across calls
	// onto the main thread, where those events are delivered.
	stationOpenMu sync.Mutex

	stationTabsMu sync.Mutex
	// stationTabs is the window open for each station, by name, so a second
	// Open selects the tab instead of making another.
	stationTabs = map[string]*application.WebviewWindow{}
)

// stationMacWindow is a station window's look: the ordinary title bar, so
// the tab row has its own place between it and the page.
func stationMacWindow() application.MacWindow {
	return application.MacWindow{
		TitleBar: application.MacTitleBarDefault,
		CollectionBehavior: application.MacWindowCollectionBehaviorMoveToActiveSpace |
			application.MacWindowCollectionBehaviorFullScreenPrimary,
	}
}

// openStationTab loads addr, a station's own page, as a tab of the
// stations' window, making that window if none is open. The station's name
// titles the tab.
//
// It answers an error the page shows, and ErrNoStationTabs when there is no
// app to open a window in, which tells the caller to open the browser
// instead rather than leave the click with nothing at all.
func (h *host) openStationTab(name, addr string) error {
	u, err := url.Parse(addr)
	if err != nil {
		return fmt.Errorf("that station's address is not a URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("a station's page is served over http")
	}
	if strings.TrimSpace(name) == "" {
		return errors.New("a tab needs the station's name")
	}
	if h.app == nil {
		return ErrNoStationTabs
	}

	stationOpenMu.Lock()
	defer stationOpenMu.Unlock()

	stationTabsMu.Lock()
	open := stationTabs[name]
	stationTabsMu.Unlock()

	if open != nil {
		// AppKit answers only on the main thread; a route's goroutine is
		// not it (InvokeSync, as every other window call here does).
		selected := application.InvokeSyncWithResult(func() bool {
			p := open.NativeWindow()
			if p == nil {
				return false
			}
			C.selectTab(p)
			return true
		})
		if selected {
			return nil
		}
		// closed since: forget it and make another below
		stationTabsMu.Lock()
		delete(stationTabs, name)
		stationTabsMu.Unlock()
	}

	// the first tab opens at the main window's size, no smaller than a
	// page's least at this text size (windowMin, as makeMain does)
	z := h.zoom()
	minW, minH := windowMin(z, 0, 0)
	wd, ht := minW, minH
	if h.main != nil {
		size := application.InvokeSyncWithResult(func() [2]int {
			a, b := h.main.Size()
			return [2]int{a, b}
		})
		wd, ht = max(size[0], minW), max(size[1], minH)
	}
	win := h.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "station:" + name,
		Title:     name,
		URL:       addr,
		Width:     wd,
		Height:    ht,
		MinWidth:  minW,
		MinHeight: minH,
		Zoom:      z,
		Hidden:    true,
		Mac:       stationMacWindow(),
	})

	stationTabsMu.Lock()
	stationTabs[name] = win
	stationTabsMu.Unlock()

	// A tab the reader closes is forgotten, so Open makes a new one.
	win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		stationTabsMu.Lock()
		if stationTabs[name] == win {
			delete(stationTabs, name)
		}
		stationTabsMu.Unlock()
	})

	// Show makes the native window on the main thread before it returns;
	// it is joined to the tabs there and then, rather than on an event:
	// Mac.WindowShow comes only when a window stops being covered, which a
	// tab opened behind another app's window never does.
	win.Show()
	joined := application.InvokeSyncWithResult(func() bool {
		p := win.NativeWindow()
		if p == nil {
			return false
		}
		ident := C.CString(stationTabGroup)
		defer C.free(unsafe.Pointer(ident))
		C.joinTabs(p, ident)
		return true
	})
	if !joined {
		return errors.New("the station's window did not open")
	}
	return nil
}
