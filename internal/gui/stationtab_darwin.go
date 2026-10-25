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
//
// What a browser tab does that Wails' webview doesn't, the tab's own
// delegate (MPStationTabDelegate) does: a window.open, or a link to another
// site, opens in the browser, as the page's `magpie web` code expects of a
// tab, and a download is saved to Downloads. Without it both did nothing.

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <objc/runtime.h>
#include <stdlib.h>

extern void mpStationOpenURL(char *url);
extern void mpStationDownloaded(char *path, char *err);

// openOutside hands u to the window's opener (stationOpenURL).
static void openOutside(NSURL *u) {
	NSString *s = u.absoluteString;
	if (s.length == 0 || [s isEqualToString:@"about:blank"]) return;
	mpStationOpenURL((char *)s.UTF8String);
}

static NSInteger portOf(NSURL *u) {
	if (u.port != nil) return u.port.integerValue;
	return [u.scheme caseInsensitiveCompare:@"https"] == NSOrderedSame ? 443 : 80;
}

// sameOrigin: scheme, host and port all equal. A nil host is no origin
// (messaging nil answers NSOrderedSame).
static BOOL sameOrigin(NSURL *a, NSURL *b) {
	if (a.host == nil || b.host == nil) return NO;
	return [a.scheme caseInsensitiveCompare:b.scheme] == NSOrderedSame &&
		[a.host caseInsensitiveCompare:b.host] == NSOrderedSame && portOf(a) == portOf(b);
}
// downloadsDir is where a tab saves what it downloads: Downloads, or the
// folder a test names (setDownloadsDir).
static NSString *downloadsOverride = nil;
static void setDownloadsDir(const char *dir) {
	[downloadsOverride release];
	downloadsOverride = dir ? [[NSString alloc] initWithUTF8String:dir] : nil;
}
static NSURL *downloadsDir(void) {
	if (downloadsOverride.length) return [NSURL fileURLWithPath:downloadsOverride isDirectory:YES];
	return [[NSFileManager defaultManager] URLsForDirectory:NSDownloadsDirectory inDomains:NSUserDomainMask].firstObject;
}

// freeName is name in dir, or "name (1).ext" and on when that is taken, as
// a browser saves a second copy. The page's name keeps no folder part.
static NSURL *freeName(NSURL *dir, NSString *name) {
	name = [[name lastPathComponent] stringByReplacingOccurrencesOfString:@":" withString:@"-"];
	if (name.length == 0 || [name hasPrefix:@"."]) name = [@"download" stringByAppendingString:name];
	NSFileManager *fm = [NSFileManager defaultManager];
	NSURL *u = [dir URLByAppendingPathComponent:name];
	NSString *stem = [name stringByDeletingPathExtension], *ext = [name pathExtension];
	for (int i = 1; [fm fileExistsAtPath:u.path] && i < 10000; i++) {
		NSString *n = [NSString stringWithFormat:@"%@ (%d)", stem, i];
		if (ext.length) n = [n stringByAppendingPathExtension:ext];
		u = [dir URLByAppendingPathComponent:n];
	}
	return u;
}

static const void *kTabDelegate = &kTabDelegate;
static const void *kDownloadTo = &kDownloadTo;
static const void *kDownloadOwner = &kDownloadOwner;

// MPStationTabDelegate sits in front of Wails' own delegate, which it hands
// everything it doesn't answer itself; Wails answers no navigation policy,
// no new window and no download, which is what this one adds.
@interface MPStationTabDelegate : NSObject <WKNavigationDelegate, WKUIDelegate, WKDownloadDelegate>
@property (nonatomic, assign) id inner; // Wails' delegate, which outlives the tab
@end

@implementation MPStationTabDelegate
- (BOOL)respondsToSelector:(SEL)sel {
	return [super respondsToSelector:sel] || [self.inner respondsToSelector:sel];
}
- (id)forwardingTargetForSelector:(SEL)sel {
	return [self.inner respondsToSelector:sel] ? self.inner : nil;
}
// A link the tab can't show (mailto:, an app's own scheme), or one clicked
// to another site in the tab itself, goes to the browser: the tab has no
// address bar or back button to come back to the station by.
- (void)webView:(WKWebView *)w decidePolicyForNavigationAction:(WKNavigationAction *)a
	decisionHandler:(void (^)(WKNavigationActionPolicy))decide {
	NSURL *u = a.request.URL;
	NSString *scheme = u.scheme.lowercaseString;
	if (a.shouldPerformDownload) { decide(WKNavigationActionPolicyDownload); return; }
	if (scheme && ![@[@"http", @"https", @"blob", @"data", @"about"] containsObject:scheme]) {
		openOutside(u);
		decide(WKNavigationActionPolicyCancel);
		return;
	}
	if (a.navigationType == WKNavigationTypeLinkActivated && a.targetFrame.isMainFrame &&
		w.URL != nil && !sameOrigin(w.URL, u)) {
		openOutside(u);
		decide(WKNavigationActionPolicyCancel);
		return;
	}
	decide(WKNavigationActionPolicyAllow);
}

// A reply sent as an attachment, or one WebKit can't show, is a download.
- (void)webView:(WKWebView *)w decidePolicyForNavigationResponse:(WKNavigationResponse *)r
	decisionHandler:(void (^)(WKNavigationResponsePolicy))decide {
	BOOL attachment = NO;
	if ([r.response isKindOfClass:[NSHTTPURLResponse class]]) {
		NSString *cd = [(NSHTTPURLResponse *)r.response valueForHTTPHeaderField:@"Content-Disposition"];
		attachment = [cd.lowercaseString hasPrefix:@"attachment"];
	}
	decide(attachment || !r.canShowMIMEType ? WKNavigationResponsePolicyDownload : WKNavigationResponsePolicyAllow);
}

