package theme

import (
	"reflect"
	"strings"
	"testing"
)

// The palette while an environment is in force is the built-in, the operator's
// own block, the environment's colour as the primary, and the environment's own
// block, lowest to highest. These hold that order, what each layer may and may
// not do to the one beneath it, and that the live palette really moves with
// the layers and goes back.

func global(colors map[string]string) Layer { return Layer{Source: "theme", Colors: colors} }

func colour(hex string) Layer {
	return Layer{Source: "profiles.prod", Colors: map[string]string{"primary": hex}, As: "color", Guarded: true}
}

func own(colors map[string]string) Layer {
	return Layer{Source: "profiles.prod.theme", Colors: colors, Guarded: true}
}

func TestEachLayerOverridesTheOneBeneathItInTheOrderTheyAreGiven(t *testing.T) {
	g := global(map[string]string{"primary": "#111111", "accent": "#222222"})
	c := colour("#333333")
	o := own(map[string]string{"primary": "#444444"})

	for _, tc := range []struct {
		name   string
		layers []Layer
		want   map[string]string
	}{
		{"nothing states anything", nil, map[string]string{}},
		{"the operator's block alone", []Layer{g}, map[string]string{"primary": "#111111", "accent": "#222222"}},
		{"the environment's colour is the primary", []Layer{g, c}, map[string]string{"primary": "#333333", "accent": "#222222"}},
		{"its own theme wins over its colour", []Layer{g, c, o}, map[string]string{"primary": "#444444", "accent": "#222222"}},
		{"and over the operator's block without one", []Layer{g, o}, map[string]string{"primary": "#444444", "accent": "#222222"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, problems := Resolve(tc.layers...)
			if len(problems) != 0 {
				t.Fatalf("problems = %v", problems)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("overrides = %v, want %v", got, tc.want)
			}
		})
	}
}

// What an environment's palette is shown beside, and what puts it back: the
// live colours follow the layers they are applied from, and applying the
// operator's block alone — which is what no environment in force is — returns
// every slot to it.
func TestApplyingLayersMovesThePaletteAndAnEnvironmentSwitchedOffPutsItBack(t *testing.T) {
	reset(t)
	g := global(map[string]string{"primary": "#111111", "accent": "#222222"})

	ApplyLayers(g)
	if got := Current()["primary"]; got != "#111111" {
		t.Fatalf("the operator's primary = %s", got)
	}
	if got := toHex(Key.GetForeground()); got != "#111111" {
		t.Fatalf("a derived style did not follow: Key is %s", got)
	}

	if problems := ApplyLayers(g, colour("#333333"), own(map[string]string{"muted": "#00AA66"})); len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	live := Current()
	if live["primary"] != "#333333" || live["muted"] != "#00AA66" || live["accent"] != "#222222" {
		t.Errorf("the environment's palette is not what is live: %v", live)
	}
	if got := toHex(Key.GetForeground()); got != "#333333" {
		t.Errorf("Key is %s inside the environment, want its colour", got)
	}
	if got := toHex(Subtle.GetForeground()); got != "#00AA66" {
		t.Errorf("Subtle is %s inside the environment, want its own", got)
	}

	// Switched off: the same call with the layers the environment brought left
	// out. Nothing of it is left behind, in the palette or in a style.
	ApplyLayers(g)
	live = Current()
	if live["primary"] != "#111111" || live["muted"] != mutedHex {
		t.Errorf("the environment left colours behind: primary %s, muted %s", live["primary"], live["muted"])
	}
	if got := toHex(Subtle.GetForeground()); got != mutedHex {
		t.Errorf("Subtle is %s after the environment, want the built-in", got)
	}

	// And with no operator block either, the built-ins.
	ApplyLayers(global(nil))
	for slot, hex := range Current() {
		if hex != fields[slot].builtin {
			t.Errorf("%s = %s with nothing stated, want the built-in %s", slot, hex, fields[slot].builtin)
		}
	}
}

