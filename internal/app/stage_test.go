package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"netease-music-mygo/internal/lyrics"
)

// All three lyric views choose YRC for the whole song and fall back to LRC together.
func TestLyricViewsShareSelectedSource(t *testing.T) {
	const lrc = "[00:24.406]Fallback\n[00:24.980]Chorus"
	const translation = "[00:24.406]Opening translation\n[00:24.980]Shared translation"
	for _, tc := range []struct{ name, yrc, text, translated, omitted string }{
		{"YRC", "[25250,3930](25250,3930,0)Chorus", "Chorus", "Shared translation", "Fallback"},
		{"LRC", "[999999999,500](999999999,500,0)Invalid YRC", "Chorus", "Shared translation", "Invalid YRC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, pane := shortLyricsApp(nil, 360, 300)
			a.pl.RefreshLyrics(1, lrc, translation, tc.yrc)
			a.pl.Hold(26 * time.Second)
			pane.Frame()
			stage := ui.NewTesterWith(func(c *ui.Context) { a.stage(c, a.pl.Current(), white) }, 360, 300,
				ui.TesterOptions{Preferences: ui.Preferences{ReduceMotion: true}})
			desktop := ui.NewTester((&deskLyrics{a: a}).view, 480, 200)
			for _, view := range []*ui.Tester{pane, stage, desktop} {
				if !view.HasText(tc.text) || !view.HasText(tc.translated) || view.HasText(tc.omitted) {
					t.Fatalf("view used the wrong source or lost its translation: %q", view.Texts())
				}
			}
			if tc.name == "LRC" && !pane.HasText("Fallback") {
				t.Fatal("whole-song fallback lost the LRC-only row")
			}
		})
	}
}

