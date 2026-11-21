//go:build unix

package trash

import (
	"strings"
	"testing"
	"time"
)

// Unusual bytes in the path (specs/clean-apply): a space, "%", a newline
// and a non-UTF-8 byte are percent-encoded in Path and decode back to the
// original bytes; DeletionDate is local time with no zone.
func TestTrashInfoRoundTrip(t *testing.T) {
	deleted := time.Date(2026, 11, 21, 10, 30, 0, 0, time.Local)
	for _, tc := range []struct {
		name, path, wantLine string
	}{
		{"Plain path", "/home/me/.cache/pip/http", "Path=/home/me/.cache/pip/http"},
		{"Unusual bytes in the path", "/home/me/.cache/a b%c\nd\xff\xfe", "Path=/home/me/.cache/a%20b%25c%0Ad%FF%FE"},
		{"Reserved characters", "/home/me/x?#[]@!$&'()*+,;=:é", "Path=/home/me/x%3F%23%5B%5D%40%21%24%26%27%28%29%2A%2B%2C%3B%3D%3A%C3%A9"},
		{"Unreserved characters stay", "/h/A-Z_a.z~0-9", "Path=/h/A-Z_a.z~0-9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := EncodeTrashInfo(tc.path, deleted)
			if err != nil {
				t.Fatal(err)
			}
			want := "[Trash Info]\n" + tc.wantLine + "\nDeletionDate=2026-11-21T10:30:00\n"
			if string(data) != want {
				t.Fatalf("encoded %q, want %q", data, want)
			}
			got, err := DecodeTrashInfo(data)
			if err != nil {
				t.Fatal(err)
			}
			if got.Path != tc.path || !got.DeletionDate.Equal(deleted) {
				t.Fatalf("decoded %q %v, want %q %v", got.Path, got.DeletionDate, tc.path, deleted)
			}
		})
	}
	t.Run("DeletionDate is local time", func(t *testing.T) {
		utc := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		data, err := EncodeTrashInfo("/x", utc)
		if err != nil {
			t.Fatal(err)
		}
		if want := "DeletionDate=" + utc.In(time.Local).Format("2006-01-02T15:04:05") + "\n"; !strings.HasSuffix(string(data), want) {
			t.Fatalf("%q does not end with %q", data, want)
		}
	})
	t.Run("Relative or NUL paths are not encoded", func(t *testing.T) {
		for _, p := range []string{"relative/x", "", "/a\x00b"} {
			if _, err := EncodeTrashInfo(p, deleted); err == nil {
				t.Errorf("EncodeTrashInfo(%q) succeeded", p)
			}
		}
	})
}

// The decoder is strict: what kipple did not write in this shape is not
// trusted as describing an item.
func TestTrashInfoDecodeIsStrict(t *testing.T) {
	const date = "DeletionDate=2026-11-21T10:30:00\n"
	for _, tc := range []struct {
		name, data string
		ok         bool
		path       string
	}{
		{"Written by GNOME", "[Trash Info]\nPath=/home/me/a%20b\n" + date, true, "/home/me/a b"},
		{"Comments, blank lines and CRLF", "# c\n\n[Trash Info]\r\nPath=/x\r\n" + date, true, "/x"},
		{"Unknown key ignored", "[Trash Info]\nPath=/x\nX-Foo=1\n" + date, true, "/x"},
		{"Later group ignored", "[Trash Info]\nPath=/x\n" + date + "[Other]\nPath=/y\n", true, "/x"},
		{"Duplicate Path", "[Trash Info]\nPath=/x\nPath=/y\n" + date, false, ""},
		{"Duplicate DeletionDate", "[Trash Info]\nPath=/x\n" + date + date, false, ""},
		{"Relative path for the home Trash", "[Trash Info]\nPath=x/y\n" + date, false, ""},
		{"Escaped relative path", "[Trash Info]\nPath=%2Fx\n" + date, true, "/x"},
		{"Missing Path", "[Trash Info]\n" + date, false, ""},
		{"Missing DeletionDate", "[Trash Info]\nPath=/x\n", false, ""},
		{"Bad date", "[Trash Info]\nPath=/x\nDeletionDate=21/11/2026\n", false, ""},
		{"Date with a zone", "[Trash Info]\nPath=/x\nDeletionDate=2026-11-21T10:30:00Z\n", false, ""},
		{"Wrong group", "[Desktop Entry]\nPath=/x\n" + date, false, ""},
		{"Key before the group", "Path=/x\n[Trash Info]\n" + date, false, ""},
		{"Repeated group", "[Trash Info]\nPath=/x\n" + date + "[Trash Info]\n", false, ""},
		{"Truncated escape", "[Trash Info]\nPath=/x%2\n" + date, false, ""},
		{"Invalid escape", "[Trash Info]\nPath=/x%zz\n" + date, false, ""},
		{"Escaped NUL", "[Trash Info]\nPath=/x%00\n" + date, false, ""},
		{"Line without =", "[Trash Info]\nPath=/x\nbogus\n" + date, false, ""},
		{"Empty file", "", false, ""},
		{"Too large", "[Trash Info]\nPath=/" + strings.Repeat("a", maxInfoSize) + "\n" + date, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeTrashInfo([]byte(tc.data))
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok %v", err, tc.ok)
			}
			if tc.ok && got.Path != tc.path {
				t.Fatalf("Path = %q, want %q", got.Path, tc.path)
			}
		})
	}
}
