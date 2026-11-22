package main

import "testing"

// TestEveryRuleSaysWhatBringsItBack checks that both catalogs' rules each
// have one recovery kind, and that the table names no rule that is in
// neither catalog.
func TestEveryRuleSaysWhatBringsItBack(t *testing.T) {
	known := map[string]bool{}
	for _, rules := range [][]Rule{darwinJunkRules, windowsJunkRules} {
		for _, r := range rules {
			known[r.ID] = true
			if recoveryOf(r.ID) == "" {
				t.Errorf("%s has no recovery kind", r.ID)
			}
		}
	}
	seen := map[string]recoveryKind{}
	for kind, ids := range ruleRecovery {
		for _, id := range ids {
			if !known[id] {
				t.Errorf("%s is in no catalog", id)
			}
			if other, dup := seen[id]; dup {
				t.Errorf("%s is both %s and %s", id, other, kind)
			}
			seen[id] = kind
		}
	}
}

// TestPreselectedRulesComeBack checks that no rule a scan selects by itself
// removes what stays gone.
func TestPreselectedRulesComeBack(t *testing.T) {
	for _, rules := range [][]Rule{darwinJunkRules, windowsJunkRules} {
		for _, r := range rules {
			if r.Default && recoveryOf(r.ID) == recoverGone {
				t.Errorf("%s is selected by a scan and stays gone", r.ID)
			}
		}
	}
}
