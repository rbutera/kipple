// Package selection holds the rules for what a person may choose in an
// interactive front end (the TUI and the GUI), so both follow kipple clean
// exactly:
//
//   - Pre-selected: the items the scan selected, never an info item. A scan
//     at level safe therefore pre-selects what kipple scan --level safe
//     selects.
//   - Selectable: what apply.Preview would select if the item were named
//     with --only: never an info item, and only when nothing but the item's
//     tier (review or manual) keeps it unselected. Naming it is consent to
//     that tier.
//   - Groups: a rule's safe or review items can be chosen together; manual
//     items one at a time; info groups not at all.
//
// It decides nothing about the file system. Apply authorises and re-checks
// every item again before it moves.
package selection

import (
	"github.com/rbutera/kipple/internal/apply"
	"github.com/rbutera/kipple/internal/plan"
	"github.com/rbutera/kipple/internal/rules"
)

// Set is a selection over one plan.
type Set struct {
	items      []plan.Item
	index      map[string]int
	selectable map[string]bool
	on         map[string]bool
}

// New returns the selection for p, with its pre-selection applied.
func New(p plan.Plan) *Set {
	s := &Set{items: p.Items, index: map[string]int{}, selectable: map[string]bool{}, on: map[string]bool{}}
	ids := make([]string, 0, len(p.Items))
	for i, it := range p.Items {
		s.index[it.ID] = i
		ids = append(ids, it.ID)
	}
	// One Preview naming every item: what it would select is exactly what
	// may be chosen (apply.Preview's rule, used by kipple clean --only).
	if len(ids) > 0 {
		for _, it := range apply.Preview(p, ids).Items {
			if it.Selected {
				s.selectable[it.ID] = true
			}
		}
	}
	for _, id := range Preselected(p) {
		s.on[id] = true
	}
	return s
}

// Preselected returns the IDs a front end selects for the person, in plan
// order: the plan's selected items, never an info item.
func Preselected(p plan.Plan) []string {
	var out []string
	for _, it := range p.Items {
		if it.Selected && it.Tier != string(rules.TierInfo) {
			out = append(out, it.ID)
		}
	}
	return out
}

// Selectable reports whether the item may be chosen.
func (s *Set) Selectable(id string) bool { return s.selectable[id] }

// Selected reports whether the item is chosen.
func (s *Set) Selected(id string) bool { return s.on[id] }

// Len is the number of chosen items.
func (s *Set) Len() int { return len(s.on) }

// Verdict is the outcome of a toggle.
type Verdict int

const (
	// Unknown: no such item.
	Unknown Verdict = iota
	// Added: the item is now selected.
	Added
	// Removed: the item is no longer selected.
	Removed
	// RefusedInfo: info items are never selectable.
	RefusedInfo
	// RefusedBlocked: something other than its tier blocks the item.
	RefusedBlocked
)

// Toggle flips the item's selection, refusing info and blocked items.
func (s *Set) Toggle(id string) Verdict {
	i, ok := s.index[id]
	if !ok {
		return Unknown
	}
	if s.on[id] {
		delete(s.on, id)
		return Removed
	}
	if s.items[i].Tier == string(rules.TierInfo) {
		return RefusedInfo
	}
	if !s.selectable[id] {
		return RefusedBlocked
	}
	s.on[id] = true
	return Added
}

// GroupState is a rule group's tile.
type GroupState int

const (
	// NoTile: the group cannot be chosen as a whole (manual or info, or
	// nothing in it is selectable).
	NoTile GroupState = iota
	// None: no selectable item is chosen.
	None
	// Partial: some selectable items are chosen.
	Partial
	// All: every selectable item is chosen.
	All
)

// GroupHasTile reports whether a tier's groups can be chosen as a whole.
func GroupHasTile(tier string) bool {
	return tier == string(rules.TierSafe) || tier == string(rules.TierReview)
}

// Group returns the state of the group made of ids, of tier.
func (s *Set) Group(tier string, ids []string) GroupState {
	if !GroupHasTile(tier) {
		return NoTile
	}
	n, on := 0, 0
	for _, id := range ids {
		if s.selectable[id] {
			n++
			if s.on[id] {
				on++
			}
		}
	}
	switch {
	case n == 0:
		return NoTile
	case on == 0:
		return None
	case on == n:
		return All
	default:
		return Partial
	}
}

// ToggleGroup selects every selectable item of the group, or, when all are
// already selected, unselects them. Blocked and info items are never
// touched. It reports the new state.
func (s *Set) ToggleGroup(tier string, ids []string) GroupState {
	st := s.Group(tier, ids)
	if st == NoTile {
		return st
	}
	for _, id := range ids {
		if !s.selectable[id] {
			continue
		}
		if st == All {
			delete(s.on, id)
		} else {
			s.on[id] = true
		}
	}
	return s.Group(tier, ids)
}

// IDs returns the selection in plan order.
func (s *Set) IDs() []string {
	var out []string
	for _, it := range s.items {
		if s.on[it.ID] {
			out = append(out, it.ID)
		}
	}
	return out
}

// Total returns the selected count and allocated bytes.
func (s *Set) Total() (int, int64) {
	n, b := 0, int64(0)
	for _, it := range s.items {
		if s.on[it.ID] {
			n++
			b += it.AllocatedBytes
		}
	}
	return n, b
}

// Same reports whether the selection is exactly the pre-selection.
func (s *Set) Same(ids []string) bool {
	if len(ids) != len(s.on) {
		return false
	}
	for _, id := range ids {
		if !s.on[id] {
			return false
		}
	}
	return true
}
