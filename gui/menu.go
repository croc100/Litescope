package main

import (
	"path/filepath"
	"runtime"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// recentSubmenu is the File ▸ Open Recent submenu. It is rebuilt at runtime from
// the frontend's recent-files list (see App.SetRecentFiles) — the app is
// single-window, so a package-level handle is sufficient.
var recentSubmenu *menu.Menu

// buildMenu constructs the native application menu bar. Items that act on app
// state emit menu:* events the React frontend listens for, keeping the window
// chrome and the UI in sync; window-level items call the Wails runtime directly.
func (a *App) buildMenu() *menu.Menu {
	m := menu.NewMenu()

	// The macOS application menu (About, Services, Hide, Quit) — expected in
	// the leftmost, bold app slot on darwin.
	if runtime.GOOS == "darwin" {
		m.Append(menu.AppMenu())
	}

	file := m.AddSubmenu("File")
	file.AddText("Open Database…", keys.CmdOrCtrl("o"), func(_ *menu.CallbackData) {
		wailsRuntime.EventsEmit(a.ctx, "menu:open-db")
	})
	file.AddText("Open Fleet Config…", nil, func(_ *menu.CallbackData) {
		wailsRuntime.EventsEmit(a.ctx, "menu:fleet")
	})
	recentSubmenu = file.AddSubmenu("Open Recent")
	recentSubmenu.AddText("No Recent Files", nil, nil).Disabled = true
	file.AddSeparator()
	file.AddText("Settings…", keys.CmdOrCtrl(","), func(_ *menu.CallbackData) {
		wailsRuntime.EventsEmit(a.ctx, "menu:settings")
	})
	// On non-mac there is no app menu to carry Quit, so add it here.
	if runtime.GOOS != "darwin" {
		file.AddSeparator()
		file.AddText("Quit", keys.CmdOrCtrl("q"), func(_ *menu.CallbackData) {
			wailsRuntime.Quit(a.ctx)
		})
	}

	// Standard Cut/Copy/Paste/Select-All — WKWebView needs an explicit Edit
	// menu for these shortcuts to work at all on macOS. On Windows/Linux the
	// WebView provides them natively, so no Edit menu is added there.
	if runtime.GOOS == "darwin" {
		m.Append(menu.EditMenu())
	}

	view := m.AddSubmenu("View")
	view.AddText("Toggle Sidebar", keys.CmdOrCtrl("b"), func(_ *menu.CallbackData) {
		wailsRuntime.EventsEmit(a.ctx, "menu:toggle-sidebar")
	})
	view.AddText("Reload", keys.CmdOrCtrl("r"), func(_ *menu.CallbackData) {
		wailsRuntime.WindowReloadApp(a.ctx)
	})
	view.AddSeparator()
	view.AddText("Toggle Full Screen", keys.Combo("f", keys.CmdOrCtrlKey, keys.ControlKey), func(_ *menu.CallbackData) {
		if wailsRuntime.WindowIsFullscreen(a.ctx) {
			wailsRuntime.WindowUnfullscreen(a.ctx)
		} else {
			wailsRuntime.WindowFullscreen(a.ctx)
		}
	})

	// Minimize / Zoom / Front — the standard macOS Window menu.
	if runtime.GOOS == "darwin" {
		m.Append(menu.WindowMenu())
	}

	return m
}

// SetRecentFiles rebuilds the File ▸ Open Recent submenu from the frontend's
// recent-files list. Bound to the frontend (Wails) and called whenever the list
// changes, so the native menu mirrors the in-app recent connections. Each entry
// opens its path via a menu:open-path event.
func (a *App) SetRecentFiles(paths []string) {
	if recentSubmenu == nil {
		return
	}
	recentSubmenu.Items = nil
	if len(paths) == 0 {
		recentSubmenu.AddText("No Recent Files", nil, nil).Disabled = true
	} else {
		for _, p := range paths {
			p := p // capture per iteration
			recentSubmenu.AddText(filepath.Base(p), nil, func(_ *menu.CallbackData) {
				wailsRuntime.EventsEmit(a.ctx, "menu:open-path", p)
			})
		}
		recentSubmenu.AddSeparator()
		recentSubmenu.AddText("Clear Menu", nil, func(_ *menu.CallbackData) {
			wailsRuntime.EventsEmit(a.ctx, "menu:clear-recent")
		})
	}
	if a.ctx != nil {
		wailsRuntime.MenuUpdateApplicationMenu(a.ctx)
	}
}
