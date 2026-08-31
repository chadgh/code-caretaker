package gh

import (
	"testing"
	"time"
)

func stubRun(t *testing.T, fn func(name string, args ...string) string) {
	t.Helper()
	orig := runOutput
	origSleep := sleepFn
	t.Cleanup(func() { runOutput = orig; sleepFn = origSleep })
	runOutput = fn
	sleepFn = func(time.Duration) {}
}

func TestGetOpenPRsParsesJSON(t *testing.T) {
	stubRun(t, func(name string, args ...string) string {
		return `[{"number":13,"title":"x","mergeable":"MERGEABLE","statusCheckRollup":[]}]`
	})
	prs := GetOpenPRs("chadgh/class-cash")
	if len(prs) != 1 || Number(prs[0]) != 13 {
		t.Errorf("prs = %+v", prs)
	}
}

func TestGetOpenPRsEmptyOnMalformed(t *testing.T) {
	stubRun(t, func(name string, args ...string) string { return "not json" })
	if len(GetOpenPRs("r")) != 0 {
		t.Error("expected empty")
	}
}

func TestResolveMergeabilityRetriesUntilResolved(t *testing.T) {
	calls := 0
	stubRun(t, func(name string, args ...string) string {
		// First call is the pr list with UNKNOWN; subsequent are pr view.
		calls++
		if calls == 1 {
			return `[{"number":7,"mergeable":"UNKNOWN"}]`
		}
		return `{"mergeable":"CONFLICTING"}`
	})
	prs := GetOpenPRs("r")
	if got := String(prs[0], "mergeable"); got != "CONFLICTING" {
		t.Errorf("mergeable = %q after retry", got)
	}
}

func TestResolveMergeabilityGivesUpOnPersistentUnknown(t *testing.T) {
	stubRun(t, func(name string, args ...string) string {
		if args[0] == "pr" && args[1] == "list" {
			return `[{"number":7,"mergeable":"UNKNOWN"}]`
		}
		return `{"mergeable":"UNKNOWN"}`
	})
	prs := GetOpenPRs("r")
	if got := String(prs[0], "mergeable"); got != "UNKNOWN" {
		t.Errorf("mergeable = %q", got)
	}
}
