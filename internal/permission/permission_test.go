package permission

import "testing"

func TestLastMatchWins(t *testing.T) {
	g := NewGate(ModeAuto, []Rule{
		{Tool: "*", Action: Deny},
		{Tool: "trim", Action: Allow},
	}, nil)
	if d := g.Check(Request{Tool: "trim", Mutating: true}); d.Action != Allow {
		t.Fatalf("trim should be allowed by the later rule: %+v", d)
	}
	if d := g.Check(Request{Tool: "concat", Mutating: true}); d.Action != Deny {
		t.Fatalf("concat should be denied by the wildcard: %+v", d)
	}
}

func TestWildcardPrefix(t *testing.T) {
	r := Rule{Tool: "change_*", Action: Deny}
	if !r.matches("change_background") || r.matches("trim") {
		t.Fatal("prefix wildcard broken")
	}
}

func TestModeDefaults(t *testing.T) {
	auto := NewGate(ModeAuto, nil, nil)
	if d := auto.Check(Request{Tool: "trim", Mutating: true}); d.Action != Allow {
		t.Fatal("auto mode should allow mutations by default")
	}
	if d := auto.Check(Request{Tool: "probe"}); d.Action != Allow {
		t.Fatal("read-only always allowed")
	}

	asked := 0
	ask := NewGate(ModeAsk, nil, func(Request) Decision { asked++; return Decision{Action: Allow} })
	if d := ask.Check(Request{Tool: "trim", Mutating: true}); d.Action != Allow || asked != 1 {
		t.Fatal("ask mode should prompt for mutations")
	}
	if d := ask.Check(Request{Tool: "probe"}); d.Action != Allow || asked != 1 {
		t.Fatal("ask mode should not prompt for read-only tools")
	}
}

func TestPlanModeIsHardCeiling(t *testing.T) {
	g := NewGate(ModePlan, []Rule{{Tool: "*", Action: Allow}}, nil)
	d := g.Check(Request{Tool: "trim", Mutating: true})
	if d.Action != Deny || d.Feedback == "" {
		t.Fatalf("plan mode must deny mutations despite allow rules: %+v", d)
	}
	if d := g.Check(Request{Tool: "probe"}); d.Action != Allow {
		t.Fatal("plan mode still allows read-only inspection")
	}
}

func TestSafetyTierIsBypassImmune(t *testing.T) {
	asked := 0
	g := NewGate(ModeAuto, []Rule{{Tool: "export", Action: Allow}},
		func(r Request) Decision { asked++; return Decision{Action: Deny} })
	d := g.Check(Request{Tool: "export", Mutating: true, Safety: "overwrites existing file x.mp4"})
	if asked != 1 || d.Action != Deny {
		t.Fatalf("safety request must prompt even with an allow rule: asked=%d %+v", asked, d)
	}
}

func TestAlwaysAllowRecordsSessionRule(t *testing.T) {
	calls := 0
	g := NewGate(ModeAsk, nil, func(Request) Decision {
		calls++
		return Decision{Action: Allow, AlwaysAllow: true}
	})
	g.Check(Request{Tool: "trim", Mutating: true})
	g.Check(Request{Tool: "trim", Mutating: true})
	if calls != 1 {
		t.Fatalf("second call should be auto-allowed by the session rule, asked %d times", calls)
	}
}

func TestNoAskerDegradesToGuidedDeny(t *testing.T) {
	g := NewGate(ModeAsk, nil, nil)
	d := g.Check(Request{Tool: "trim", Mutating: true})
	if d.Action != Deny || d.Feedback == "" {
		t.Fatalf("headless ask must deny with feedback: %+v", d)
	}
}

func TestDenyWithFeedbackPassthrough(t *testing.T) {
	g := NewGate(ModeAsk, nil, func(Request) Decision {
		return Decision{Action: Deny, Feedback: "trim from 5s instead"}
	})
	d := g.Check(Request{Tool: "trim", Mutating: true})
	if d.Feedback != "trim from 5s instead" {
		t.Fatalf("user feedback lost: %+v", d)
	}
}