// Choosing a style from the stage's menu sets its lyrics in it, and
// choosing 流光 goes back to the classic.
func TestStageStyleMenu(t *testing.T) {
	a, tt, _ := playingApp()
	a.nowPlaying, a.npPane = true, paneStage
	a.pl.Seek(2 * time.Second)
	tt.Frame()
	for _, c := range []struct{ item, want string }{{"云阶", "partita"}, {"倾诉", "tilt"}, {"流光", ""}} {
		if err := tt.Click("律动效果"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem(c.item); err != nil {
			t.Fatalf("%v; the menu shows %q", err, tt.Menu())
		}
		tt.Frame()
		if a.settings.StageStyle != c.want {
			t.Errorf("chose %s: style %q, want %q", c.item, a.settings.StageStyle, c.want)
		}
	}
}

// 火花 in the stage's menu turns the sparks on, and again off, whatever
// the style.
func TestStageSparksMenu(t *testing.T) {
	a, tt, _ := playingApp()
	a.nowPlaying, a.npPane = true, paneStage
	a.settings.StageStyle = "tilt"
	tt.Frame()
	for _, want := range []bool{true, false} {
		if err := tt.Click("律动效果"); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem("火花"); err != nil {
			t.Fatalf("%v; the menu shows %q", err, tt.Menu())
		}
		tt.Frame()
		if a.settings.StageSparks != want || a.settings.StageStyle != "tilt" {
			t.Errorf("sparks %v in %q, want %v in tilt", a.settings.StageSparks, a.settings.StageStyle, want)
		}
	}
}

func words(texts ...string) []lyrics.Word {
	out := make([]lyrics.Word, len(texts))
	for i, s := range texts {
		out[i] = lyrics.Word{Text: s, Start: time.Duration(i) * time.Second, End: time.Duration(i+1) * time.Second}
	}
	return out
}

// A line breaks at its spaces and punctuation, punctuation staying with
// the word before it and a contraction whole, and a long run of
// characters in pieces.
func TestStageUnits(t *testing.T) {
	for _, c := range []struct {
		words []lyrics.Word
		want  [][2]int
	}{
		{words("从", "不 ", "主", "动 ", "示", "弱"), [][2]int{{0, 2}, {2, 4}, {4, 6}}},
		{words("Hello", ", ", "It", "’", "s ", "me"), [][2]int{{0, 2}, {2, 5}, {5, 6}}},
		{words("I ", "never ", "show"), [][2]int{{0, 1}, {1, 2}, {2, 3}}},
		{words("曾", "经", "的", "那", "些", "发", "生", "过"), [][2]int{{0, 4}, {4, 8}}},
	} {
		got := stageUnits(c.words)
		if len(got) != len(c.want) {
			t.Errorf("%v: units %v, want %v", c.words, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%v: units %v, want %v", c.words, got, c.want)
				break
			}
		}
	}
}

// 倾诉 splits a line into parts that follow one another, none empty, and
// rather after punctuation than nearest the middle.
func TestTiltSplitsAtPunctuation(t *testing.T) {
	ws := words("夜", "空", "中", "最", "亮", "的", "星，", "能", "否", "听", "清")
	units := stageUnits(ws)
	for n := 1; n <= min(4, len(units)); n++ {
		parts := splitParts(ws, units, n)
		if len(parts) != n || parts[0].from != 0 || parts[n-1].to != len(ws) {
			t.Fatalf("%d parts: %+v", n, parts)
		}
		for i := 1; i < n; i++ {
			if parts[i].from != parts[i-1].to || parts[i].from >= parts[i].to {
				t.Fatalf("%d parts: %+v", n, parts)
			}
		}
	}
	if parts := splitParts(ws, units, 2); parts[0].to != 7 {
		t.Errorf("split at %d, not after 星，", parts[0].to)
	}
}

// 云阶 lays out the line, size and stage height it is given: it keeps the
// one it laid out last, and handing that back for another line, or for a
// stage of another height, would set a line's words where the last line's
// sat.
func TestPartitaLayoutFollowsItsLineAndStage(t *testing.T) {
	a := newTestApp()
	line := func(at time.Duration, texts ...string) *lyrics.Line {
		l := &lyrics.Line{At: at, End: at + time.Second, Timed: true}
		for i, text := range texts {
			start := at + time.Duration(i)*200*time.Millisecond
			l.Words = append(l.Words, lyrics.Word{Text: text, Start: start, End: start + 150*time.Millisecond})
			l.Text += text
		}
		return l
	}
	eight := line(0, "one", "two", "three", "four", "five", "six", "seven", "eight")
	three := line(10*time.Second, "nine", "ten", "eleven")
	font := lyricTypeface{}

	// Two lines at the same size, then the first line on a shorter stage:
	// each settles over the one before it, as the stage shows them.
	var tall, shorter, tallerAgain, other, bigger []partitaRowLayout
	ui.NewTesterWith(func(c *ui.Context) {
		tall = a.laidOutPartita(c, eight, 40, 900, font)
		other = a.laidOutPartita(c, three, 40, 900, font)
		shorter = a.laidOutPartita(c, eight, 40, 300, font)
		tallerAgain = a.laidOutPartita(c, eight, 40, 900, font)
		bigger = a.laidOutPartita(c, eight, 80, 900, font)
	}, 360, 300, ui.TesterOptions{})

	// The rows describe the line asked for, not the one laid out before it.
	if len(tall) < 2 {
		t.Fatalf("eight words on a 900 tall stage: %d rows, want more than one", len(tall))
	}
	if last := tall[len(tall)-1]; last.to != len(eight.Words) || !last.shapes[len(last.shapes)-1].last {
		t.Errorf("eight words: the last row ends at word %d of %d, and its last word ends the line", last.to, len(eight.Words))
	}
	if len(other) > len(three.Words) {
		t.Fatalf("three words after eight: %d rows for %d words", len(other), len(three.Words))
	}
	if last := other[len(other)-1]; last.to != len(three.Words) {
		t.Errorf("three words after eight: the last row ends at word %d, want %d", last.to, len(three.Words))
	}
	if got := len(other[0].shapes); got == len(tall[0].shapes) {
		t.Errorf("the shapes of the line before were kept for another line of other words")
	}
	// A shorter stage stacks the same line in fewer rows.
	if len(shorter) >= len(tall) {
		t.Fatalf("300 tall: %d rows, not fewer than the %d of a 900 tall stage", len(shorter), len(tall))
	}
	// The words are measured at the size they are given: a larger size leaves
	// more room after each one.
	if bigger[0].shapes[0].margin <= tallerAgain[0].shapes[0].margin {
		t.Errorf("measured at 80: %g after the first word, no more than the %g of 40",
			bigger[0].shapes[0].margin, tallerAgain[0].shapes[0].margin)
	}
	// The same line at the same height comes back as it was.
	if len(tallerAgain) != len(tall) || tallerAgain[0].from != tall[0].from || tallerAgain[0].x != tall[0].x {
		t.Errorf("the same line came back laid out differently: %+v, then %+v", tall[0].partitaRow, tallerAgain[0].partitaRow)
	}
}
