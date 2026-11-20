package selection

import (
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/policy"
	"github.com/rbutera/kipple/internal/testutil"
)

func TestMain(m *testing.M) { os.Exit(testutil.GuardedMain(m)) }

func item(id, tier string, selected bool, reasons ...string) plan.Item {
	if reasons == nil {
		reasons = []string{}
	}
	return plan.Item{ID: id, Rule: "r-" + tier, Tier: tier, Selected: selected, Reasons: reasons, AllocatedBytes: 10}
}

// Every tier crossed with every reason: selectable exactly when
// apply.Preview naming the item alone would select it.
func TestSelectableMatchesPreview(t *testing.T) {
	tiers := []string{"safe", "review", "manual", "info"}
	reasons := append([]string{""}, plan.Reasons()...)
	reasons = append(reasons, plan.Refused(policy.Reasons()[0]))
	n := 0
	var items []plan.Item
	for _, tier := range tiers {
		for _, r := range reasons {
			for _, withTier := range []bool{false, true} {
				var rs []string
				if withTier && (tier == "review" || tier == "manual" || tier == "info") {
					rs = append(rs, "tier_"+tier)
				}
				if r != "" {
					rs = append(rs, r)
				}
				id := fmt.Sprintf("%012d", n)
				n++
				items = append(items, item(id, tier, len(rs) == 0 && tier != "info", rs...))
			}
		}
	}
	p := plan.Plan{Items: items}
	s := New(p)
	for _, it := range items {
		pv := apply.Preview(p, []string{it.ID})
		var want bool
		for _, x := range pv.Items {
			if x.ID == it.ID {
				want = x.Selected
			}
		}
		if got := s.Selectable(it.ID); got != want {
			t.Errorf("%s tier %s reasons %v: selectable %v, Preview %v", it.ID, it.Tier, it.Reasons, got, want)
		}
		if it.Tier == "info" && s.Selectable(it.ID) {
			t.Errorf("info item %s is selectable", it.ID)
		}
	}
}

func TestPreselectionNeverInfo(t *testing.T) {
	p := plan.Plan{Items: []plan.Item{
		item("aaaaaaaaaaaa", "safe", true),
		item("bbbbbbbbbbbb", "info", true), // a plan that claims an info item is selected
		item("cccccccccccc", "review", false, plan.ReasonTierReview),
	}}
	if got := Preselected(p); !reflect.DeepEqual(got, []string{"aaaaaaaaaaaa"}) {
		t.Fatalf("%v", got)
	}
	s := New(p)
	if !s.Selected("aaaaaaaaaaaa") || s.Selected("bbbbbbbbbbbb") || s.Len() != 1 {
		t.Fatal("pre-selection")
	}
	if !s.Same([]string{"aaaaaaaaaaaa"}) {
		t.Fatal("Same")
	}
}

func TestToggleVerdicts(t *testing.T) {
	p := plan.Plan{Items: []plan.Item{
		item("aaaaaaaaaaaa", "safe", true),
		item("bbbbbbbbbbbb", "info", false, plan.ReasonTierInfo),
		item("cccccccccccc", "manual", false, plan.ReasonTierManual),
		item("dddddddddddd", "safe", false, plan.ReasonOwnerRunning),
	}}
	s := New(p)
	cases := []struct {
		id   string
		want Verdict
	}{
		{"aaaaaaaaaaaa", Removed}, {"aaaaaaaaaaaa", Added},
		{"bbbbbbbbbbbb", RefusedInfo}, {"cccccccccccc", Added},
		{"dddddddddddd", RefusedBlocked}, {"zzzzzzzzzzzz", Unknown},
	}
	for _, c := range cases {
		if got := s.Toggle(c.id); got != c.want {
			t.Errorf("toggle %s: %v, want %v", c.id, got, c.want)
		}
	}
	if s.Selected("bbbbbbbbbbbb") || s.Selected("dddddddddddd") {
		t.Fatal("a refused item was selected")
	}
	if n, b := s.Total(); n != 2 || b != 20 {
		t.Fatalf("total %d %d", n, b)
	}
	if got := s.IDs(); !reflect.DeepEqual(got, []string{"aaaaaaaaaaaa", "cccccccccccc"}) {
		t.Fatalf("%v", got)
	}
}

func TestGroupToggle(t *testing.T) {
	p := plan.Plan{Items: []plan.Item{
		item("aaaaaaaaaaaa", "review", false, plan.ReasonTierReview),
		item("bbbbbbbbbbbb", "review", false, plan.ReasonTierReview),
		item("cccccccccccc", "review", false, plan.ReasonTierReview, plan.ReasonGitDirty),
		item("dddddddddddd", "manual", false, plan.ReasonTierManual),
		item("eeeeeeeeeeee", "info", false, plan.ReasonTierInfo),
	}}
	s := New(p)
	g := []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc"}
	if s.Group("review", g) != None {
		t.Fatal("none")
	}
	s.Toggle("aaaaaaaaaaaa")
	if s.Group("review", g) != Partial {
		t.Fatal("partial")
	}
	if s.ToggleGroup("review", g) != All || s.Selected("cccccccccccc") {
		t.Fatal("all, blocked skipped")
	}
	if s.ToggleGroup("review", g) != None || s.Len() != 0 {
		t.Fatal("none again")
	}
	if s.Group("manual", []string{"dddddddddddd"}) != NoTile || s.ToggleGroup("manual", []string{"dddddddddddd"}) != NoTile || s.Len() != 0 {
		t.Fatal("manual groups have no tile")
	}
	if s.ToggleGroup("info", []string{"eeeeeeeeeeee"}) != NoTile || s.Len() != 0 {
		t.Fatal("info groups have no tile")
	}
	if s.Group("review", []string{"cccccccccccc"}) != NoTile {
		t.Fatal("a group with nothing selectable has no tile")
	}
}
