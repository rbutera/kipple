//go:build gui

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"

	"github.com/rbutera/kipple/internal/guimodel"
)

// Red team rt8 F3, kept as a regression test with real time: holding ⌘⌫
// sends the shortcut again after macOS's default initial key-repeat delay
// (375 ms) and every 90 ms after. The first press opens the sheet; the
// repeats must not confirm it.
func TestRT8HoldCmdBackspaceMoves(t *testing.T) {
	home := fixture(t)
	u := newTestUI(t, false)
	u.m.ScanNow()
	u.update()
	sc := &desktop.CustomShortcut{KeyName: fyne.KeyBackspace, Modifier: fyne.KeyModifierSuper}
	press := func(fyne.Shortcut) { u.moveToTrash() }
	press(sc)
	if u.m.Sheet() != guimodel.SheetConfirm {
		t.Fatalf("sheet %d", u.m.Sheet())
	}
	for i := 0; i < 3; i++ {
		if i == 0 {
			time.Sleep(375 * time.Millisecond)
		} else {
			time.Sleep(90 * time.Millisecond)
		}
		press(sc)
	}
	ents, _ := os.ReadDir(filepath.Join(home, ".Trash"))
	if u.m.Sheet() != guimodel.SheetConfirm || u.m.Screen() == guimodel.ScreenDone || len(ents) > 0 {
		t.Fatalf("one held ⌘⌫ moved items: screen %d sheet %d, Trash holds %d", u.m.Screen(), u.m.Sheet(), len(ents))
	}
}

// Red team rt8 F4: Open Trash opens only the home's Trash, in Finder, and
// Open receipt only reveals the receipt, whatever the receipt file says.
func TestRT8OpenTrashFromTamperedReceipt(t *testing.T) {
	home := fixture(t)
	state := os.Getenv("KIPPLE_STATE_DIR")
	u := newTestUI(t, false)
	var opened [][]string
	u.open = func(a ...string) { opened = append(opened, a) }
	u.openTrash()
	u.openReceipt()
	u.m.ScanNow()
	u.update()
	u.openTrash()
	u.openReceipt()
	if len(opened) != 0 {
		t.Fatalf("opened before any run: %q", opened)
	}
	u.m.OpenConfirm()
	if err := u.m.ApplyNow(); err != nil {
		t.Fatal(err)
	}
	rp := u.m.Result().ReceiptPath
	b, err := os.ReadFile(rp)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, `"trash_dir": "`)
	if i < 0 {
		t.Fatal("no trash_dir in the receipt")
	}
	j := strings.Index(s[i+14:], `"`)
	s = s[:i+14] + "/System/Applications/Calculator.app" + s[i+14+j:]
	if err := os.WriteFile(rp, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	u.update()
	u.openTrash()
	u.openReceipt()
	if len(opened) != 2 || strings.Join(opened[0], " ") != "-a Finder "+home+"/.Trash" ||
		opened[1][0] != "-R" || !strings.HasPrefix(opened[1][1], state+"/") {
		t.Fatalf("open argv %q", opened)
	}
}