- (void)webView:(WKWebView *)w navigationAction:(WKNavigationAction *)a didBecomeDownload:(WKDownload *)d {
	d.delegate = self;
	objc_setAssociatedObject(d, kDownloadOwner, self, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
}
- (void)webView:(WKWebView *)w navigationResponse:(WKNavigationResponse *)r didBecomeDownload:(WKDownload *)d {
	d.delegate = self;
	objc_setAssociatedObject(d, kDownloadOwner, self, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
}
- (void)download:(WKDownload *)d decideDestinationUsingResponse:(NSURLResponse *)resp
	suggestedFilename:(NSString *)name completionHandler:(void (^)(NSURL *))done {
	NSURL *dir = downloadsDir();
	NSError *err = nil;
	if (dir == nil || ![[NSFileManager defaultManager] createDirectoryAtURL:dir
			withIntermediateDirectories:YES attributes:nil error:&err]) {
		mpStationDownloaded("", (char *)(err.localizedDescription ?: @"there is no Downloads folder").UTF8String);
		done(nil); // nil cancels the download
		return;
	}
	NSURL *to = freeName(dir, name);
	objc_setAssociatedObject(d, kDownloadTo, to, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
	done(to);
}
- (void)downloadDidFinish:(WKDownload *)d {
	NSURL *to = objc_getAssociatedObject(d, kDownloadTo);
	// the Downloads stack in the Dock bounces, as it does for a browser's
	[[NSDistributedNotificationCenter defaultCenter]
		postNotificationName:@"com.apple.DownloadFileFinished" object:to.path];
	mpStationDownloaded((char *)(to.path ?: @"").UTF8String, "");
}
- (void)download:(WKDownload *)d didFailWithError:(NSError *)err resumeData:(NSData *)resume {
	NSURL *to = objc_getAssociatedObject(d, kDownloadTo);
	mpStationDownloaded((char *)(to.path ?: @"").UTF8String, (char *)err.localizedDescription.UTF8String);
}

// window.open and target=_blank: the browser, as `magpie web`'s own code
// opens them there. Answering nil makes no window in this app.
- (WKWebView *)webView:(WKWebView *)w createWebViewWithConfiguration:(WKWebViewConfiguration *)c
	forNavigationAction:(WKNavigationAction *)a windowFeatures:(WKWindowFeatures *)f {
	openOutside(a.request.URL);
	return nil;
}
@end

// findWebView is the WKWebView in v, Wails' own subclass included.
static WKWebView *findWebView(NSView *v) {
	if ([v isKindOfClass:[WKWebView class]]) return (WKWebView *)v;
	for (NSView *s in v.subviews) {
		WKWebView *w = findWebView(s);
		if (w != nil) return w;
	}
	return nil;
}

// installTabDelegate puts the tab's delegate in front of Wails' on the
// window's webview, once; the webview keeps it. 0 when there is no webview.
static int installTabDelegate(void *win) {
	NSWindow *t = (NSWindow *)win;
	WKWebView *w = findWebView(t.contentView);
	if (w == nil) return 0;
	if (objc_getAssociatedObject(w, kTabDelegate) != nil) return 1;
	MPStationTabDelegate *d = [[MPStationTabDelegate alloc] init];
	d.inner = (id)w.navigationDelegate;
	objc_setAssociatedObject(w, kTabDelegate, d, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
	w.navigationDelegate = d;
	w.UIDelegate = d;
	[d release];
	return 1;
}
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
	"log"
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

// openOutsideWith makes the window's own opener where a tab's links go,
// unless something (a test) already set one. On the main thread, as every
// tab's delegate reads it there.
func (h *host) openOutsideWith() {
	if stationOpenURL != nil {
		return
	}
	stationOpenURL = func(u string) {
		if err := h.OpenURL(u); err != nil {
			// the link may carry a sign-in code: say where, not what
			p, _ := url.Parse(u)
			where := "a link"
			if p != nil {
				where = p.Scheme + "://" + p.Host
			}
			log.Printf("station tab: can't open %s in the browser: %v", where, err)
		}
	}
}

// setStationDownloadsDir is where tabs save downloads from now on, ""
// for Downloads. A test's own folder; nothing else sets it.
func setStationDownloadsDir(dir string) {
	application.InvokeSync(func() {
		if dir == "" {
			C.setDownloadsDir(nil)
			return
		}
		c := C.CString(dir)
		defer C.free(unsafe.Pointer(c))
		C.setDownloadsDir(c)
	})
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
	// tab opened behind another app's window never does. The tab's
	// delegate goes on its webview in the same step, before the page can
	// ask it for anything but the first load.
	win.Show()
	joined, delegated := false, false
	application.InvokeSync(func() {
		p := win.NativeWindow()
		if p == nil {
			return
		}
		h.openOutsideWith()
		delegated = C.installTabDelegate(p) != 0
		ident := C.CString(stationTabGroup)
		defer C.free(unsafe.Pointer(ident))
		C.joinTabs(p, ident)
		joined = true
	})
	if !joined {
		return errors.New("the station's window did not open")
	}
	if !delegated {
		// the page still works; its links and downloads don't
		log.Printf("station tab %s: no webview to hand links and downloads", name)
	}
	return nil
}