// One bad entry costs its own line: the colour beneath it stands, which is the
// operator's own and not the built-in, and every other entry applies.
func TestABadEntryInALayerLeavesTheColourBeneathItStanding(t *testing.T) {
	reset(t)
	g := global(map[string]string{"muted": "#AA0000", "label": "#0000AA"})
	o := own(map[string]string{"muted": "red", "colour": "#123456", "accent": "#00AA66"})

	overrides, problems := Resolve(g, o)
	if overrides["muted"] != "#AA0000" {
		t.Errorf("muted = %q, want the operator's #AA0000 beneath the entry that was not a colour", overrides["muted"])
	}
	if overrides["accent"] != "#00AA66" || overrides["label"] != "#0000AA" {
		t.Errorf("the entries that were fine did not apply: %v", overrides)
	}
	keys := map[string]string{}
	for _, p := range problems {
		keys[p.Key()] = p.Reason
	}
	if len(keys) != 2 || keys["profiles.prod.theme.muted"] == "" || keys["profiles.prod.theme.colour"] == "" {
		t.Errorf("the problems should name the two lines by the key they are written under, got %v", keys)
	}
	if !strings.Contains(keys["profiles.prod.theme.muted"], `"red"`) {
		t.Errorf("the reason does not quote the value: %q", keys["profiles.prod.theme.muted"])
	}
}

// The status colours are the operator's to set, in their own named config file,
// and nothing an environment or a file nobody named brings may state them: not
// the same as another, not near one, not swapped. A threshold would have to
// guess at what is too close for everybody's eyes; leaving the slots out of
// what a guarded layer can say needs no guess.
func TestAGuardedLayerMayNotStateAStatusColourAtAll(t *testing.T) {
	reset(t)
	for place, layer := range map[string]func(map[string]string) Layer{
		"an environment's own theme": own,
		"a file nobody named": func(c map[string]string) Layer {
			return Layer{Source: "theme", Colors: c, Guarded: true}
		},
	} {
		for _, slot := range []string{"good", "warn", "bad", "inverse", "ink"} {
			t.Run(place+"/"+slot, func(t *testing.T) {
				// A colour a long way from every other, so nothing but the slot is wrong.
				l := layer(map[string]string{slot: "#778899", "accent": "#445566"})
				overrides, problems := Resolve(l)
				if _, there := overrides[slot]; there {
					t.Errorf("%s was applied from a guarded layer: %v", slot, overrides)
				}
				if overrides["accent"] != "#445566" {
					t.Errorf("an entry that is no status was left out with it: %v", overrides)
				}
				if len(problems) != 1 || problems[0].Field != slot || problems[0].Source != l.Source {
					t.Fatalf("problems = %v, want %s named once, by the line it is on", problems, slot)
				}
				p := problems[0]
				if !strings.Contains(p.Reason, "status colour") || !strings.Contains(p.Reason, "your own config file") {
					t.Errorf("the reason does not say the status colours are the operator's: %q", p.Reason)
				}
				if !strings.Contains(p.Hint, "rta config set theme."+slot) || !strings.Contains(p.Hint, "left out") {
					t.Errorf("the hint does not say what is left of it and what to do: %q", p.Hint)
				}
			})
		}
	}
}

// What the old guard missed, and why it was the wrong shape: one digit away from
// the failure colour and the badge's text is invisible, and the pair swapped
// reads failure as success. Neither is a question for a threshold now.
func TestANearlyEqualOrSwappedStatusFromAGuardedLayerIsNotAppliedEither(t *testing.T) {
	reset(t)
	for name, colors := range map[string]map[string]string{
		"a digit from the badge's own background": {"inverse": "#FF4358"},
		"ink beside warn":                         {"ink": "#BF7E01"},
		"good and bad swapped":                    {"good": "#FF0000", "bad": "#00FF00"},
	} {
		t.Run(name, func(t *testing.T) {
			overrides, problems := Resolve(own(colors))
			if len(overrides) != 0 {
				t.Errorf("a status colour stood: %v", overrides)
			}
			if len(problems) != len(colors) {
				t.Errorf("problems = %v, want one per entry", problems)
			}
		})
	}
	ApplyLayers(own(map[string]string{"good": "#FF0000", "bad": "#00FF00"}))
	if live := Current(); live["good"] != goodHex || live["bad"] != badHex {
		t.Errorf("an environment swapped the statuses: good %s, bad %s", live["good"], live["bad"])
	}
}

// What the operator states about their own screen is theirs, the status colours
// included, so the guard is for what an environment brings and not a refusal of
// the file's own block: good and bad the same in `theme:` is a monochrome
// palette and always was.
func TestTheOperatorsOwnBlockKeepsEveryStatusColour(t *testing.T) {
	mine := map[string]string{"good": "#888888", "warn": "#999999", "bad": "#888888", "inverse": "#101010", "ink": "#EEEEEE"}
	overrides, problems := Resolve(global(mine))
	if len(problems) != 0 || !reflect.DeepEqual(overrides, mine) {
		t.Errorf("the operator's own block was held to a rule of the environment's: %v %v", overrides, problems)
	}
	// And an environment that leaves those alone does not inherit a fault
	// that was not its own.
	overrides, problems = Resolve(global(mine), own(map[string]string{"accent": "#445566"}))
	if len(problems) != 0 || overrides["accent"] != "#445566" || overrides["bad"] != "#888888" {
		t.Errorf("an environment was blamed for the operator's own block: %v %v", overrides, problems)
	}
}

