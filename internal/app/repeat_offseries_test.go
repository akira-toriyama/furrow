package app

import (
	"strings"
	"testing"
	"time"

	"github.com/akira-toriyama/furrow/v10/internal/core"
)

// `set --due` alone moves THIS occurrence and never the series. That is the
// rule the snooze needs, and it is silent exactly where it is misread: an
// operator who types a clock onto a whole-day chore sees `due … 11:00
// repeats`, and learns at the next close — one cycle late, when it looks like
// a defect — that the series still runs at the end of the day (t-6n5v).
//
// So the write says it, when the date was absolute and the series does not
// land on it — or when it carried this occurrence past others, which are then
// never created: a date further on reads as a deadline the series keeps, and a
// close hands back the occurrence after it (t-fq40). Every row below is one
// side of that line.
func TestSetSaysWhenAnAbsoluteDueLeavesTheSeries(t *testing.T) {
	const weekdays = "weekly on mon,tue,wed,thu,fri"
	done := "done"
	cases := []struct {
		name string
		due  string // the task's first due — its anchor
		rule string // "" = a task that does not repeat
		opts func(rule string) SetOpts
		want []string // substrings of the one note; nil = the write says nothing
		not  []string // substrings the note must not carry
	}{
		{
			name: "a clock typed onto a whole-day series",
			due:  "2026-11-18", rule: weekdays,
			opts: dueOnly("2026-11-18T11:00"),
			want: []string{
				"repeats, and --due alone moves THIS occurrence only",
				"a close now mints the next one for 2026-11-19 23:59 +09:00",
				"--due 2026-11-18T11:00 --repeat 'FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR'`",
			},
			not: []string{"COUNT", "<rule>", "never created", "deadline"},
		},
		{
			name: "the snooze is a delta, and a delta can only mean this occurrence",
			due:  "2026-11-18", rule: weekdays,
			opts: dueOnly("+1d"),
		},
		{
			name: "so is the shift",
			due:  "2026-11-18", rule: weekdays,
			opts: func(string) SetOpts { s := "+1d"; return SetOpts{DueShift: &s} },
		},
		{
			name: "the next date the series lands on passes nothing and has nothing to misread",
			due:  "2026-11-18", rule: weekdays,
			opts: dueOnly("2026-11-19"),
		},
		{
			name: "a date the series lands on further on says the occurrences between are never created",
			due:  "2026-11-18", rule: weekdays,
			opts: dueOnly("2026-11-23"), // past Thu 19 and Fri 20
			want: []string{
				"moves THIS occurrence only — the 2 occurrences between its old date and this one are never created",
				"a deadline that has to outlast occurrences belongs on another task's due",
				"the series stands where it was: a close now mints the next one for 2026-11-24 23:59 +09:00",
			},
			// The series lands there: re-anchoring would change nothing.
			not: []string{"--repeat", "to move the series"},
		},
		{
			name: "one occurrence passed is named in the singular",
			due:  "2026-11-18", rule: weekdays,
			opts: dueOnly("2026-11-20"),
			want: []string{"the occurrence between its old date and this one is never created"},
		},
		{
			// Counted from the old due, not from the close window: a close now
			// would settle Tue 17 and mint Wed 18 alone, but Fri 13 to Wed 18
			// lapsed too, and after this write no close reports them skipped.
			name: "an overdue occurrence moved ahead counts from its old date",
			due:  "2026-11-12", rule: weekdays,
			opts: dueOnly("2026-11-19"),
			want: []string{"the 4 occurrences between its old date and this one"},
		},
		{
			name: "a delta stays silent even when it passes occurrences",
			due:  "2026-11-18", rule: weekdays,
			opts: dueOnly("+7d"),
		},
		{
			// The same slot, spelled as the instant the shard stores.
			name: "the spelling does not matter, the date does",
			due:  "2026-11-18", rule: weekdays,
			opts: dueOnly("2026-11-19T14:59:59Z"),
		},
		{
			name: "a day the rule does not name cannot be handed the stored rule",
			due:  "2026-11-18", rule: weekdays,
			opts: dueOnly("2026-11-21"), // a Saturday
			want: []string{
				"the 2 occurrences between its old date and this one are never created",
				"a close now mints the next one for 2026-11-23 23:59 +09:00",
				"bind a rule that lands on this date",
				"--due 2026-11-21 --repeat <rule>`",
				"FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR, does not",
			},
			not: []string{"--repeat 'FREQ"},
		},
		{
			name: "a bare monthly moved to another day of the month",
			due:  "2026-12-15", rule: "monthly",
			opts: dueOnly("2026-12-20"),
			want: []string{
				"a close now mints the next one for 2027-01-15 23:59 +09:00",
				"--due 2026-12-20 --repeat 'FREQ=MONTHLY'`",
			},
		},
		{
			name: "a timed series moved earlier the same day still fires that evening",
			due:  "2026-11-18T21:00", rule: "daily",
			opts: dueOnly("2026-11-18T11:00"),
			want: []string{"a close now mints the next one for 2026-11-18 21:00 +09:00"},
		},
		{
			name: "a COUNT starts over when the rule is bound again",
			due:  "2026-12-01", rule: "daily for 3 times",
			opts: dueOnly("2026-12-01T08:00"),
			want: []string{"--repeat 'FREQ=DAILY;COUNT=3'` (its COUNT starts over from that date)"},
		},
		{
			name: "past the last date of a bounded rule, a close completes it",
			due:  "2026-11-18", rule: "daily until 2026-11-19",
			opts: dueOnly("2026-11-19T08:00"),
			want: []string{"a close now completes it (the rule has no later occurrence)"},
			not:  []string{"mints the next one", "COUNT", "--repeat"},
		},
		{
			// The close still mints tonight's, but bound from 08:00 the rule
			// has nothing after its first date, and the bind door refuses it.
			name: "a rule the bind would refuse from the new date is not printed as the remedy",
			due:  "2026-11-18T21:00", rule: "daily until 2026-11-20",
			opts: dueOnly("2026-11-20T08:00"),
			want: []string{"a close now mints the next one for 2026-11-20 21:00 +09:00"},
			not:  []string{"--repeat"},
		},
		{
			name: "a zoneless datetime spelled with a space is quoted in the remedy",
			due:  "2026-11-18", rule: weekdays,
			opts: dueOnly("2026-11-18 11:00"),
			want: []string{"--due '2026-11-18 11:00' --repeat '"},
		},
		{
			name: "--due with --repeat IS the re-anchor",
			due:  "2026-11-18", rule: weekdays,
			opts: func(string) SetOpts {
				d, r := "2026-11-18T11:00", weekdays
				return SetOpts{Due: &d, Repeat: &r}
			},
		},
		{
			name: "a write that also closes hands the rule on",
			due:  "2026-11-18", rule: weekdays,
			opts: func(string) SetOpts {
				d := "2026-11-18T11:00"
				return SetOpts{Due: &d, Status: &done}
			},
		},
		{
			// 2026-11-20 is a Friday: the anchor is off the rule's own lattice,
			// which the BIND already said. Re-stating the date moves nothing.
			name: "a due still standing on an off-lattice anchor",
			due:  "2026-11-20", rule: "weekly on mon",
			opts: dueOnly("2026-11-20"),
		},
		{
			name: "a task that does not repeat",
			due:  "2026-11-18",
			opts: dueOnly("2026-11-18T11:00"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// 11:30 on 2026-11-17, in the board's calendar.
			a := newRepeatApp(time.Date(2026, 11, 17, 2, 30, 0, 0, time.UTC))
			var task *core.Task
			if c.rule == "" {
				var err error
				if task, err = a.Add("一回きり", AddOpts{Due: c.due}); err != nil {
					t.Fatal(err)
				}
			} else {
				task = mustAddRepeating(t, a, "点検", c.due, c.rule, AddOpts{})
			}
			o := c.opts(task.Repeat)
			after, _, _, err := a.Set(task.ID, o)
			if err != nil {
				t.Fatalf("Set: %v", err)
			}
			ws := a.SetRepeatWarnings(after, o, task.Due)
			if c.want == nil {
				if len(ws) != 0 {
					t.Fatalf("the write said %q, want silence", ws)
				}
				return
			}
			if len(ws) != 1 {
				t.Fatalf("SetRepeatWarnings = %q, want exactly the off-series note", ws)
			}
			if !strings.HasPrefix(ws[0], task.ID+" repeats") {
				t.Errorf("the note does not open with the task it is about: %q", ws[0])
			}
			for _, w := range c.want {
				if !strings.Contains(ws[0], w) {
					t.Errorf("note lacks %q:\n%s", w, ws[0])
				}
			}
			for _, n := range c.not {
				if strings.Contains(ws[0], n) {
					t.Errorf("note carries %q:\n%s", n, ws[0])
				}
			}
			// The bare message: `note: ` is the CLI's framing, and --json's
			// `repeat_warnings` carries the string as returned.
			if strings.HasPrefix(ws[0], "note:") {
				t.Errorf("the note carries the stderr framing: %q", ws[0])
			}
		})
	}
}

