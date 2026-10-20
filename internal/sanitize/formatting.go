package sanitize

import (
	"strings"

	"golang.org/x/net/html"
)

// MaxActiveFormatting bounds how many formatting elements BoundFormatting lets
// be active at once. Real mail rarely nests more than four or five.
const MaxActiveFormatting = 8

// formattingTags are the HTML5 "formatting elements" (§13.2.4.3). They are the
// only elements the tree builder clones: whenever a new element or text starts
// after a block has implicitly closed them, it re-creates every entry in the
// list of active formatting elements. Entries that differ in an attribute are
// not deduplicated, so without a bound one 4-byte "<p>x" can clone hundreds of
// elements, and the parse and its rendering grow with the product.
var formattingTags = map[string]bool{
	"a": true, "b": true, "big": true, "code": true, "em": true, "font": true,
	"i": true, "nobr": true, "s": true, "small": true, "strike": true,
	"strong": true, "tt": true, "u": true,
}

// BoundFormatting drops formatting start tags (and their end tags) that would
// make more than MaxActiveFormatting formatting elements active at once, so
// html.Parse clones at most that many elements per token. Markup that never
// exceeds the bound is returned byte for byte.
//
// The input must already be sanitized by one of this package's policies: the
// model below is exact only for their element set, which has no foreign
// content, no raw-text elements, no <select>, and no markers other than td, th
// and caption. Use it on stored bodies before parsing them.
func BoundFormatting(h string) string {
	return boundFormatting(h, MaxActiveFormatting)
}

type fmtEntry struct {
	name string
	// depth is how many elements were open in the region when it was pushed.
	depth int
}

// fmtRegion is a stretch of the list of active formatting elements: the
// document root, a table (entries foster-parented into it), or a cell or
// caption (which the tree builder opens with a marker and clears on close).
type fmtRegion struct {
	kind    string // "", "table", "td", "th" or "caption"
	entries []fmtEntry
	// dropped counts start tags left out, per name, to drop their end tags.
	dropped map[string]int
	// open is the region's non-formatting elements, popped only by an end
	// tag naming the innermost one, so it never holds fewer than the parser.
	open []string
}

func isCellRegion(kind string) bool {
	return kind == "td" || kind == "th" || kind == "caption"
}

// voidElements never stay open.
var voidElements = map[string]bool{"br": true, "col": true, "hr": true, "img": true, "wbr": true}

// boundFormatting tracks an upper bound of the tree builder's list of active
// formatting elements after its last marker. Where the model cannot be sure the
// parser removed an entry, it keeps counting it, so it may drop formatting from
// odd markup but never under-counts. maxActive 0 removes all formatting.
func boundFormatting(h string, maxActive int) string {
	regions := []*fmtRegion{{}}
	top := func() *fmtRegion { return regions[len(regions)-1] }
	push := func(kind string) { regions = append(regions, &fmtRegion{kind: kind}) }
	// closeCell discards a cell's entries, as the tree builder clears the list
	// back to the cell's marker when the cell closes.
	closeCell := func() { regions = regions[:len(regions)-1] }
	// closeKeeping keeps a closed region's entries: no marker clears them.
	// If the region's elements are certainly popped, nothing is open above
	// those entries; otherwise they are never removed again.
	closeKeeping := func(popped bool) {
		r := top()
		regions = regions[:len(regions)-1]
		t := top()
		depth := -1
		if popped {
			depth = len(t.open)
		}
		for _, e := range r.entries {
			t.entries = append(t.entries, fmtEntry{name: e.name, depth: depth})
		}
	}
	// active counts entries after the innermost marker.
	active := func() int {
		n := 0
		for i := len(regions) - 1; i >= 0; i-- {
			n += len(regions[i].entries)
			if isCellRegion(regions[i].kind) {
				break
			}
		}
		return n
	}
	// remove takes the last entry named name out of the region if the parser
	// certainly removes it. With elements open above it the adoption agency
	// algorithm may give up and leave a replacement entry, so it then stays.
	remove := func(r *fmtRegion, name string) {
		for i := len(r.entries) - 1; i >= 0; i-- {
			if r.entries[i].name == name {
				if len(r.open) <= r.entries[i].depth {
					r.entries = append(r.entries[:i], r.entries[i+1:]...)
				}
				return
			}
		}
	}

	var out strings.Builder
	out.Grow(len(h))
	changed := false
	z := html.NewTokenizer(strings.NewReader(h))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			out.Write(z.Raw())
			break
		}
		keep := true
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			b, _ := z.TagName()
			name := string(b)
			r := top()
			switch {
			case formattingTags[name]:
				if name == "a" {
					// A new <a> closes and removes any <a> still in the list.
					remove(r, "a")
				}
				if keep = active() < maxActive; keep {
					r.entries = append(r.entries, fmtEntry{name: name, depth: len(r.open)})
				} else {
					if r.dropped == nil {
						r.dropped = make(map[string]int)
					}
					r.dropped[name]++
				}
			case name == "table":
				// A table directly in a table closes it and opens a new one.
				if r.kind == "table" {
					closeKeeping(true)
				}
				push("table")
			case name == "th" && r.kind == "caption":
				// x/net/html ignores <th> in a caption (the standard closes it).
			case name == "td" || name == "th" || name == "caption":
				if isCellRegion(r.kind) {
					closeCell()
				}
				if top().kind == "table" {
					push(name)
				}
			case name == "tr" || name == "tbody" || name == "thead" || name == "tfoot" ||
				name == "col" || name == "colgroup":
				if isCellRegion(r.kind) {
					closeCell()
				}
			case !voidElements[name]:
				r.open = append(r.open, name)
			}
		case html.EndTagToken:
			b, _ := z.TagName()
			name := string(b)
			r := top()
			switch {
			case formattingTags[name]:
				if r.dropped[name] > 0 {
					r.dropped[name]--
					keep = false
				} else {
					remove(r, name)
				}
			case name == "td" || name == "th" || name == "caption":
				if r.kind == name {
					closeCell()
				}
			case name == "tr":
				if r.kind == "td" || r.kind == "th" {
					closeCell()
				}
			case name == "tbody" || name == "thead" || name == "tfoot":
				// Closes the cell only if that section is open; keep counting
				// its entries in case it was not.
				if r.kind == "td" || r.kind == "th" {
					closeKeeping(false)
				}
			case name == "table":
				for isCellRegion(top().kind) {
					closeCell()
				}
				if top().kind == "table" {
					closeKeeping(true)
				}
			default:
				if n := len(r.open); n > 0 && r.open[n-1] == name {
					r.open = r.open[:n-1]
				}
			}
		}
		if keep {
			out.Write(z.Raw())
		} else {
			changed = true
		}
	}
	if !changed {
		return h
	}
	return out.String()
}
