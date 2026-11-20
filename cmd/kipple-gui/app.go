//go:build gui

package main

import (
	"image/color"
	"os/exec"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/rbutera/kipple/internal/guimodel"
)

// ui is the window and the model behind it. Every model change happens
// on the main goroutine; jobs run on others and hand back through do.
type ui struct {
	app fyne.App
	win fyne.Window
	th  *kippleTheme
	m   *guimodel.Model

	// do runs fn on the main goroutine (fyne.Do).
	do func(fn func())
	// open runs /usr/bin/open with args (Finder, the Trash, settings).
	open func(args ...string)
	// quit ends the app.
	quit func()

	list      *widget.List
	listRows  int
	sheet     fyne.CanvasObject
	sheetKind guimodel.Sheet
	last      map[string]time.Time
	menu      *fyne.MainMenu
}

func newUI(a fyne.App, w fyne.Window, th *kippleTheme, m *guimodel.Model) *ui {
	u := &ui{app: a, win: w, th: th, m: m, do: fyne.Do, last: map[string]time.Time{}}
	u.open = func(args ...string) {
		cmd := exec.Command("/usr/bin/open", args...) // never a shell
		if err := cmd.Start(); err == nil {
			go func() { _ = cmd.Wait() }()
		}
	}
	u.quit = a.Quit
	w.Canvas().SetOnTypedKey(u.typedKey)
	u.shortcuts()
	return u
}

// update redraws the window for the model's state.
func (u *ui) update() {
	u.win.SetContent(u.root())
	// Wrapped text knows its height only once it has a width, so lay the
	// window out a second time with those heights.
	u.win.Content().Refresh()
	u.syncSheet()
	if u.menu != nil {
		u.refreshMenu()
	}
	if u.m.QuitNow() {
		u.quit()
	}
}

// root is header, body and action bar, at least 760x520.
func (u *ui) root() fyne.CanvasObject {
	body := u.body()
	top := container.NewVBox(u.header(), hline())
	bottom := container.NewVBox(hline(), u.actionBar())
	minimum := canvas.NewRectangle(color.Transparent)
	minimum.SetMinSize(fyne.NewSize(760, 520))
	return container.NewStack(minimum, fill(container.NewBorder(top, bottom, nil, nil, body), cGround))
}

func (u *ui) body() fyne.CanvasObject {
	switch u.m.Screen() {
	case guimodel.ScreenScanning:
		return u.scanning()
	case guimodel.ScreenReview:
		return u.review()
	case guimodel.ScreenDone:
		return u.done()
	case guimodel.ScreenProblem:
		return u.problem()
	}
	return u.start()
}

// ---- jobs ----

func (u *ui) scan() {
	job, ok := u.m.StartScan()
	u.runScan(job, ok)
}

func (u *ui) rescan() {
	job, ok := u.m.RequestRescan()
	u.runScan(job, ok)
}

func (u *ui) runScan(job guimodel.ScanJob, ok bool) {
	u.update()
	if !ok {
		return
	}
	go func() {
		p, err := job.Run(func(msg string) {
			u.do(func() { u.m.ScanProgress(job, msg); u.update() })
		})
		u.do(func() { u.m.ScanDone(job, p, err); u.update() })
	}()
}

// moveToTrash is ⌘⌫ and the Move buttons: it opens the confirmation from
// the review screen, and moves from the confirmation.
func (u *ui) moveToTrash() {
	if !u.debounce("move") {
		return
	}
	switch u.m.Sheet() {
	case guimodel.SheetNone:
		u.m.OpenConfirm()
		u.update()
	case guimodel.SheetConfirm:
		u.apply()
	}
}

func (u *ui) apply() {
	job, ok := u.m.StartApply()
	u.update()
	if !ok {
		return
	}
	go func() {
		res, err := job.Run(func(msg string) {
			u.do(func() { u.m.ApplyProgress(msg); u.update() })
		})
		u.do(func() { u.m.ApplyDone(res, err); u.update() })
	}()
}

func (u *ui) putBack() {
	job, ok := u.m.StartPutBack()
	u.update()
	if !ok {
		return
	}
	go func() {
		res, err := job.Run()
		u.do(func() { u.m.PutBackDone(res, err); u.update() })
	}()
}

// debounce ignores a repeat of the same action within 250ms, so one key
// press that reaches both a menu and the canvas never acts twice.
func (u *ui) debounce(key string) bool {
	now := time.Now()
	if t, ok := u.last[key]; ok && now.Sub(t) < 250*time.Millisecond {
		return false
	}
	u.last[key] = now
	return true
}