func dueOnly(spelling string) func(string) SetOpts {
	return func(string) SetOpts { return SetOpts{Due: &spelling} }
}

// The date the note names is the one a close then hands back, and the write it
// prints is the one that moves the series: a note whose remedy did something
// else would be the misleading surface this one exists to remove.
func TestTheOffSeriesNoteNamesWhatACloseDoesAndTheWriteThatChangesIt(t *testing.T) {
	now := time.Date(2026, 11, 17, 2, 30, 0, 0, time.UTC)
	clock := "2026-11-18T11:00"

	// Left alone, the close mints what the note said it would.
	a := newRepeatApp(now)
	task := mustAddRepeating(t, a, "点検", "2026-11-18", "weekly on mon,tue,wed,thu,fri", AddOpts{})
	moved, _, _, err := a.Set(task.ID, SetOpts{Due: &clock})
	if err != nil {
		t.Fatal(err)
	}
	ws := a.SetRepeatWarnings(moved, SetOpts{Due: &clock}, task.Due)
	if len(ws) != 1 || !strings.Contains(ws[0], "next one for 2026-11-19 23:59 +09:00") {
		t.Fatalf("SetRepeatWarnings = %q, want the note naming 2026-11-19 23:59", ws)
	}
	_, rep, err := a.moveOne(task.ID, a.Cfg.DoneLane)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 19, 14, 59, 59, 0, time.UTC); rep == nil || rep.Due == nil || !rep.Due.Equal(want) {
		t.Errorf("the close minted %v, want %s — the date the note named", rep, want)
	}

	// The printed write — the same --due, the stored rule — moves the series,
	// and has nothing left to say about itself.
	b := newRepeatApp(now)
	task = mustAddRepeating(t, b, "点検", "2026-11-18", "weekly on mon,tue,wed,thu,fri", AddOpts{})
	rule := task.Repeat
	remedy := SetOpts{Due: &clock, Repeat: &rule}
	rebound, _, _, err := b.Set(task.ID, remedy)
	if err != nil {
		t.Fatalf("the remedy the note prints was refused: %v", err)
	}
	if ws := b.SetRepeatWarnings(rebound, remedy, task.Due); len(ws) != 0 {
		t.Errorf("the remedy raised %q", ws)
	}
	_, rep, err = b.moveOne(task.ID, b.Cfg.DoneLane)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 19, 2, 0, 0, 0, time.UTC); rep == nil || rep.Due == nil || !rep.Due.Equal(want) {
		t.Errorf("after the remedy the close minted %v, want %s — the series at the typed clock", rep, want)
	}
}