// The environment's colour is the primary, and a primary that is exactly a
// status colour would put the selection in the colour of that status.
func TestAnEnvironmentColourThatIsAStatusColourIsLeftOutOfThePalette(t *testing.T) {
	reset(t)
	for _, status := range []string{"good", "warn", "bad"} {
		t.Run(status, func(t *testing.T) {
			hex := strings.ToLower(fields[status].builtin) // spelled differently from the built-in
			overrides, problems := Resolve(colour(hex))
			if _, there := overrides["primary"]; there {
				t.Errorf("primary = %s is the %s colour and was applied: %v", hex, status, overrides)
			}
			if len(problems) != 1 || problems[0].Key() != "profiles.prod.color" {
				t.Fatalf("problems = %v, want the line `color:` named", problems)
			}
			if !strings.Contains(problems[0].Reason, status) {
				t.Errorf("the reason does not name %s: %q", status, problems[0].Reason)
			}
		})
	}
	// While the colour a person marks production with is the palette's primary.
	overrides, problems := Resolve(colour("#DD3333"))
	if len(problems) != 0 || overrides["primary"] != "#DD3333" {
		t.Errorf("an ordinary red was refused: %v %v", overrides, problems)
	}
}

// The environment's own block states primary last, so when its colour is the
// collision the block's primary stands in its place — and when the block's own
// primary collides the colour is what is beneath it.
func TestLeavingOutOneEntryLetsTheLayerBeneathItStand(t *testing.T) {
	overrides, problems := Resolve(colour("#DD3333"), own(map[string]string{"primary": badHex}))
	if overrides["primary"] != "#DD3333" {
		t.Errorf("primary = %q, want the environment's colour beneath its own refused one", overrides["primary"])
	}
	if len(problems) != 1 || problems[0].Key() != "profiles.prod.theme.primary" {
		t.Errorf("problems = %v", problems)
	}
}

// Against the operator's own colour, and without regard to how it is spelled:
// the status that stands is the operator's, and a primary that is it would draw
// the selection as that status.
func TestAnEnvironmentCannotStateAPrimaryThatIsTheStatusTheOperatorChose(t *testing.T) {
	g := global(map[string]string{"good": "#00aa66"})
	overrides, problems := Resolve(g, own(map[string]string{"primary": "#00AA66"}))
	if overrides["good"] != "#00aa66" {
		t.Errorf("the operator's own colour was taken from them: %v", overrides)
	}
	if _, there := overrides["primary"]; there {
		t.Errorf("an environment's primary took the operator's ok colour: %v", overrides)
	}
	if len(problems) != 1 || problems[0].Field != "primary" || !strings.Contains(problems[0].Reason, "good") {
		t.Errorf("problems = %v, want primary named as the same colour as good", problems)
	}
}

// Leaving one entry out can put a lower value in the way of the primary, and
// that is held to the same rule. The block's primary and the colour beneath it
// are both the operator's bad colour, so the colour is judged once the block's
// is gone, and left out too.
func TestLeavingAnEntryOutIsHeldToTheSameRuleAgain(t *testing.T) {
	g := global(map[string]string{"bad": "#AA0000"})
	overrides, problems := Resolve(g, colour("#AA0000"), own(map[string]string{"primary": "#aa0000"}))
	if _, there := overrides["primary"]; there {
		t.Errorf("primary was applied as the colour the layer above it was refused for: %v", overrides)
	}
	if overrides["bad"] != "#AA0000" {
		t.Errorf("the operator's own colour went with them: %v", overrides)
	}
	keys := map[string]bool{}
	for _, p := range problems {
		keys[p.Key()] = true
	}
	if len(problems) != 2 || !keys["profiles.prod.color"] || !keys["profiles.prod.theme.primary"] {
		t.Errorf("problems = %v, want both lines named", problems)
	}
}

// What nothing here guards beyond a floor: the colours that are decoration.
func TestAnEnvironmentMayRepaintWhatDoesNotCarryAMeaning(t *testing.T) {
	overrides, problems := Resolve(own(map[string]string{
		"accent": badHex, "muted": goodHex, "faint": warnHex, "label": primaryHex,
	}))
	if len(problems) != 0 || len(overrides) != 4 {
		t.Errorf("a decorative colour was refused: %v %v", overrides, problems)
	}
}