// escape is Esc and ⌘.: stop a scan, cancel a sheet, stop a move.
func (u *ui) escape() {
	switch u.m.Sheet() {
	case guimodel.SheetConfirm:
		u.m.CancelConfirm()
	case guimodel.SheetApplying:
		u.m.StopApply()
	case guimodel.SheetPutBack:
		u.m.CancelPutBack()
	case guimodel.SheetNone:
		if u.m.Screen() == guimodel.ScreenScanning {
			u.m.StopScan()
		}
	}
	u.update()
}

func (u *ui) requestQuit() {
	if u.m.RequestQuit() {
		u.quit()
		return
	}
	u.update()
}

// ---- keys ----

func (u *ui) typedKey(ev *fyne.KeyEvent) {
	m := u.m
	switch ev.Name {
	case fyne.KeyEscape:
		u.escape()
		return
	case fyne.KeyReturn, fyne.KeyEnter:
		if m.Screen() == guimodel.ScreenStart && m.Sheet() == guimodel.SheetNone {
			u.scan()
		}
		return
	}
	if m.Screen() != guimodel.ScreenReview || m.Sheet() != guimodel.SheetNone {
		return
	}
	switch ev.Name {
	case fyne.KeyUp:
		m.MoveCursor(-1)
	case fyne.KeyDown:
		m.MoveCursor(1)
	case fyne.KeyPageUp:
		m.MoveCursor(-u.page())
	case fyne.KeyPageDown:
		m.MoveCursor(u.page())
	case fyne.KeyHome:
		m.CursorHome()
	case fyne.KeyEnd:
		m.CursorEnd()
	case fyne.KeySpace:
		m.Toggle()
	default:
		return
	}
	u.update()
	u.scrollToCursor()
}

// page is how many item rows fit in the list.
func (u *ui) page() int {
	if u.list == nil {
		return 10
	}
	n := int(u.list.Size().Height / sz(32))
	if n < 1 {
		n = 1
	}
	return n
}

func (u *ui) scrollToCursor() {
	if u.list != nil && u.m.Cursor() >= 0 {
		u.list.ScrollTo(u.m.Cursor())
	}
}

func (u *ui) shortcuts() {
	c := u.win.Canvas()
	add := func(k fyne.KeyName, mod fyne.KeyModifier, fn func()) {
		c.AddShortcut(&desktop.CustomShortcut{KeyName: k, Modifier: mod}, func(fyne.Shortcut) { fn() })
	}
	alt, cmd := fyne.KeyModifierAlt, fyne.KeyModifierSuper
	review := func(fn func()) func() {
		return func() {
			if u.m.Screen() == guimodel.ScreenReview && u.m.Sheet() == guimodel.SheetNone {
				fn()
				u.update()
				u.scrollToCursor()
			}
		}
	}
	add(fyne.KeyUp, alt, review(func() { u.m.JumpGroup(-1) }))
	add(fyne.KeyDown, alt, review(func() { u.m.JumpGroup(1) }))
	add(fyne.KeySpace, alt, review(u.m.ToggleCursorGroup))
	add(fyne.KeyPeriod, cmd, u.escape)
	// The menu carries these on macOS; the canvas copies serve windows
	// without a native menu. debounce keeps a key from acting twice.
	add(fyne.KeyBackspace, cmd, u.moveToTrash)
	add(fyne.KeyR, cmd, func() {
		if u.debounce("scan") {
			u.rescan()
		}
	})
	c.AddShortcut(&fyne.ShortcutCopy{}, func(fyne.Shortcut) {
		if u.debounce("copy") {
			u.copyPath()
		}
	})
	add(fyne.KeyC, cmd|fyne.KeyModifierShift, func() {
		if u.debounce("copyid") {
			u.copyID()
		}
	})
}

// ---- actions ----

func (u *ui) copyText(s string) {
	u.app.Clipboard().SetContent(s)
	u.m.SetHint("Copied.")
	u.update()
}

func (u *ui) copyPath() {
	if p, ok := u.m.CursorPath(); ok && u.m.Screen() == guimodel.ScreenReview {
		u.copyText(p)
	}
}

func (u *ui) copyID() {
	if id, ok := u.m.CursorID(); ok && u.m.Screen() == guimodel.ScreenReview {
		u.copyText(id)
	}
}

func (u *ui) card() *guimodel.ReceiptCard {
	switch u.m.Screen() {
	case guimodel.ScreenDone:
		c := u.m.Done().Card
		return &c
	case guimodel.ScreenProblem:
		return u.m.Problem().Card
	}
	return nil
}