// What the COUNT clause warns of: the count is measured from the anchor, so a
// rule bound again on its second occurrence hands out a full count from there.
func TestRebindingACountedRuleStartsItsCountOver(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 11, 17, 2, 30, 0, 0, time.UTC))
	first := mustAddRepeating(t, a, "三回", "2026-12-01", "daily for 3 times", AddOpts{})
	_, rep, err := a.moveOne(first.ID, a.Cfg.DoneLane)
	if err != nil || rep == nil || rep.Created == nil {
		t.Fatalf("first close: rep=%v err=%v", rep, err)
	}
	second := *rep.Created

	clock := "2026-12-02T08:00"
	moved, _, _, err := a.Set(second, SetOpts{Due: &clock})
	if err != nil {
		t.Fatal(err)
	}
	ws := a.SetRepeatWarnings(moved, SetOpts{Due: &clock}, rep.Due)
	if len(ws) != 1 || !strings.Contains(ws[0], "its COUNT starts over from that date") {
		t.Fatalf("SetRepeatWarnings = %q, want the COUNT clause", ws)
	}

	rule := moved.Repeat
	if _, _, _, err := a.Set(second, SetOpts{Due: &clock, Repeat: &rule}); err != nil {
		t.Fatal(err)
	}
	// One was closed before the rebind; the rebound rule now hands out three
	// more — the occurrence in hand and two after it.
	live, closes := second, 0
	for {
		_, rep, err := a.moveOne(live, a.Cfg.DoneLane)
		if err != nil {
			t.Fatal(err)
		}
		closes++
		if rep == nil || rep.Completed {
			break
		}
		if closes > 10 {
			t.Fatal("the rebound series never ran out")
		}
		live = *rep.Created
	}
	if closes != 3 {
		t.Errorf("the rebound `for 3 times` rule took %d closes to spend, want 3 — the count did not start over", closes)
	}
}