// **A floor against being invisible, not a standard for legibility.** The text
// colours of a guarded layer — an environment, a drop-in, a file nobody named —
// are an outside party's choice on the operator's screen, and one that is
// near-black draws the whole of it unreadably on a dark terminal. 1.5:1 against
// pure black and against pure white is what keeps every one of them visible on
// either kind; the built-in palette is at 3.4 and 6.2, and a 3:1 floor would
// have refused the built-in faint, which is structure and meant to recede.
func TestAGuardedTextColourKeepsAFloorOfContrastOnBlackAndWhite(t *testing.T) {
	reset(t)
	for _, slot := range []string{"primary", "label", "accent", "muted", "faint"} {
		for _, tc := range []struct {
			hex, ratio, against string
		}{
			{"#101010", "1.10:1", "black"},
			{"#0C0C0C", "1.07:1", "black"},
			{"#000000", "1.00:1", "black"},
			{"#F5F5F5", "1.09:1", "white"},
			{"#FFFFFF", "1.00:1", "white"},
		} {
			t.Run(slot+"/"+tc.hex, func(t *testing.T) {
				overrides, problems := Resolve(own(map[string]string{slot: tc.hex}))
				if _, there := overrides[slot]; there {
					t.Errorf("%s = %s was applied: %v", slot, tc.hex, overrides)
				}
				if len(problems) != 1 || problems[0].Key() != "profiles.prod.theme."+slot {
					t.Fatalf("problems = %v, want %s named", problems, slot)
				}
				p := problems[0]
				if !strings.Contains(p.Reason, tc.ratio) || !strings.Contains(p.Reason, tc.against) {
					t.Errorf("the reason does not give the measured ratio %s against %s: %q", tc.ratio, tc.against, p.Reason)
				}
				if !strings.Contains(p.Hint, "1.5:1") || !strings.Contains(p.Hint, "left out") {
					t.Errorf("the hint does not say what is asked of a colour, and what became of it: %q", p.Hint)
				}
			})
		}
	}
	// The operator's own block is theirs, and so is a screen of their own making.
	overrides, problems := Resolve(global(map[string]string{"primary": "#101010", "muted": "#FFFFFF"}))
	if len(problems) != 0 || overrides["primary"] != "#101010" || overrides["muted"] != "#FFFFFF" {
		t.Errorf("the operator's own block was held to a floor: %v %v", overrides, problems)
	}
}

func TestIsStatusNamesTheFiveSlotsOnlyTheOperatorSets(t *testing.T) {
	for _, slot := range Fields() {
		want := slot == "good" || slot == "warn" || slot == "bad" || slot == "inverse" || slot == "ink"
		if got := IsStatus(slot); got != want {
			t.Errorf("IsStatus(%q) = %v, want %v", slot, got, want)
		}
	}
	if IsStatus("colour") {
		t.Error("a name that is no slot was called a status")
	}
}

// The colours the documentation tells a person to write.
func TestTheColoursTheDocumentationShowsClearTheFloor(t *testing.T) {
	for _, hex := range []string{"#dd3333", "#DD3333", "#C792EA", "#FFB000", "#FF6B7A", "#D97757", "#D77015", "#7794B6", "#8B8B96", "#4A4A56"} {
		overrides, problems := Resolve(colour(hex), own(map[string]string{"label": hex, "muted": hex, "faint": hex, "accent": hex}))
		if len(problems) != 0 || len(overrides) != 5 {
			t.Errorf("%s was refused: %v", hex, problems)
		}
	}
}

// A profile's colour is its badge first, and the badge picks its own ink, so a
// colour too dim to be the screen's primary is still a colour to mark it by:
// only the derivation is skipped, and the note says so.
func TestAProfileColourTooDimForAPrimaryIsSkippedAndKeepsItsBadge(t *testing.T) {
	overrides, problems := Resolve(colour("#101010"))
	if _, there := overrides["primary"]; there {
		t.Errorf("a near-black primary was derived: %v", overrides)
	}
	if len(problems) != 1 || problems[0].Key() != "profiles.prod.color" {
		t.Fatalf("problems = %v, want the line `color:` named", problems)
	}
	if p := problems[0]; !strings.Contains(p.Reason, "1.10:1") || !strings.Contains(p.Hint, "badge keeps it") {
		t.Errorf("the note does not say what is skipped and what is kept: %+v", p)
	}
	if got := Badge("prod", "#101010"); got == "prod" || !strings.Contains(got, "prod") {
		t.Errorf("the badge lost the colour it is kept for: %q", got)
	}
}