// openTrash opens the Trash folder the receipt names (a KIPPLE_HOME run
// opens its own test Trash, never the real one).
func (u *ui) openTrash() {
	if c := u.card(); c != nil && c.TrashDir != "" {
		u.open(c.TrashDir)
	}
}

// openReceipt reveals the receipt file in Finder.
func (u *ui) openReceipt() {
	if c := u.card(); c != nil && c.Path != "" {
		u.open("-R", c.Path)
	}
}

func (u *ui) openPrivacy() {
	u.open("x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles")
}

func (u *ui) zoom(z float32) {
	if z == 0 {
		u.th.setZoom(1)
	} else {
		u.th.setZoom(u.th.zoom * z)
	}
	u.app.Settings().SetTheme(u.th)
	u.update()
}

// ---- menus ----

func (u *ui) menus() *fyne.MainMenu {
	item := func(label string, k fyne.KeyName, mod fyne.KeyModifier, fn func()) *fyne.MenuItem {
		it := fyne.NewMenuItem(label, fn)
		if k != "" {
			it.Shortcut = &desktop.CustomShortcut{KeyName: k, Modifier: mod}
		}
		return it
	}
	cmd, shift := fyne.KeyModifierSuper, fyne.KeyModifierShift
	about := fyne.NewMenuItem("About", u.about)
	quit := fyne.NewMenuItem("Quit kipple", u.requestQuit)
	quit.IsQuit = true
	quit.Shortcut = &desktop.CustomShortcut{KeyName: fyne.KeyQ, Modifier: cmd}
	file := fyne.NewMenu("File",
		item("Scan", fyne.KeyR, cmd, func() {
			if u.debounce("scan") {
				u.rescan()
			}
		}),
		item("Move to Trash…", fyne.KeyBackspace, cmd, u.moveToTrash),
		fyne.NewMenuItemSeparator(),
		item("Open Trash", fyne.KeyT, cmd|shift, u.openTrash),
		item("Open Receipt", fyne.KeyO, cmd, u.openReceipt),
		fyne.NewMenuItemSeparator(),
		item("Close Window", fyne.KeyW, cmd, u.requestQuit),
		about, quit,
	)
	edit := fyne.NewMenu("Edit",
		item("Copy Path", fyne.KeyC, cmd, func() {
			if u.debounce("copy") {
				u.copyPath()
			}
		}),
		item("Copy ID", fyne.KeyC, cmd|shift, func() {
			if u.debounce("copyid") {
				u.copyID()
			}
		}),
	)
	view := fyne.NewMenu("View",
		item("Zoom In", fyne.KeyEqual, cmd, func() { u.zoom(1.1) }),
		item("Zoom Out", fyne.KeyMinus, cmd, func() { u.zoom(1 / 1.1) }),
		item("Actual Size", fyne.Key0, cmd, func() { u.zoom(0) }),
	)
	help := fyne.NewMenu("Help", fyne.NewMenuItem("How kipple Stays Safe", u.safety))
	u.menu = fyne.NewMainMenu(file, edit, view, help)
	return u.menu
}

// refreshMenu enables only what applies now.
func (u *ui) refreshMenu() {
	m := u.m
	file, edit := u.menu.Items[0], u.menu.Items[1]
	file.Items[0].Disabled = !m.CanScan()
	canMove := m.CanMove() || m.Sheet() == guimodel.SheetConfirm && m.Confirm().CanMove
	file.Items[1].Disabled = !canMove
	hasCard := u.card() != nil && u.card().ID != ""
	file.Items[3].Disabled, file.Items[4].Disabled = !hasCard, !hasCard
	_, ok := m.CursorPath()
	edit.Items[0].Disabled = !ok || m.Screen() != guimodel.ScreenReview
	edit.Items[1].Disabled = edit.Items[0].Disabled
	u.menu.Refresh()
}

func (u *ui) about() {
	dialog.ShowInformation("About kipple", "kipple "+version+" (commit "+commit+")\nMIT licence.\n\nkipple only moves things to the Trash, checks each item again just before it moves, and writes a receipt so it can put them back.", u.win)
}

// safetyText is the five promises of docs/safety.md.
const safetyText = `1. kipple looks first. A scan only reads; nothing moves until you confirm.
2. Only the Trash. kipple moves items to the Trash; it never deletes them.
3. Checked again. Each item is authorised and checked again just before it moves, and skipped with a reason if anything changed.
4. A receipt for every move. kipple records what moved and where, so it can put it back.
5. Never your documents. Keys, .env files, documents and other protected files are refused, and kipple never uses sudo or the network.`

func (u *ui) safety() {
	dialog.ShowInformation("How kipple stays safe", safetyText, u.win)
}