// The occurrences the note says are never created are the ones the series then
// lacks: the close hands back the occurrence after the new date, and nothing
// stands for the dates between. A date moved back again passes nothing.
func TestALandingDateFurtherOnLeavesTheOccurrencesBetweenUncreated(t *testing.T) {
	a := newRepeatApp(time.Date(2026, 11, 17, 2, 30, 0, 0, time.UTC))
	task := mustAddRepeating(t, a, "点検", "2026-11-18", "weekly on mon,tue,wed,thu,fri", AddOpts{})
	later := "2026-11-23"
	moved, _, _, err := a.Set(task.ID, SetOpts{Due: &later})
	if err != nil {
		t.Fatal(err)
	}
	ws := a.SetRepeatWarnings(moved, SetOpts{Due: &later}, task.Due)
	if len(ws) != 1 || !strings.Contains(ws[0], "the 2 occurrences between") || !strings.Contains(ws[0], "next one for 2026-11-24 23:59 +09:00") {
		t.Fatalf("SetRepeatWarnings = %q, want the 2 occurrences passed and 2026-11-24 next", ws)
	}

	back := "2026-11-19"
	again, _, _, err := a.Set(task.ID, SetOpts{Due: &back})
	if err != nil {
		t.Fatal(err)
	}
	if ws := a.SetRepeatWarnings(again, SetOpts{Due: &back}, moved.Due); len(ws) != 0 {
		t.Errorf("a date moved back onto the series said %q, want silence", ws)
	}
	if _, _, _, err := a.Set(task.ID, SetOpts{Due: &later}); err != nil {
		t.Fatal(err)
	}

	_, rep, err := a.moveOne(task.ID, a.Cfg.DoneLane)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 24, 14, 59, 59, 0, time.UTC); rep == nil || rep.Due == nil || !rep.Due.Equal(want) {
		t.Fatalf("the close minted %v, want %s — the date the note named", rep, want)
	}
	if rep.Skipped != 0 {
		t.Errorf("the close reported %d skipped — the occurrences passed were never due to lapse", rep.Skipped)
	}
	idx, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(idx.Tasks); n != 2 {
		t.Errorf("the board holds %d tasks, want 2 — the closed one and the 2026-11-24 occurrence", n)
	}
}

// On a whole-day series a clocked occurrence still stands for its day: a close
// settles the day, so the slot at that day's end is the one being moved, not
// one passed. Counting from the clock instead named it "never created" on a
// move to the very next slot, and one too many after that.
func TestAClockedOccurrenceOfAWholeDaySeriesPassesNothingOnItsOwnDay(t *testing.T) {
	cases := []struct {
		to   string
		want string // "" = silence
	}{
		{to: "2026-11-19"},
		{to: "2026-11-20", want: "the occurrence between its old date and this one is never created"},
		{to: "2026-11-20T11:00", want: "the occurrence between its old date and this one is never created"},
	}
	for _, c := range cases {
		t.Run(c.to, func(t *testing.T) {
			a := newRepeatApp(time.Date(2026, 11, 17, 2, 30, 0, 0, time.UTC))
			task := mustAddRepeating(t, a, "点検", "2026-11-18", "weekly on mon,tue,wed,thu,fri", AddOpts{})
			clock := "2026-11-18T11:00"
			clocked, _, _, err := a.Set(task.ID, SetOpts{Due: &clock})
			if err != nil {
				t.Fatal(err)
			}
			to := c.to
			moved, _, _, err := a.Set(task.ID, SetOpts{Due: &to})
			if err != nil {
				t.Fatal(err)
			}
			ws := a.SetRepeatWarnings(moved, SetOpts{Due: &to}, clocked.Due)
			if c.want == "" {
				if len(ws) != 0 {
					t.Fatalf("the write said %q, want silence", ws)
				}
				return
			}
			if len(ws) != 1 || !strings.Contains(ws[0], c.want) {
				t.Fatalf("SetRepeatWarnings = %q, want %q", ws, c.want)
			}
		})
	}
}