// A slot written in another case is the one it nearly is, and the file is where
// that mistake is most likely made and read without an editor.
func TestAMiscasedSlotIsMetWithTheOneItNearlyIs(t *testing.T) {
	for typed, want := range map[string]string{"LABEL": "label", "Good": "good", "primry": "primary", "mutted": "muted"} {
		if got := NearestSlot(typed); got != want {
			t.Errorf("NearestSlot(%q) = %q, want %q", typed, got, want)
		}
	}
	if got := NearestSlot("zzzzzz"); got != "" {
		t.Errorf("NearestSlot(zzzzzz) = %q, a guess nothing supports", got)
	}
	_, problems := Resolve(global(map[string]string{"LABEL": "#C792EA"}))
	if len(problems) != 1 || !strings.Contains(problems[0].Reason, "did you mean `label`?") ||
		!strings.Contains(problems[0].Hint, "one of: ") {
		t.Errorf("problems = %v, want the near slot in the reason and the list in the hint", problems)
	}
	if _, problems = Resolve(own(map[string]string{"Muted": "#C792EA"})); len(problems) != 1 ||
		!strings.Contains(problems[0].Reason, "did you mean `muted`?") {
		t.Errorf("a guarded layer's typo was not helped either: %v", problems)
	}
	// Nothing near enough is the list alone, as it was.
	_, problems = Resolve(global(map[string]string{"zzzzzz": "#C792EA"}))
	if len(problems) != 1 || strings.Contains(problems[0].Reason, "did you mean") || !strings.Contains(problems[0].Hint, "one of: ") {
		t.Errorf("a guess nothing supports was offered: %v", problems)
	}
}

// An unquoted # starts a YAML comment, so `label: #C792EA` is a label with no
// value at all: the most likely mistake in a colour, and the message used to say
// the person had written an empty string.
func TestAnEmptyColourIsToldAsAnUnquotedHash(t *testing.T) {
	for _, layer := range []Layer{
		global(map[string]string{"label": ""}),
		own(map[string]string{"label": "  "}),
	} {
		_, problems := Resolve(layer)
		if len(problems) != 1 {
			t.Fatalf("problems = %v", problems)
		}
		p := problems[0]
		if strings.Contains(p.Reason, `""`) || !strings.Contains(p.Reason, "no value") {
			t.Errorf("the reason does not say there is no value: %q", p.Reason)
		}
		if !strings.Contains(p.Reason, "YAML comment") || !strings.Contains(p.Hint, `label: "#`) {
			t.Errorf("the line does not say a # starts a comment and show the quoted form: %q (%s)", p.Reason, p.Hint)
		}
	}
	// And the example in the generic hint is quoted, since copying it as shown
	// is how the mistake is made.
	_, problems := Resolve(global(map[string]string{"label": "red"}))
	if len(problems) != 1 || !strings.Contains(problems[0].Hint, `label: "#`) {
		t.Errorf("the hint's own example is not quoted: %v", problems)
	}
	// The profile's colour is written as `color`, and the example follows the line.
	_, problems = Resolve(Layer{Source: "profiles.prod", Colors: map[string]string{"primary": ""}, As: "color", Guarded: true})
	if len(problems) != 1 || !strings.Contains(problems[0].Hint, `color: "#`) {
		t.Errorf("the example does not use the key as written: %v", problems)
	}
}

func TestEffectiveIsWhatApplyLeavesWithoutApplyingIt(t *testing.T) {
	reset(t)
	overrides := map[string]string{"primary": "#123456", "good": "not-a-colour", "nope": "#ABCDEF"}
	before := Current()
	got := Effective(overrides)
	if !reflect.DeepEqual(Current(), before) {
		t.Fatal("Effective moved the live palette")
	}
	Apply(overrides)
	if !reflect.DeepEqual(got, Current()) {
		t.Errorf("Effective = %v, Current after Apply = %v", got, Current())
	}
	if len(got) != len(Fields()) {
		t.Errorf("Effective names %d slots, want all %d", len(got), len(Fields()))
	}
}

func TestAProblemNamesTheLineItIsAbout(t *testing.T) {
	for _, tc := range []struct {
		p    Problem
		want string
	}{
		{Problem{Field: "primary", Reason: "x"}, "theme.primary: x"},
		{Problem{Source: "profiles.prod.theme", Field: "bad", Reason: "x", Hint: "y"}, "profiles.prod.theme.bad: x (y)"},
		{Problem{Source: "profiles.prod", Field: "color", Reason: "x"}, "profiles.prod.color: x"},
	} {
		if got := tc.p.String(); got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
	}
}
