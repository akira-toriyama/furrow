package main

import (
	"fmt"
	"strings"

	lg "charm.land/lipgloss/v2"
)

// The flat table view, because GitHub Projects has one. It is hand-rolled
// rather than bubbles/v2's table: that widget is flat (Row []string) with no
// per-cell styling and no horizontal scroll, and it would measure CJK columns
// with its own width logic instead of lipgloss's.

// tableRows is the filtered board flattened in lane-then-priority order.
func (m *Model) tableRows() []*Task {
	var out []*Task
	for _, l := range m.b.Lanes() {
		out = append(out, m.cols[l.Name]...)
	}
	return out
}

func (m *Model) renderTable() string {
	th := m.th
	rows := m.tableRows()

	wCur, wID, wLane, wFlag, wVE, wRepo, wLbl, wDep := 1, 8, 12, 4, 4, 10, 12, 5
	fixed := wCur + wID + wLane + wFlag + wVE + wRepo + wLbl + wDep + 8
	wTitle := maxInt(16, m.w-fixed-1)

	head := strings.Join([]string{
		pad("", wCur), pad("id", wID), pad("lane", wLane), pad("", wFlag), pad("v/e", wVE),
		pad("title", wTitle), pad("repo", wRepo), pad("labels", wLbl), pad("deps", wDep),
	}, " ")

	var body []string
	for i, t := range rows {
		glyph, styleFor := cardMarker(t, m.g)
		deps := ""
		if n := len(t.Deps); n > 0 {
			if b := len(m.g.BlockedBy(t.ID)); b > 0 {
				deps = th.danger.Render(fmt.Sprintf("%d/%d", b, n))
			} else {
				deps = th.ok.Render(fmt.Sprintf("0/%d", n))
			}
		}
		ve := ""
		if t.Value > 0 || t.Effort > 0 {
			ve = fmt.Sprintf("%d/%d", t.Value, t.Effort)
		}
		cur := " "
		if i == m.tableIdx {
			// A glyph, not just colour: `-dump -plain` has to show the cursor.
			cur = "▌"
		}
		plain := []string{
			cur, pad(t.ID, wID), pad(t.Status, wLane), pad(glyph, wFlag), pad(ve, wVE),
			pad(t.Title, wTitle), pad(t.ShortRepo(), wRepo),
			pad(strings.Join(t.Labels, ","), wLbl), pad(ansiStrip(deps), wDep),
		}
		var line string
		if i == m.tableIdx {
			// One inverse band rather than a patchwork of per-cell colours.
			line = th.invert.Render(pad(strings.Join(plain, " "), m.w))
		} else {
			line = strings.Join([]string{
				plain[0],
				th.dim.Render(plain[1]),
				plain[2],
				styleFor(th).Render(plain[3]),
				plain[4],
				plain[5],
				th.chipAlt.Render(plain[6]),
				th.muted.Render(plain[7]),
				pad(deps, wDep),
			}, " ")
		}
		body = append(body, pad(line, m.w))
	}

	layers := []*lg.Layer{
		lg.NewLayer(blankCanvas(m.w, m.h)).X(0).Y(0).Z(zChrome - 1),
	}
	layers = append(layers, m.chromeLayers()...)
	layers = append(layers,
		lg.NewLayer(th.colHdr.Render(pad(head, m.w))).X(0).Y(rowColHdr).Z(zChrome),
		// maxInt, not m.w: strings.Repeat panics on a negative count, and
		// `-dump -w -1` reached it.
		lg.NewLayer(th.rule.Render(strings.Repeat("─", maxInt(m.w, 1)))).X(0).Y(rowColSum).Z(zChrome),
	)

	const tableTop = rowRule // the table needs no per-column rule row
	visRows := m.h - tableTop - footerH
	top := clamp(m.tableIdx-visRows/2, 0, maxInt(0, len(body)-visRows))
	for i := top; i < len(body) && tableTop+i-top < m.h-footerH; i++ {
		layers = append(layers, lg.NewLayer(body[i]).X(0).Y(tableTop+i-top).Z(zCard))
	}
	if m.peekOpen {
		layers = append(layers, m.peekLayer())
	}
	if m.fullHelp {
		layers = append(layers, m.helpLayer())
	}
	return m.fitFrame(lg.NewCompositor(layers...).Render())
}

// ansiStrip removes escape sequences. Used for the inverse-highlighted table
// row and for `-dump -plain`, whose whole point is a diffable frame.
//
// It handles the WHOLE CSI/OSC grammar, not just SGR. The narrower version it
// replaces ended a sequence only on 'm' or 'K', which is fine for lipgloss
// output (SGR only) and silently corrupting on anything else: given
// "\x1b[6;31Hmoved" it kept hunting for a terminator, found the 'm' of the WORD
// "moved", and ate the live text in between. A stripper that deletes real
// content mid-word is a trap for the next person who points it at a captured
// terminal stream.
func ansiStrip(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			i++
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		switch s[i] {
		case '[': // CSI: parameter/intermediate bytes, then a final byte in @..~
			i++
			for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
				i++
			}
			i++
		case ']': // OSC: runs to BEL or ST
			i++
			for i < len(s) {
				if s[i] == 0x07 {
					i++
					break
				}
				if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		default: // a two-byte escape
			i++
		}
	}
	return b.String()
}
